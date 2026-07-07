package simulation

import (
	"slices"

	"github.com/sperano/puckdb/sqlcdb"
)

// ============================================================================
// Snake-draft order generation
// ============================================================================

// snakeDirAscending / snakeDirDescending — the two strings emitted by
// SnakeDirection and surfaced in the LLM's draft prompt. Constants
// rather than literals so a typo is a compile error.
const (
	snakeDirAscending  = "ascending"
	snakeDirDescending = "descending"
)

// SnakeDraftOrder returns the agent_id at each pick in a snake draft.
//
// agentOrder is the round-1 ordering — typically the result of
// `workflow.NewRandom(ctx).Shuffle` on the agent IDs (NOT math/rand,
// which would break Temporal replay determinism). The returned slice
// has length len(agentOrder) * rounds; index i corresponds to the
// (i+1)-th overall pick in the draft.
//
// Snake mechanics: round 1 ascends, round 2 descends, round 3 ascends,
// and so on, alternating. With 5 teams over 3 rounds and agentOrder
// [a, b, c, d, e], the result is:
//
//	[a, b, c, d, e,  e, d, c, b, a,  a, b, c, d, e]
//
// Empty agentOrder or rounds <= 0 returns nil.
func SnakeDraftOrder(agentOrder []int64, rounds int) []int64 {
	if len(agentOrder) == 0 || rounds <= 0 {
		return nil
	}
	out := make([]int64, 0, len(agentOrder)*rounds)
	for r := range rounds {
		if r%2 == 0 {
			out = append(out, agentOrder...)
			continue
		}
		// Append the reversed slice without mutating agentOrder.
		for i := len(agentOrder) - 1; i >= 0; i-- {
			out = append(out, agentOrder[i])
		}
	}
	return out
}

// SnakeDirection returns "ascending" or "descending" for a 1-indexed
// round. Used as the `snake_direction` field in the draft-prompt
// context so the agent can predict its next pick number.
func SnakeDirection(round int) string {
	if round%2 == 1 {
		return snakeDirAscending
	}
	return snakeDirDescending
}

// ============================================================================
// Draft candidates — typed, prior-season-only stats
// ============================================================================

// SkaterDraftCandidate is one rankable skater for the draft. Stats
// must come from the PRIOR season's `player_season_totals` row
// (game_type = regular season). Using current-season stats would leak
// future information into the draft and invalidate the simulation as a
// model-vs-model comparison. PLAN.md > "Draft Fallback".
type SkaterDraftCandidate struct {
	PlayerID int64
	Name     string
	Position string // one of "C", "LW", "RW", "D" — the 5-position table excludes F
	PriorG   int
	PriorA   int
}

// Score returns the skater's ranking value: prior-season G + A.
// Other categories (PPP, SOG, +/-, PIM) are intentionally excluded —
// G + A is the well-understood headline draft metric and matches what
// PLAN.md > "Draft Fallback" calls for.
func (s SkaterDraftCandidate) Score() int { return s.PriorG + s.PriorA }

// GoalieDraftCandidate is one rankable goalie. Stats from prior season.
// Tie-breaks: lower GA wins (a stingy goalie outranks a wins-equal but
// leakier one).
type GoalieDraftCandidate struct {
	PlayerID int64
	Name     string
	Position string // always "G"
	PriorW   int
	PriorGA  int
}

// ============================================================================
// Ranking — descending by score, tie-broken by ID for determinism
// ============================================================================

// RankSkaters returns candidates sorted by Score() descending; ties
// broken by PlayerID ascending (stable across map iteration / Temporal
// retries). The input slice is NOT mutated — callers may reuse it.
func RankSkaters(candidates []SkaterDraftCandidate) []SkaterDraftCandidate {
	out := slices.Clone(candidates)
	slices.SortFunc(out, func(a, b SkaterDraftCandidate) int {
		if d := b.Score() - a.Score(); d != 0 {
			return d
		}
		return cmpInt64(a.PlayerID, b.PlayerID)
	})
	return out
}

