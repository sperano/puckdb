// Package simulation runs the AI-agent fantasy hockey roto pool simulator.
// See worker/simulation/PLAN.md for the design.
package simulation

import (
	"fmt"

	"github.com/sperano/puckdb/sqlcdb"
)

// displayName returns the agent's user-visible identifier: its
// team_name when set, "agent #<id>" otherwise. The fallback handles
// (a) the brief window between pool creation and PickTeamName's
// completion, and (b) the permanent state when PickTeamName failed
// (e.g., a model that can't emit a valid set_team_name tool call).
func displayName(a sqlcdb.SimAgent) string {
	if a.TeamName != "" {
		return a.TeamName
	}
	return fmt.Sprintf("agent #%d", a.ID)
}

// PoolStatus is the value stored in sim_pools.status.
type PoolStatus string

const (
	PoolStatusDraft     PoolStatus = "draft"
	PoolStatusRunning   PoolStatus = "running"
	PoolStatusPaused    PoolStatus = "paused"
	PoolStatusComplete  PoolStatus = "complete"
	PoolStatusCancelled PoolStatus = "cancelled"
)

// TransactionType is the value stored in sim_transactions.type.
// See PLAN.md "Per-type column population" for which other columns each
// type populates.
type TransactionType string

const (
	TransactionTypeDraftPick      TransactionType = "draft_pick"
	TransactionTypeAdd            TransactionType = "add"
	TransactionTypeClaim          TransactionType = "claim"
	TransactionTypeDrop           TransactionType = "drop"
	TransactionTypeLineupSet      TransactionType = "lineup_set"
	TransactionTypePass           TransactionType = "pass"
	TransactionTypeError          TransactionType = "error"
	TransactionTypeCostCapReached TransactionType = "cost_cap_reached"
)

// WaiverClaimStatus is the value stored in sim_waiver_claims.status.
// The partial unique index ux_sim_waiver_claims_pending_one_per_agent_player
// is keyed off WaiverClaimStatusPending — moving a claim to any other status
// removes it from the index, freeing the (pool, agent, player) triple for
// a future claim.
type WaiverClaimStatus string

const (
	WaiverClaimStatusPending   WaiverClaimStatus = "pending"
	WaiverClaimStatusWon       WaiverClaimStatus = "won"
	WaiverClaimStatusLost      WaiverClaimStatus = "lost"
	WaiverClaimStatusCancelled WaiverClaimStatus = "cancelled"
)

// RosterSlot is the value stored in sim_rosters.slot.
type RosterSlot string

const (
	SlotC    RosterSlot = "C"
	SlotLW   RosterSlot = "LW"
	SlotRW   RosterSlot = "RW"
	SlotD    RosterSlot = "D"
	SlotG    RosterSlot = "G"
	SlotUtil RosterSlot = "Util"
	SlotBN   RosterSlot = "BN"
	SlotIR   RosterSlot = "IR"
)

// AcquiredVia is the value stored in sim_rosters.acquired_via.
type AcquiredVia string

const (
	AcquiredViaDraft     AcquiredVia = "draft"
	AcquiredViaFreeAgent AcquiredVia = "free_agent"
)

// ErrorKind is the value stored in sim_transactions.error_kind for
// rows with type = TransactionTypeError.
type ErrorKind string

const (
	ErrorKindToolUseFailure ErrorKind = "tool_use_failure"
	ErrorKindLLMError       ErrorKind = "llm_error"
	ErrorKindValidation     ErrorKind = "validation"
)

