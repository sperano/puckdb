package simulation

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sperano/puckdb/llm"
)

// Tool names. Constants rather than literals so the activity dispatch
// switch in agent.go and the per-tool parsers stay in lockstep with the
// tool definitions — a typo'd name on either side is a compile error.
const (
	ToolDraftPlayer = "draft_player"
	ToolSetLineup   = "set_lineup"
	ToolAddPlayer   = "add_player"
	ToolClaimPlayer = "claim_player"
	ToolDropPlayer  = "drop_player"
	ToolUpdateNotes = "update_notes"
	ToolSetTeamName = "set_team_name"
)

// Argument structs — one per tool. JSON tags match the schema property
// names below. Pointer fields are used for OPTIONAL integers
// (drop_player_id) so a missing field unmarshals to nil rather than 0;
// 0 is a meaningful "no player" sentinel that would silently mask a
// schema mismatch.

type DraftPlayerArgs struct {
	PlayerID int64  `json:"player_id"`
	Reason   string `json:"reason,omitempty"`
}

type LineupMoveArg struct {
	PlayerID int64      `json:"player_id"`
	Slot     RosterSlot `json:"slot"`
}

type SetLineupArgs struct {
	Moves  []LineupMoveArg `json:"moves"`
	Reason string          `json:"reason,omitempty"`
}

type AddPlayerArgs struct {
	PlayerID     int64  `json:"player_id"`
	DropPlayerID *int64 `json:"drop_player_id,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type ClaimPlayerArgs struct {
	PlayerID     int64  `json:"player_id"`
	DropPlayerID *int64 `json:"drop_player_id,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type DropPlayerArgs struct {
	PlayerID int64  `json:"player_id"`
	Reason   string `json:"reason,omitempty"`
}

type UpdateNotesArgs struct {
	Notes string `json:"notes"`
}

type SetTeamNameArgs struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// ============================================================================
// Tool schemas (raw JSON, kept as strings for readability and so the
// schema and the JSON Schema property names live next to each other).
// Each schema is a JSON object literal with type / properties / required.
// ============================================================================

const draftPlayerSchema = `{
  "type": "object",
  "properties": {
    "player_id": {
      "type": "integer",
      "description": "NHL player ID to draft from the available pool."
    },
    "reason": {
      "type": "string",
      "description": "One short sentence explaining why this player. Logged in sim_transactions.reasoning for human review.",
      "maxLength": 200
    }
  },
  "required": ["player_id", "reason"]
}`

const setLineupSchema = `{
  "type": "object",
  "properties": {
    "moves": {
      "type": "array",
      "description": "Lineup moves applied in array order (not atomically). To swap two active players, move A to slot_of_B (B is auto-displaced to BN), then move B to slot_of_A.",
      "items": {
        "type": "object",
        "properties": {
          "player_id": { "type": "integer" },
          "slot": {
            "type": "string",
            "enum": ["C", "LW", "RW", "D", "G", "Util", "BN", "IR"]
          }
        },
        "required": ["player_id", "slot"]
      }
    },
    "reason": {
      "type": "string",
      "description": "One short sentence explaining why this lineup. Logged in sim_transactions.reasoning for human review.",
      "maxLength": 200
    }
  },
  "required": ["moves", "reason"]
}`

const addPlayerSchema = `{
  "type": "object",
  "properties": {
    "player_id": {
      "type": "integer",
      "description": "NHL player ID of the free agent to add."
    },
    "drop_player_id": {
      "type": "integer",
      "description": "Required if roster is full (21 players). The roster player to drop to make room. If omitted while roster is full, the call fails with no retry."
    },
    "reason": {
      "type": "string",
      "description": "One short sentence explaining why this add. Logged in sim_transactions.reasoning for human review.",
      "maxLength": 200
    }
  },
  "required": ["player_id", "reason"]
}`

const claimPlayerSchema = `{
  "type": "object",
  "properties": {
    "player_id": {
      "type": "integer",
      "description": "NHL player ID of the player on waivers to claim."
    },
    "drop_player_id": {
      "type": "integer",
      "description": "Required if roster is full. Drop is executed only if the claim wins."
    },
    "reason": {
      "type": "string",
      "description": "One short sentence explaining why this claim. Logged in sim_transactions.reasoning for human review.",
      "maxLength": 200
    }
  },
  "required": ["player_id", "reason"]
}`

const dropPlayerSchema = `{
  "type": "object",
  "properties": {
    "player_id": {
      "type": "integer",
      "description": "NHL player ID of the rostered player to release. They go on waivers for waiver_days, then become a free agent if unclaimed."
    },
    "reason": {
      "type": "string",
      "description": "One short sentence explaining why this drop. Logged in sim_transactions.reasoning for human review.",
      "maxLength": 200
    }
  },
  "required": ["player_id", "reason"]
}`

const setTeamNameSchema = `{
  "type": "object",
  "properties": {
    "name": {
      "type": "string",
      "description": "Your team's name. Memorable, 1-50 chars. Reflect your strategy or persona.",
      "minLength": 1,
      "maxLength": 50
    },
    "summary": {
      "type": "string",
      "description": "Very terse strategy label (<= 30 chars). Shown in dashboards alongside your team name. Examples: \"Punt GAA\", \"Stack SOG forwards\", \"Stream goalies\".",
      "minLength": 1,
      "maxLength": 30
    }
  },
  "required": ["name", "summary"]
}`