// RankGoalies returns goalies sorted by W desc, then GA asc, then ID asc.
// Defensive copy of the input; same determinism contract as RankSkaters.
func RankGoalies(candidates []GoalieDraftCandidate) []GoalieDraftCandidate {
	out := slices.Clone(candidates)
	slices.SortFunc(out, func(a, b GoalieDraftCandidate) int {
		if d := b.PriorW - a.PriorW; d != 0 {
			return d
		}
		if d := a.PriorGA - b.PriorGA; d != 0 {
			return d
		}
		return cmpInt64(a.PlayerID, b.PlayerID)
	})
	return out
}

// cmpInt64 returns -1/0/1 — int64 differences would overflow in
// large-ID corner cases, so don't use `int(a - b)`.
func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// ============================================================================
// Available-by-position selection
// ============================================================================

// MaxAvailablePerPosition / MaxBestAvailableOverall are the V1 sizes
// of the corresponding sub-blocks in the draft prompt. PLAN.md >
// "Draft Context" implies "top 10 per unfilled position + top 5 BPA";
// pinning here as named constants so a future tuning is explicit.
const (
	MaxAvailablePerPosition = 10
	MaxBestAvailableOverall = 5
)

// SelectAvailableByPosition produces the two structures the draft
// prompt needs:
//
//   - byPosition: maps "C"/"LW"/"RW"/"D"/"G" to the top
//     MaxAvailablePerPosition candidates of that position, excluding
//     anyone in `taken`.
//   - bestAvailable: top MaxBestAvailableOverall SKATERS by score —
//     V1 simplification, since the skater G+A axis isn't directly
//     comparable to goalie W. Goalies are still reachable via
//     byPosition["G"].
//
// The skater/goalie inputs MUST already be ranked (see RankSkaters /
// RankGoalies) — this function is a filter+truncate pass, not a
// re-sort.
//
// Both outputs return DraftablePlayer (the prompts.go shape) so the
// caller can drop them straight into DraftPromptInput.
func SelectAvailableByPosition(
	rankedSkaters []SkaterDraftCandidate,
	rankedGoalies []GoalieDraftCandidate,
	taken map[int64]struct{},
) (byPosition map[string][]DraftablePlayer, bestAvailable []DraftablePlayer) {

	byPosition = map[string][]DraftablePlayer{
		"C": {}, "LW": {}, "RW": {}, "D": {}, "G": {},
	}

	for _, s := range rankedSkaters {
		if _, t := taken[s.PlayerID]; t {
			continue
		}
		bucket, ok := byPosition[s.Position]
		if !ok {
			continue // unrecognized position — skip silently rather than panic
		}
		if len(bucket) >= MaxAvailablePerPosition {
			continue
		}
		byPosition[s.Position] = append(bucket, DraftablePlayer{
			Player:     s.Name,
			ID:         s.PlayerID,
			Position:   s.Position,
			LastSeason: SkaterStats{G: s.PriorG, A: s.PriorA},
		})
	}

	for _, g := range rankedGoalies {
		if _, t := taken[g.PlayerID]; t {
			continue
		}
		if len(byPosition["G"]) >= MaxAvailablePerPosition {
			continue
		}
		byPosition["G"] = append(byPosition["G"], DraftablePlayer{
			Player:     g.Name,
			ID:         g.PlayerID,
			Position:   "G",
			LastSeason: GoalieStats{W: g.PriorW, GA: g.PriorGA},
		})
	}

	for _, s := range rankedSkaters {
		if _, t := taken[s.PlayerID]; t {
			continue
		}
		if len(bestAvailable) >= MaxBestAvailableOverall {
			break
		}
		bestAvailable = append(bestAvailable, DraftablePlayer{
			Player:     s.Name,
			ID:         s.PlayerID,
			Position:   s.Position,
			LastSeason: SkaterStats{G: s.PriorG, A: s.PriorA},
		})
	}

	return byPosition, bestAvailable
}

// ============================================================================
// Fallback picker — used when the LLM has failed twice on a draft turn
// ============================================================================

