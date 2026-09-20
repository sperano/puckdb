package simulation

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sperano/puckdb/internal/llm"
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

// reasonMaxLength bounds the `reason` argument on every action tool
// (draft_player, set_lineup, add_player, claim_player, drop_player).
// Shared by reasonProperty below so all five schemas and the system
// prompt's "≤200 chars" claim (prompts.go) can't drift independently.
const reasonMaxLength = 200

// reasonProperty returns the JSON Schema fragment for the `reason`
// argument shared by every action tool. `action` completes "One short
// sentence explaining why this <action>." — the only part that varies
// per tool. Extracted so the maxLength/description shape lives in one
// place instead of five hand-copied string literals.
func reasonProperty(action string) string {
	return fmt.Sprintf(`"reason": {
      "type": "string",
      "description": "One short sentence explaining why this %s. Logged in sim_transactions.reasoning for human review.",
      "maxLength": %d
    }`, action, reasonMaxLength)
}

var draftPlayerSchema = fmt.Sprintf(`{
  "type": "object",
  "properties": {
    "player_id": {
      "type": "integer",
      "description": "NHL player ID to draft from the available pool."
    },
    %s
  },
  "required": ["player_id", "reason"]
}`, reasonProperty("player"))

var setLineupSchema = fmt.Sprintf(`{
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
    %s
  },
  "required": ["moves", "reason"]
}`, reasonProperty("lineup"))

var addPlayerSchema = fmt.Sprintf(`{
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
    %s
  },
  "required": ["player_id", "reason"]
}`, reasonProperty("add"))

var claimPlayerSchema = fmt.Sprintf(`{
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
    %s
  },
  "required": ["player_id", "reason"]
}`, reasonProperty("claim"))

var dropPlayerSchema = fmt.Sprintf(`{
  "type": "object",
  "properties": {
    "player_id": {
      "type": "integer",
      "description": "NHL player ID of the rostered player to release. They go on waivers for waiver_days, then become a free agent if unclaimed."
    },
    %s
  },
  "required": ["player_id", "reason"]
}`, reasonProperty("drop"))

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
// nested ToolFunction + json.RawMessage wrapping. Cacheable defaults to
// false here — it is set on the LAST tool of a phase's list by
// markCacheBoundary, not per-call, so the cache boundary can't drift if
// a phase's tool list is ever reordered or extended.
func makeTool(name, description, schema string) llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        name,
			Description: description,
			Parameters:  json.RawMessage(schema),
		},
	}
}

// markCacheBoundary sets Cacheable on the LAST tool in the slice.
// Anthropic's translator places the cache_control marker on the final
// cacheable block in wire order, so the marker must always land on the
// last entry — computing this from len(tools)-1 instead of hand-setting
// a bool on one specific makeTool call means reordering or adding to a
// phase's tool list can never silently leave the marker mid-list (or
// off the list entirely).
func markCacheBoundary(tools []llm.Tool) []llm.Tool {
	if len(tools) > 0 {
		tools[len(tools)-1].Cacheable = true
	}
	return tools
}

// TeamNameTools returns the tool list visible during the one-shot
// team-name pick phase that runs before the draft. A single tool so
// the agent has exactly one valid action. Not cache-boundary-marked:
// this phase runs once per agent, so there's no repeated call for a
// cached prefix to benefit.
func TeamNameTools() []llm.Tool {
	return []llm.Tool{
		makeTool(
			ToolSetTeamName,
			"Commit your team's name. Called exactly once at the start of the simulation.",
			setTeamNameSchema,
		),
	}
}

// DraftTools returns the tool list visible during the draft phase.
//
// Only two tools — `draft_player` (the single decision per turn) and
// `update_notes` (so the agent can build a multi-round draft plan).
// `update_notes` is LAST so it carries the cache_control marker (see
// markCacheBoundary); the system prompt + both tool definitions become
// the agent's cacheable prefix and stay byte-identical across all 18
// draft rounds.
func DraftTools() []llm.Tool {
	return markCacheBoundary([]llm.Tool{
		makeTool(
			ToolDraftPlayer,
			"Draft a player. Called once per draft turn.",
			draftPlayerSchema,
		),
		makeTool(
			ToolUpdateNotes,
			"Replace persistent notes. Use during the draft to record positional plans and reasoning that should inform later picks (e.g. \"locked two elite Cs, targeting goalies in rounds 5-6\").",
			updateNotesSchema,
		),
	})
}

// DailyTools returns the tool list visible during the daily-management
// phase (after the draft completes, every calendar day of the regular
// season).
//
// Order matters for the cache: the LAST entry carries the cache_control
// marker (see markCacheBoundary). We put `update_notes` last so the
// cacheable prefix is (system + the four roster-management tools +
// update_notes) — i.e. the entire tool definition block. Reordering
// this list will silently invalidate the cache for any in-flight pool,
// costing real money.
func DailyTools() []llm.Tool {
	return markCacheBoundary([]llm.Tool{
		makeTool(
			ToolSetLineup,
			"Set today's lineup. Moves are applied in array order (not atomically); displaced players auto-move to BN.",
			setLineupSchema,
		),
		makeTool(
			ToolAddPlayer,
			"Pick up a free agent (a player never owned in this pool, or who cleared waivers). First-processed agent wins same-day contested adds.",
			addPlayerSchema,
		),
		makeTool(
			ToolClaimPlayer,
			"File a waiver claim on a recently-dropped player. Resolved after the configured waiver period; highest waiver priority wins contested claims.",
			claimPlayerSchema,
		),
		makeTool(
			ToolDropPlayer,
			"Release a player from your roster. They go on waivers for waiver_days; if unclaimed they become a free agent.",
			dropPlayerSchema,
		),
		makeTool(
			ToolUpdateNotes,
			"Replace your persistent notes. Notes carry across days; treat as a terse strategy scratchpad rather than a journal.",
			updateNotesSchema,
		),
	})
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