// AgentConfig is the per-agent configuration loaded from one
// sim_agents row at workflow start (and after each ContinueAsNew).
// LoadPoolStateActivity returns one of these per agent in the pool;
// the workflow stores the slice on simState.Agents and looks up by
// AgentID when it needs to construct an *Agent for an LLM call.
//
// Persistence: every field maps to a typed sim_agents column —
// provider, model, strategy, timeout_seconds, temperature, api_base,
// max_tokens. See sim_agents in 000013_simulation.up.sql.
//
// No Name field — the agent's identity is its team_name, populated
// by PickTeamName in Phase 0 of the workflow. Displays fall back to
// "agent #<id>" during the brief pre-PickTeamName window. See
// migration 000021 for the rationale.
//
// Temperature is a pointer so a zero value (0.0 — valid for
// deterministic sampling) can be distinguished from "unset" (use
// the provider default). Mirrors llm.Request.Temperature.
type AgentConfig struct {
	Provider       string   `json:"provider"`
	Model          string   `json:"model"`
	Strategy       string   `json:"strategy"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	Temperature    *float64 `json:"temperature,omitempty"`
	APIBase        string   `json:"api_base,omitempty"`
	MaxTokens      int      `json:"max_tokens,omitempty"`
}

// PoolConfig is the immutable pool-level configuration loaded from
// the sim_pools row's typed columns. Per-agent configs live in
// sim_agents (loaded separately via ListSimAgentsByPool) and are
// not part of this struct — the workflow's LoadPoolStateResult
// carries them in its own Agents slice.
//
// "Immutable after creation" per PLAN.md > "Configuration > Config
// is immutable" — to change settings, cancel and create a new pool.
type PoolConfig struct {
	Season               int                `json:"season"`
	NumTeams             int                `json:"num_teams"`
	Categories           []string           `json:"categories"`
	RosterPositions      map[RosterSlot]int `json:"roster_positions"`
	WaiverDays           int                `json:"waiver_days"`
	DraftRounds          int                `json:"draft_rounds"`
	MaxLLMCostUsdPerPool float64            `json:"max_llm_cost_usd_per_pool"`

	// StopAfter — config-time scope: the workflow exits cleanly
	// (status=complete) when it reaches the configured phase boundary.
	// See migration 000022 for the values and rationale. Default is
	// StopAfterNever, meaning run to season end.
	StopAfter StopAfter `json:"stop_after"`

	// MaxSeasonDays caps the season day-loop at N days. 0 means no
	// cap (run to the season's actual end date). Useful for fast
	// small-sample debugging: maxSeasonDays=10 lets a sim complete
	// in minutes instead of running the full ~180-day NHL season.
	MaxSeasonDays int `json:"max_season_days"`
}

// StopAfter enumerates the config-time stop points for the workflow.
// Replaces the pre-000022 pause_at/controlState runtime-navigation
// machinery: instead of pausing-and-resuming mid-execution, the
// workflow runs to a configured boundary and exits.
//
// In Go this is a typed int with iota — the DB column is TEXT (the
// String() method serializes to the same labels as the
// sim_pools.stop_after CHECK constraint), and ParseStopAfter converts
// back at the DB / GraphQL boundary.
type StopAfter int

const (
	// StopAfterNever — run to season end (no early exit). Default.
	StopAfterNever StopAfter = iota
	// StopAfterTeamName — exit cleanly after Phase 0 (team-name picks).
	// Useful for inspecting how each agent names its team without
	// burning LLM budget on a draft.
	StopAfterTeamName
	// StopAfterDraft — exit cleanly after the snake draft completes.
	// Useful for inspecting draft picks without running the season.
	StopAfterDraft
	// StopAfterSeason — explicit synonym for "never" — runs to season
	// end. Kept for symmetry with the other values.
	StopAfterSeason
)

// String returns the lowercase label matching the
// sim_pools.stop_after CHECK constraint.
func (s StopAfter) String() string {
	switch s {
	case StopAfterNever:
		return "never"
	case StopAfterTeamName:
		return "team_name"
	case StopAfterDraft:
		return "draft"
	case StopAfterSeason:
		return "season"
	default:
		return "unknown"
	}
}

// ParseStopAfter is the inverse of String(). Returns StopAfterNever
// (the default) for empty input — that's what the JSON zero value
// from an omitted YAML field deserializes to.
func ParseStopAfter(s string) (StopAfter, error) {
	switch s {
	case "", "never":
		return StopAfterNever, nil
	case "team_name":
		return StopAfterTeamName, nil
	case "draft":
		return StopAfterDraft, nil
	case "season":
		return StopAfterSeason, nil
	default:
		return StopAfterNever, fmt.Errorf("invalid stop_after %q: must be one of never|team_name|draft|season", s)
	}
}

// EligibleSlots returns every roster slot a player at the given NHL position
// may fill. The simulator recognizes exactly five positions:
//
//	C  → C, Util, BN, IR
//	LW → LW, Util, BN, IR
//	RW → RW, Util, BN, IR
//	D  → D, Util, BN, IR
//	G  → G, BN, IR     (goalies cannot fill Util)
//
// Returns an error for sqlcdb.PlayerPositionF (PLAN.md V1 simplification —
// no F → C/LW/RW mapping) or any unrecognized value. Callers must surface
// the error rather than silently filtering the player; per PLAN.md
// "Position Eligibility" a non-conforming player is a loud validation
// failure, not a silent drop.
func EligibleSlots(position sqlcdb.PlayerPosition) ([]RosterSlot, error) {
	switch position {
	case sqlcdb.PlayerPositionC:
		return []RosterSlot{SlotC, SlotUtil, SlotBN, SlotIR}, nil
	case sqlcdb.PlayerPositionLW:
		return []RosterSlot{SlotLW, SlotUtil, SlotBN, SlotIR}, nil
	case sqlcdb.PlayerPositionRW:
		return []RosterSlot{SlotRW, SlotUtil, SlotBN, SlotIR}, nil
	case sqlcdb.PlayerPositionD:
		return []RosterSlot{SlotD, SlotUtil, SlotBN, SlotIR}, nil
	case sqlcdb.PlayerPositionG:
		return []RosterSlot{SlotG, SlotBN, SlotIR}, nil
	case sqlcdb.PlayerPositionF:
		return nil, fmt.Errorf("simulation: position %q not supported (V1 simplification: 5 strict positions only — no F mapping)", position)
	default:
		return nil, fmt.Errorf("simulation: unrecognized player position %q (expected C, LW, RW, D, or G)", position)
	}
}

// IsEligibleSlot reports whether a player at the given position may be
// placed in the given slot. Returns an error for unsupported positions
// (same contract as EligibleSlots).
func IsEligibleSlot(position sqlcdb.PlayerPosition, slot RosterSlot) (bool, error) {
	slots, err := EligibleSlots(position)
	if err != nil {
		return false, err
	}
	for _, s := range slots {
		if s == slot {
			return true, nil
		}
	}
	return false, nil
}
