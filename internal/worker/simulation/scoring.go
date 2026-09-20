package simulation

// scoring.go: Yahoo-style rotisserie scoring engine for the sim pool.
// The reference algorithm is documented in PLAN.md "Scoring: Rotisserie".

import (
	"cmp"
	"math"
	"slices"
)

// Category identifies one of the 9 Crapettes 2025 roto categories.
// The string values match the PoolConfig.Categories entries.
type Category string

const (
	CategoryG   Category = "G"
	CategoryA   Category = "A"
	CategoryPM  Category = "+/-"
	CategoryPIM Category = "PIM"
	CategoryPPP Category = "PPP"
	CategorySOG Category = "SOG"
	CategoryW   Category = "W"
	CategoryGA  Category = "GA"
	CategoryGAA Category = "GAA"
)

// IsLowerBetter reports whether smaller values rank higher in c.
// Only GA and GAA are lower-is-better; the seven counting categories
// are higher-is-better.
func (c Category) IsLowerBetter() bool {
	return c == CategoryGA || c == CategoryGAA
}

// AgentCategoryStat carries one agent's stat row for a single category.
//
// For non-GAA categories only AgentID and Value are read.
// For GAA, RankGAA derives the displayed value from GoalieGA / GoalieTOISeconds;
// an agent with GoalieTOISeconds == 0 receives the worst rank in that category
// per PLAN.md "Rate stats" (no goalie minutes ever accrued, so GAA is undefined).
type AgentCategoryStat struct {
	AgentID          int64
	Value            float64
	GoalieGA         int
	GoalieTOISeconds int
}

// CategoryRanking is the per-agent ranking output for one category.
//
// Rank is the (1-indexed) sorted position; tied agents share the lowest
// position in their tie group (competition ranking, e.g. "1, 2, 2, 4").
// RotoPoints is fractional under ties (e.g. 2.5 for a 2-way tie at 3rd
// in a 5-team pool).
type CategoryRanking struct {
	AgentID    int64
	Value      float64
	Rank       int
	RotoPoints float64
}

// AgentRotoTotal is one agent's roto total summed across every category.
type AgentRotoTotal struct {
	AgentID         int64
	TotalRotoPoints float64
}

// CategoryStats bundles the per-agent stat rows for one category, used as
// input to RankAllCategories.
type CategoryStats struct {
	Category Category
	Stats    []AgentCategoryStat
}

// RankCategory ranks agents for one counting category and applies the Yahoo
// tie-split rule: a run of K agents tied at sorted positions P..P+K-1 each
// receive N + 1 - P - (K-1)/2 points (the average of the K positional point
// values they would have earned individually).
//
// Sort direction: descending when lowerIsBetter is false; ascending otherwise.
// GAA must use RankGAA — it handles the zero-TOI worst-rank rule that pure
// value-sorting cannot express.
//
// The returned slice is in input order, one entry per input row.
func RankCategory(stats []AgentCategoryStat, lowerIsBetter bool) []CategoryRanking {
	n := len(stats)
	out := make([]CategoryRanking, n)
	if n == 0 {
		return out
	}

	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	// order elements are indices into stats; SortStableFunc passes the
	// elements themselves, so index stats directly. Stable sort keeps
	// equal-value rows in input order for reproducible run detection.
	slices.SortStableFunc(order, func(a, b int) int {
		if lowerIsBetter {
			return cmp.Compare(stats[a].Value, stats[b].Value)
		}
		return cmp.Compare(stats[b].Value, stats[a].Value)
	})

	// Walk runs of equal value, awarding shared rank + averaged points.
	p := 0
	for p < n {
		q := p
		for q < n && stats[order[q]].Value == stats[order[p]].Value {
			q++
		}
		k := q - p
		firstPos := p + 1 // 1-indexed sorted position of the run's first agent
		// avg over k consecutive positions starting at firstPos:
		//   N+1-firstPos - (k-1)/2
		avgPoints := float64(n+1-firstPos) - float64(k-1)/2.0
		for r := p; r < q; r++ {
			idx := order[r]
			out[idx] = CategoryRanking{
				AgentID:    stats[idx].AgentID,
				Value:      stats[idx].Value,
				Rank:       firstPos,
				RotoPoints: avgPoints,
			}
		}
		p = q
	}
	return out
}

// RankGAA ranks agents in the GAA category. The displayed GAA is derived
// from cumulative components: GAA = (GoalieGA / GoalieTOISeconds) * 3600.
//
// Agents with GoalieTOISeconds == 0 are forced to the worst sorted position
// regardless of value (PLAN.md "Rate stats"); their reported Value is 0
// since no goalie minutes accrued. Multiple zero-TOI agents tie at the
// bottom and split the lowest positions per the standard tie-split rule.
//
// The returned slice is in input order.
func RankGAA(stats []AgentCategoryStat) []CategoryRanking {
	n := len(stats)
	if n == 0 {
		return []CategoryRanking{}
	}
	derived := make([]AgentCategoryStat, n)
	for i, s := range stats {
		derived[i].AgentID = s.AgentID
		if s.GoalieTOISeconds == 0 {
			// Sentinel: +Inf sorts last under ascending order regardless
			// of the magnitude of any positive GAA from other agents.
			// Replaced with 0 in the output below.
			derived[i].Value = math.Inf(1)
		} else {
			derived[i].Value = float64(s.GoalieGA) / float64(s.GoalieTOISeconds) * 3600.0
		}
	}
	rankings := RankCategory(derived, true)
	for i := range rankings {
		if math.IsInf(rankings[i].Value, 1) {
			rankings[i].Value = 0
		}
	}
	return rankings
}

// RankAllCategories ranks every category in cats. GAA is dispatched to
// RankGAA; every other category uses RankCategory with the sort direction
// from Category.IsLowerBetter.
func RankAllCategories(cats []CategoryStats) map[Category][]CategoryRanking {
	out := make(map[Category][]CategoryRanking, len(cats))
	for _, c := range cats {
		if c.Category == CategoryGAA {
			out[c.Category] = RankGAA(c.Stats)
		} else {
			out[c.Category] = RankCategory(c.Stats, c.Category.IsLowerBetter())
		}
	}
	return out
}

// AgentRotoTotals sums each agent's roto points across all category rankings,
// returning rows sorted by total descending (AgentID ascending breaks ties).
func AgentRotoTotals(rankings map[Category][]CategoryRanking) []AgentRotoTotal {
	totals := make(map[int64]float64)
	for _, rows := range rankings {
		for _, r := range rows {
			totals[r.AgentID] += r.RotoPoints
		}
	}
	out := make([]AgentRotoTotal, 0, len(totals))
	for id, pts := range totals {
		out = append(out, AgentRotoTotal{AgentID: id, TotalRotoPoints: pts})
	}
	slices.SortStableFunc(out, func(a, b AgentRotoTotal) int {
		if c := cmp.Compare(b.TotalRotoPoints, a.TotalRotoPoints); c != 0 {
			return c
		}
		return cmp.Compare(a.AgentID, b.AgentID)
	})
	return out
}