const updateNotesSchema = `{
  "type": "object",
  "properties": {
    "notes": {
      "type": "string",
      "description": "Replaces the agent's persistent notes. Carries across days. Hard cap 50000 bytes — oversized writes are rejected by the database CHECK constraint and surface as a tool error."
    }
  },
  "required": ["notes"]
}`

// makeTool builds a single tool, eliminating the boilerplate around the
// nested ToolFunction + json.RawMessage wrapping. Cacheable goes on the
// LAST tool in each phase's tool list — see DraftTools / DailyTools.
func makeTool(name, description, schema string, cacheable bool) llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        name,
			Description: description,
			Parameters:  json.RawMessage(schema),
		},
		Cacheable: cacheable,
	}
}

// TeamNameTools returns the tool list visible during the one-shot
// team-name pick phase that runs before the draft. A single tool so
// the agent has exactly one valid action.
func TeamNameTools() []llm.Tool {
	return []llm.Tool{
		makeTool(
			ToolSetTeamName,
			"Commit your team's name. Called exactly once at the start of the simulation.",
			setTeamNameSchema,
			false,
		),
	}
}

// DraftTools returns the tool list visible during the draft phase.
//
// Only two tools — `draft_player` (the single decision per turn) and
// `update_notes` (so the agent can build a multi-round draft plan).
// `update_notes` is LAST so it carries the cache_control marker; the
// system prompt + both tool definitions become the agent's cacheable
// prefix and stay byte-identical across all 18 draft rounds.
func DraftTools() []llm.Tool {
	return []llm.Tool{
		makeTool(
			ToolDraftPlayer,
			"Draft a player. Called once per draft turn.",
			draftPlayerSchema,
			false,
		),
		makeTool(
			ToolUpdateNotes,
			"Replace persistent notes. Use during the draft to record positional plans and reasoning that should inform later picks (e.g. \"locked two elite Cs, targeting goalies in rounds 5-6\").",
			updateNotesSchema,
			true, // cacheable boundary
		),
	}
}

// DailyTools returns the tool list visible during the daily-management
// phase (after the draft completes, every calendar day of the regular
// season).
//
// Order matters for the cache: the LAST entry carries the cache_control
// marker. We put `update_notes` last so the cacheable prefix is
// (system + the four roster-management tools + update_notes) — i.e. the
// entire tool definition block. Reordering this list will silently
// invalidate the cache for any in-flight pool, costing real money.
func DailyTools() []llm.Tool {
	return []llm.Tool{
		makeTool(
			ToolSetLineup,
			"Set today's lineup. Moves are applied in array order (not atomically); displaced players auto-move to BN.",
			setLineupSchema,
			false,
		),
		makeTool(
			ToolAddPlayer,
			"Pick up a free agent (a player never owned in this pool, or who cleared waivers). First-processed agent wins same-day contested adds.",
			addPlayerSchema,
			false,
		),
		makeTool(
			ToolClaimPlayer,
			"File a waiver claim on a recently-dropped player. Resolved after the configured waiver period; highest waiver priority wins contested claims.",
			claimPlayerSchema,
			false,
		),
		makeTool(
			ToolDropPlayer,
			"Release a player from your roster. They go on waivers for waiver_days; if unclaimed they become a free agent.",
			dropPlayerSchema,
			false,
		),
		makeTool(
			ToolUpdateNotes,
			"Replace your persistent notes. Notes carry across days; treat as a terse strategy scratchpad rather than a journal.",
			updateNotesSchema,
			true, // cacheable boundary
		),
	}
}

// ParseArgs unmarshals a tool call's Arguments into the requested type.
// Returns a wrapped error if the JSON is malformed, naming the tool so
// the surfaced error is debuggable when 5 tools land in one round.
//
// Decoding is strict: unknown fields are rejected. Without this, a
// model that emits {"team_name":"X"} for set_team_name(name) silently
// unmarshals into a zero-valued struct and the validator surfaces the
// confusing "team name is empty after trim". With DisallowUnknownFields
// the same input fails here with "unknown field \"team_name\"", which
// the round-loop forwards to the LLM as the tool result so it can
// self-correct on the next round.
//
// Type parameter usage: `args, err := ParseArgs[DraftPlayerArgs](call)`.
// The zero value of T is returned on error so callers can ignore the
// returned struct and just check err.
func ParseArgs[T any](call llm.ToolCall) (T, error) {
	var v T
	if call.Function.Arguments == "" {
		// Empty Arguments unmarshals as JSON error from the SDK; return
		// our own clearer message. Some providers (notably tool-call
		// retry shims) emit "" instead of "{}" when the LLM produces no
		// args at all.
		return v, fmt.Errorf("simulation: tool %q called with empty arguments", call.Function.Name)
	}
	dec := json.NewDecoder(strings.NewReader(call.Function.Arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, fmt.Errorf("simulation: parse args for tool %q: %w", call.Function.Name, err)
	}
	return v, nil
}
