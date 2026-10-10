package simulation

import (
	"slices"
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
	slices.SortStableFunc(out, func(a, b SkaterDraftCandidate) int {
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
	slices.SortStableFunc(out, func(a, b GoalieDraftCandidate) int {
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
		string(SlotC): {}, string(SlotLW): {}, string(SlotRW): {}, string(SlotD): {}, string(SlotG): {},
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
		if len(byPosition[string(SlotG)]) >= MaxAvailablePerPosition {
			continue
		}
		byPosition[string(SlotG)] = append(byPosition[string(SlotG)], DraftablePlayer{
			Player:     g.Name,
			ID:         g.PlayerID,
			Position:   string(SlotG),
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
// Positional need — shared by the draft prompt and the fallback picker
// ============================================================================

// computeSlotsRemaining returns "starter slots this agent still needs
// to fill" — PoolConfig.RosterPositions positional buckets minus what
// the agent has already drafted at each NHL position. Util/BN/IR are
// excluded since they're position-agnostic and don't represent a
// strategic gap during the draft.
//
// This is the single positional-need calculation of the draft: the
// workflow puts it in DraftPromptInput.SlotsRemaining, the LLM reads
// it there, and FallbackDraftPick consumes the same map when the LLM
// fails, so both see the pool's configured slots and the agent's real
// picks.
func computeSlotsRemaining(cfg PoolConfig, picks []int64, lookup map[int64]DraftablePlayer) map[string]int {
	out := map[string]int{}
	for slot, n := range cfg.RosterPositions {
		switch slot {
		case SlotC, SlotLW, SlotRW, SlotD, SlotG:
			out[string(slot)] = n
		}
	}
	for _, pid := range picks {
		dp, ok := lookup[pid]
		if !ok {
			continue
		}
		if out[dp.Position] > 0 {
			out[dp.Position]--
		}
	}
	return out
}

// ============================================================================
// Fallback picker — used when the LLM has failed twice on a draft turn
// ============================================================================

// fallbackPositionOrder is the fallback's tie-break between positions
// with the same remaining need, so retries pick the same position.
var fallbackPositionOrder = []string{string(SlotC), string(SlotLW), string(SlotRW), string(SlotD), string(SlotG)}

// FallbackDraftPick returns a deterministic player_id when the LLM has
// failed and the activity needs to advance the draft. Strategy
// (PLAN.md > "Draft Fallback"):
//
//  1. Determine the agent's most-needed position — the position with
//     the most starter slots still to fill. Ties broken by a fixed
//     position ordering: C, LW, RW, D, G (so retries pick the same
//     position).
//  2. Pick the highest-ranked available candidate at that position.
//  3. If the most-needed position has no available candidate, fall
//     back to the next-most-needed; once no position has need left
//     (or none has a candidate), pick the best available skater, then
//     goalie; with nothing available at all, return 0 (caller treats
//     as "draft cannot proceed").
//
// slotsRemaining is computeSlotsRemaining's output for the picking
// agent (DraftPromptInput.SlotsRemaining): the pool's configured
// starter slots minus the agent's picks so far. A position missing
// from the map has no need.
func FallbackDraftPick(
	slotsRemaining map[string]int,
	rankedSkaters []SkaterDraftCandidate,
	rankedGoalies []GoalieDraftCandidate,
	taken map[int64]struct{},
) int64 {
	// Build a need-ordered list of positions. Stable order across
	// retries — fixed position ordering is the tiebreaker.
	positions := slices.Clone(fallbackPositionOrder)
	slices.SortStableFunc(positions, func(a, b string) int {
		return slotsRemaining[b] - slotsRemaining[a] // larger need first
	})

	for _, pos := range positions {
		// Zero need means the position's starter slots are filled.
		if slotsRemaining[pos] <= 0 {
			continue
		}

		if pos == string(SlotG) {
			for _, g := range rankedGoalies {
				if _, t := taken[g.PlayerID]; t {
					continue
				}
				return g.PlayerID
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
			return s.PlayerID
		}
	}

	// Every position is at capacity OR has no available candidates.
	// As a final safety net, pick the BPA across all skaters/goalies.
	for _, s := range rankedSkaters {
		if _, t := taken[s.PlayerID]; !t {
			return s.PlayerID
		}
	}
	for _, g := range rankedGoalies {
		if _, t := taken[g.PlayerID]; !t {
			return g.PlayerID
		}
	}
	return 0
}
