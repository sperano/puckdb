package draftrecommend

import (
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/draftsession"
)

func makeCandidate(in Input, ref playerRef, slot string, refs []playerRef, startsAdded int) Candidate {
	baseline, hasBaseline := ref.player.Placements[draftrank.ScenarioBaseline]
	newsDelta := 0.0
	if hasBaseline {
		newsDelta = ref.place.AdjustedValue - baseline.AdjustedValue
	}
	value := strategyValue(ref.place, in.Strategy)
	uncertainty := ref.place.Uncertainty
	if ref.player.Adjustment != nil {
		uncertainty = math.Max(uncertainty, ref.player.Adjustment.Uncertainty)
	}
	riskPenalty := 0.0
	if in.Strategy.RiskToleranceSet {
		riskPenalty = 1 - in.Strategy.RiskTolerance
	}
	riskAdjustment := uncertainty * riskPenalty * math.Abs(value)
	reasons := NumericReasons{
		LeagueValue: ref.place.AdjustedValue, StrategyValue: value, RiskAdjustment: riskAdjustment,
		NewsDelta: newsDelta, MarginalValue: value - riskAdjustment, AssignedSlot: slot,
		AdditionalStarts:   startsAdded,
		PositionRank:       positionRank(ref.place, ref.player.EligiblePositions, slot),
		Tier:               ref.place.Tier,
		CategoryStrengths:  categoryReasons(ref.place.Contributions, true),
		CategoryWeaknesses: categoryReasons(ref.place.Contributions, false),
	}
	reasons.ScarcityCount = countEligible(refs, slot)
	reasons.NextPositionRank, reasons.NextTier = nextPositionPlacement(ref, refs, slot)
	if reasons.Tier > 0 && reasons.NextTier > reasons.Tier {
		reasons.TierDrop = reasons.NextTier - reasons.Tier
	}
	if slotPriority(slot) > 0 {
		reasons.Tradeoffs = append(reasons.Tradeoffs, "addition fills a bench or reserve slot rather than a starting slot")
	}
	if newsDelta < 0 {
		reasons.Tradeoffs = append(reasons.Tradeoffs, "news scenario lowers projected value")
	}
	candidate := Candidate{PlayerKey: ref.player.PlayerKey, Name: ref.player.Name,
		Eligible: slices.Clone(ref.player.RosterEligiblePositions), NewsRisk: newsRisk(ref.player, ref.place), Reasons: reasons}
	candidate.Explanations = explanations(candidate)
	return candidate
}

func strategyValue(place draftrank.Placement, strategy Strategy) float64 {
	value := place.AdjustedValue
	for _, contribution := range place.Contributions {
		weight, specified := strategy.CategoryWeights[contribution.StatID]
		if specified {
			value += contribution.Adjusted * (weight - 1)
		}
	}
	return value
}

func bestPositionRank(place draftrank.Placement, eligible []string) int {
	best := 0
	for _, position := range eligible {
		if rank := place.PositionRanks[position]; rank > 0 && (best == 0 || rank < best) {
			best = rank
		}
	}
	return best
}

func positionRank(place draftrank.Placement, eligible []string, slot string) int {
	if rank := place.PositionRanks[slot]; rank > 0 {
		return rank
	}
	return bestPositionRank(place, eligible)
}

func nextPositionPlacement(ref playerRef, refs []playerRef, slot string) (int, int) {
	bestRank, nextRank, nextTier := positionRank(ref.place, ref.player.EligiblePositions, slot), 0, 0
	for _, other := range refs {
		if other.player.PlayerKey == ref.player.PlayerKey || !draft.SlotAccepts(slot, other.player.RosterEligiblePositions) {
			continue
		}
		rank := positionRank(other.place, other.player.EligiblePositions, slot)
		if rank > bestRank && (nextRank == 0 || rank < nextRank) {
			nextRank, nextTier = rank, other.place.Tier
		}
	}
	return nextRank, nextTier
}

func countEligible(refs []playerRef, slot string) int {
	count := 0
	for _, ref := range refs {
		if draft.SlotAccepts(slot, ref.player.RosterEligiblePositions) {
			count++
		}
	}
	return count
}

func categoryReasons(contributions []draftrank.Contribution, strongest bool) []CategoryReason {
	rows := make([]CategoryReason, 0, len(contributions))
	for _, contribution := range contributions {
		rows = append(rows, CategoryReason{StatID: contribution.StatID, Name: contribution.Abbr, Value: contribution.Adjusted})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Value == rows[j].Value {
			return rows[i].StatID < rows[j].StatID
		}
		if strongest {
			return rows[i].Value > rows[j].Value
		}
		return rows[i].Value < rows[j].Value
	})
	if len(rows) > maxCategoryReasons {
		rows = rows[:maxCategoryReasons]
	}
	return rows
}

func newsRisk(player draftrank.Player, selected draftrank.Placement) []string {
	var risk []string
	if player.Adjustment != nil {
		risk = append(risk, player.Adjustment.Alerts...)
		risk = append(risk, player.Adjustment.Assumptions...)
		for _, reason := range player.Adjustment.Reasons {
			risk = append(risk, string(reason.Type)+": "+reason.Detail)
		}
	}
	if selected.Uncertainty > 0 {
		risk = append(risk, fmt.Sprintf("projection uncertainty %.3f", selected.Uncertainty))
	}
	slices.Sort(risk)
	return slices.Compact(risk)
}

func waitRisk(in Input, key string, result *Result) *WaitRisk {
	if result.PicksUntilNextTurn == nil || in.ADP.Name == "" || in.ADP.Version == "" || in.ADP.AsOf.IsZero() {
		return nil
	}
	adp, exists := in.ADP.ByPlayer[key]
	if !exists || !finite(adp) || adp <= 0 || result.CurrentPick == nil {
		return nil
	}
	nextPick := 0
	filled := make(map[draftsession.PickKey]bool)
	for _, entry := range draftsession.EffectiveBoard(in.Session) {
		filled[entry.Pick.Key] = true
	}
	seenCurrent := false
	for index, slot := range in.Order {
		if slot.Key == result.CurrentPick.Key {
			seenCurrent = true
			continue
		}
		if seenCurrent && !filled[slot.Key] && slot.TeamID == in.OurTeamID {
			nextPick = index + 1
			break
		}
	}
	if nextPick <= 0 {
		return nil
	}
	kind := "ADP estimate suggests the player may last; availability is uncertain"
	if adp <= float64(nextPick) {
		kind = "ADP estimate suggests the player may go before our next turn; availability is uncertain"
	}
	return &WaitRisk{Kind: kind, Source: in.ADP.Name,
		SourceVersion: in.ADP.Version, SourceAsOf: in.ADP.AsOf.UTC(), PlayerADP: adp, NextPickNumber: nextPick}
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