// activeSlotLimits is the per-PLAN.md V1 active slot composition
// (excluding BN/IR). Fallback uses these to compute positional need.
// Util is excluded — it's a flexible slot, not tied to a position.
var activeSlotLimits = map[string]int{
	"C":  2,
	"LW": 2,
	"RW": 2,
	"D":  3,
	"G":  2,
}

// FallbackDraftPick returns a deterministic player_id when the LLM has
// failed and the activity needs to advance the draft. Strategy
// (PLAN.md > "Draft Fallback"):
//
//  1. Determine the agent's most-needed position — the position with
//     the largest deficit (limit minus filled). Ties broken by a fixed
//     position ordering: C, LW, RW, D, G (so retries pick the same
//     position).
//  2. Pick the highest-ranked available candidate at that position.
//  3. If the most-needed position has no available candidate, fall
//     back to the next-most-needed; if all positions are exhausted,
//     return 0 (caller treats as "draft cannot proceed").
//
// catalog gives NHL position for already-rostered players so we can
// count positional fill correctly (a player in Util counts toward
// their NHL position's total). A catalog lookup error short-circuits
// to (0, err).
func FallbackDraftPick(
	roster RosterState,
	catalog PlayerCatalog,
	rankedSkaters []SkaterDraftCandidate,
	rankedGoalies []GoalieDraftCandidate,
	taken map[int64]struct{},
) (int64, error) {

	filled, err := positionFillCounts(roster, catalog)
	if err != nil {
		return 0, err
	}

	// Build a deficit-ordered list of positions. Stable order across
	// retries — fixed position ordering is the tiebreaker.
	positions := []string{"C", "LW", "RW", "D", "G"}
	slices.SortStableFunc(positions, func(a, b string) int {
		da := activeSlotLimits[a] - filled[a]
		db := activeSlotLimits[b] - filled[b]
		return db - da // larger deficit first
	})

	for _, pos := range positions {
		// Negative or zero deficit means already at capacity; skip.
		if activeSlotLimits[pos]-filled[pos] <= 0 {
			continue
		}

		if pos == "G" {
			for _, g := range rankedGoalies {
				if _, t := taken[g.PlayerID]; t {
					continue
				}
				return g.PlayerID, nil
			}
			continue
		}

		for _, s := range rankedSkaters {
			if s.Position != pos {
				continue
			}
			if _, t := taken[s.PlayerID]; t {
				continue
			}
			return s.PlayerID, nil
		}
	}

	// Every position is at capacity OR has no available candidates.
	// As a final safety net, pick the BPA across all skaters/goalies.
	for _, s := range rankedSkaters {
		if _, t := taken[s.PlayerID]; !t {
			return s.PlayerID, nil
		}
	}
	for _, g := range rankedGoalies {
		if _, t := taken[g.PlayerID]; !t {
			return g.PlayerID, nil
		}
	}
	return 0, nil
}

// positionFillCounts counts how many players on the roster fill each
// position based on their NHL position (NOT their current slot). A
// goalie in BN still counts toward "G filled"; a C in Util still
// counts toward "C filled".
func positionFillCounts(roster RosterState, catalog PlayerCatalog) (map[string]int, error) {
	filled := map[string]int{"C": 0, "LW": 0, "RW": 0, "D": 0, "G": 0}
	for playerID := range roster.Placements {
		pos, err := catalog.Position(playerID)
		if err != nil {
			return nil, err
		}
		// Translate the sqlc enum to the prompt-side string. Anything
		// outside the 5-position table is silently ignored — this is
		// V1 and EligibleSlots already rejects F/unknown loudly at
		// validation time.
		switch pos {
		case sqlcdb.PlayerPositionC:
			filled["C"]++
		case sqlcdb.PlayerPositionLW:
			filled["LW"]++
		case sqlcdb.PlayerPositionRW:
			filled["RW"]++
		case sqlcdb.PlayerPositionD:
			filled["D"]++
		case sqlcdb.PlayerPositionG:
			filled["G"]++
		}
	}
	return filled, nil
}
