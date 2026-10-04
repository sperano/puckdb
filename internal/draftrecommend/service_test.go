package draftrecommend

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/draftsession"
)

const testTimeLayout = "2006-01-02T15:04:05Z"

func TestEvaluate_ExcludesDraftedKeeperUnavailableAndRanksFit(t *testing.T) {
	in := recommendationInput(t)
	in.Scenario = draftrank.ScenarioBaseline
	in.Availability = AvailabilitySource{Name: "board sync", Version: "v2", AsOf: time.Now().UTC(),
		UnavailablePlayerIDs: map[int]string{106: "confirmed injured reserve"}}
	in.ADP = ADPSource{Name: "League ADP export", Version: "2026-10-01", AsOf: time.Now().UTC(),
		ByPlayer: map[string]float64{"wing": 1.2}}
	result, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionVersion != 0 {
		t.Fatalf("session version = %d, want valid zero version", result.SessionVersion)
	}
	if result.CurrentPick == nil || result.CurrentPick.Key != (draftsession.PickKey{Round: 1, Pick: 2}) {
		t.Fatalf("current pick = %+v", result.CurrentPick)
	}
	if result.PicksUntilNextTurn == nil || *result.PicksUntilNextTurn != 0 {
		t.Fatalf("picks until = %v", result.PicksUntilNextTurn)
	}
	if result.BestValue == nil || result.BestValue.PlayerKey != "center" {
		t.Fatalf("best value = %+v", result.BestValue)
	}
	if result.BestRosterFit == nil || result.BestRosterFit.PlayerKey != "wing" {
		t.Fatalf("best fit = %+v", result.BestRosterFit)
	}
	if result.BestRosterFit.Reasons.AssignedSlot != draft.PositionLeftWing {
		t.Fatalf("fit slot = %q", result.BestRosterFit.Reasons.AssignedSlot)
	}
	if result.BestRosterFit.WaitRisk == nil || result.BestRosterFit.WaitRisk.Source != in.ADP.Name {
		t.Fatalf("ADP wait risk = %+v", result.BestRosterFit.WaitRisk)
	}
	for _, candidate := range result.Candidates {
		if candidate.PlayerKey == "drafted" || candidate.PlayerKey == "keeper" || candidate.PlayerKey == "unavailable" {
			t.Fatalf("unavailable player recommended: %s", candidate.PlayerKey)
		}
	}
}

func TestEvaluate_UsesFullEligibilityForReserveAndFailsClosed(t *testing.T) {
	in := recommendationInput(t)
	in.Ranking.League.RosterSlots = []draft.RosterSlot{
		{Position: draft.PositionCenter, Count: 1, Starting: true},
		{Position: draft.SlotInjuredReserve, Count: 1},
	}
	in.Roster = []draft.RosterPlayer{{YahooPlayerID: 101, Name: "keeper", EligiblePositions: []string{draft.PositionCenter}}}
	in.Ranking.Players = []draftrank.Player{
		player("eligible-ir", 108, []string{draft.PositionCenter, draft.SlotInjuredReserve}, 0.8, 1),
		player("no-ir", 109, []string{draft.PositionCenter}, 0.9, 1),
	}
	in.Ranking.Meta.PoolSize = len(in.Ranking.Players)
	result, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].PlayerKey != "eligible-ir" {
		t.Fatalf("feasible candidates = %+v", result.Candidates)
	}
	in.Roster[0].EligiblePositions = nil
	if _, err := Evaluate(in); err == nil {
		t.Fatal("expected unresolved roster error")
	}
}

func TestEvaluate_RejectsDuplicateRosterPlayer(t *testing.T) {
	in := recommendationInput(t)
	in.Roster = append(in.Roster, in.Roster[0])

	if _, err := Evaluate(in); err == nil {
		t.Fatal("expected duplicate roster player error")
	}
}

func TestEvaluate_TurnOrderSkipsFilledKeeperAndUsesTradedOwner(t *testing.T) {
	in := recommendationInput(t)
	keeperKey := draftsession.PickKey{Round: 1, Pick: 2}
	in.Session.Upstream[keeperKey] = draftsession.Pick{
		Key: keeperKey, TeamID: in.OurTeamID, PlayerID: in.Roster[0].YahooPlayerID,
	}
	in.KeeperPlayerKeys = nil
	in.ADP = ADPSource{
		Name: "League ADP export", Version: "2026-10-01", AsOf: in.GeneratedAt,
		ByPlayer: map[string]float64{"wing": 3.5},
	}

	result, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if result.CurrentPick == nil || result.CurrentPick.Key != (draftsession.PickKey{Round: 1, Pick: 3}) {
		t.Fatalf("current pick = %+v", result.CurrentPick)
	}
	if result.PicksUntilNextTurn == nil || *result.PicksUntilNextTurn != 1 {
		t.Fatalf("picks until next unfilled owned slot = %v", result.PicksUntilNextTurn)
	}
	for _, candidate := range result.Candidates {
		if candidate.PlayerKey == "keeper" {
			t.Fatal("rostered keeper was recommended without a keeper-key hint")
		}
		if candidate.PlayerKey == "wing" && (candidate.WaitRisk == nil || candidate.WaitRisk.NextPickNumber != 4) {
			t.Fatalf("wing wait risk = %+v", candidate.WaitRisk)
		}
	}
}

func TestEvaluate_UsesNewsScenarioAndStrategyWeights(t *testing.T) {
	in := recommendationInput(t)
	in.Scenario = draftrank.ScenarioBaseline
	baseline, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Scenario = draftrank.ScenarioBase
	unweighted, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Strategy.CategoryWeights = map[int]float64{1: 3}
	centerPlacement := in.Ranking.Players[2].Placements[draftrank.ScenarioBase]
	centerPlacement.Contributions[0].Adjusted = 1.0
	in.Ranking.Players[2].Placements[draftrank.ScenarioBase] = centerPlacement
	wingPlacement := in.Ranking.Players[3].Placements[draftrank.ScenarioBase]
	wingPlacement.Contributions[0].Adjusted = 0.1
	in.Ranking.Players[3].Placements[draftrank.ScenarioBase] = wingPlacement
	news, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	center := candidateByKey(news.Candidates, "center")
	if center.Reasons.NewsDelta >= 0 {
		t.Fatalf("news delta = %.3f, expected negative", center.Reasons.NewsDelta)
	}
	if news.Scenario == baseline.Scenario {
		t.Fatal("scenario change was not recorded")
	}
	if unweighted.BestValue == nil || unweighted.BestValue.PlayerKey != "wing" {
		t.Fatalf("news scenario did not change league value ordering: %+v", unweighted.BestValue)
	}
	if news.Candidates[0].PlayerKey != "center" {
		t.Fatalf("strategy did not change ordering: %+v", news.Candidates)
	}
}

func TestEvaluate_FixturesDifferByLeagueValueAndRosterBuild(t *testing.T) {
	points := recommendationInput(t)
	points.Scenario = draftrank.ScenarioBaseline
	points.Ranking.League.ScoringType = "points"
	pointsResult, err := Evaluate(points)
	if err != nil {
		t.Fatal(err)
	}
	if pointsResult.BestValue == nil || pointsResult.BestValue.PlayerKey != "center" ||
		pointsResult.BestRosterFit == nil || pointsResult.BestRosterFit.PlayerKey != "wing" {
		t.Fatalf("points fixture recommendations = value %+v, fit %+v", pointsResult.BestValue, pointsResult.BestRosterFit)
	}

	category := recommendationInput(t)
	category.Scenario = draftrank.ScenarioBaseline
	category.Ranking.League.ScoringType = "head"
	category.Ranking.League.RosterSlots = []draft.RosterSlot{
		{Position: draft.PositionCenter, Count: 2, Starting: true},
		{Position: draft.SlotBench, Count: 1},
	}
	center := category.Ranking.Players[2].Placements[draftrank.ScenarioBaseline]
	center.AdjustedValue = 0.8
	category.Ranking.Players[2].Placements[draftrank.ScenarioBaseline] = center
	wing := category.Ranking.Players[3].Placements[draftrank.ScenarioBaseline]
	wing.AdjustedValue = 0.95
	category.Ranking.Players[3].Placements[draftrank.ScenarioBaseline] = wing
	categoryResult, err := Evaluate(category)
	if err != nil {
		t.Fatal(err)
	}
	if categoryResult.BestValue == nil || categoryResult.BestValue.PlayerKey != "wing" ||
		categoryResult.BestRosterFit == nil || categoryResult.BestRosterFit.PlayerKey != "center" {
		t.Fatalf("category fixture recommendations = value %+v, fit %+v", categoryResult.BestValue, categoryResult.BestRosterFit)
	}
}

func TestEvaluate_LabelsStaleIncompleteAndMissingOrder(t *testing.T) {
	in := recommendationInput(t)
	in.SessionStale = true
	stale, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if stale.BoardStatus != BoardStatusStale || len(stale.Candidates) != 0 {
		t.Fatalf("stale response = %+v", stale)
	}
	in.SessionStale = false
	in.SessionSafe = false
	in.Order = nil
	in.RankingIssues = []draftrank.Issue{{Code: draftrank.IssueStalePool, Message: "old pool"}}
	in.Ranking.Meta.PoolSize++
	partial, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if partial.BoardStatus != BoardStatusIncomplete || len(partial.Candidates) != 0 {
		t.Fatalf("partial response = %+v", partial)
	}
	if len(partial.Issues) < 2 {
		t.Fatalf("missing issue labels: %+v", partial.Issues)
	}
}

func TestEvaluate_SuppressesIncompleteRankingButKeepsDeterministicFallback(t *testing.T) {
	incomplete := recommendationInput(t)
	incomplete.Ranking.Meta.PoolSize++
	result, err := Evaluate(incomplete)
	if err != nil {
		t.Fatal(err)
	}
	if result.BoardStatus != BoardStatusIncomplete || len(result.Candidates) != 0 {
		t.Fatalf("incomplete ranking result = %+v", result)
	}

	fallback := recommendationInput(t)
	fallback.Ranking.Meta.Unavailable = []draftrank.Issue{{
		Code: draftrank.IssueNewsAdjustmentsDown, Message: "news service unavailable",
	}}
	result, err = Evaluate(fallback)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) == 0 {
		t.Fatalf("deterministic fallback was suppressed: %+v", result.Issues)
	}
}

func TestEvaluate_RejectsMismatchedSessionLeague(t *testing.T) {
	in := recommendationInput(t)
	in.SessionLeagueKey = "nhl.l.other"

	if _, err := Evaluate(in); err == nil {
		t.Fatal("expected session/ranking league mismatch")
	}
}

func TestEvaluate_IsStableAndUpdatesAfterBoardCorrection(t *testing.T) {
	in := recommendationInput(t)
	in.Scenario = draftrank.ScenarioBaseline
	first, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Candidates, second.Candidates) {
		t.Fatalf("fixed snapshot changed candidates:\nfirst=%+v\nsecond=%+v", first.Candidates, second.Candidates)
	}

	key := draftsession.PickKey{Round: 1, Pick: 1}
	corrected := in.Session.Upstream[key]
	corrected.PlayerID = 102
	in.Session.Upstream[key] = corrected
	in.Session.Version++
	updated, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if candidateByKey(updated.Candidates, "center").PlayerKey != "" {
		t.Fatal("corrected drafted player remained recommended")
	}
	if updated.SessionVersion != in.Session.Version {
		t.Fatalf("session version = %d, want %d", updated.SessionVersion, in.Session.Version)
	}
}

func recommendationInput(t *testing.T) Input {
	t.Helper()
	now, err := time.Parse(testTimeLayout, "2026-10-04T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	players := []draftrank.Player{
		player("drafted", 100, []string{draft.PositionCenter}, 1.0, 1),
		player("keeper", 101, []string{draft.PositionCenter}, 0.5, 1),
		player("center", 102, []string{draft.PositionCenter}, 0.9, 2),
		player("wing", 103, []string{draft.PositionLeftWing}, 0.7, 1),
		player("flex", 104, []string{draft.PositionCenter, draft.PositionLeftWing}, 0.6, 2),
		player("unavailable", 106, []string{draft.PositionLeftWing}, 0.4, 2),
	}
	players[2].Placements[draftrank.ScenarioBase] = placement(0.3, 2)
	players[3].Placements[draftrank.ScenarioBase] = placement(0.8, 1)
	players[4].Placements[draftrank.ScenarioBase] = placement(0.6, 2)
	state := draftsession.State{
		Upstream: map[draftsession.PickKey]draftsession.Pick{
			{Round: 1, Pick: 1}: {Key: draftsession.PickKey{Round: 1, Pick: 1}, TeamID: 2, PlayerID: 100},
		},
		Manual: map[draftsession.PickKey]draftsession.ManualChange{}, UpstreamComplete: true,
	}
	return Input{
		Session: state, SessionLeagueKey: "nhl.l.1", SessionSafe: true, OurTeamID: 1,
		Roster:           []draft.RosterPlayer{{YahooPlayerID: 101, Name: "keeper", EligiblePositions: []string{draft.PositionCenter}}},
		KeeperPlayerKeys: map[string]bool{"keeper": true},
		Ranking: &draftrank.Snapshot{SnapshotInfo: draftrank.SnapshotInfo{
			ID: uuid.New(), Identity: "rank-identity", AsOf: now,
			Meta: draftrank.Meta{League: draftrank.League{
				LeagueKey: "nhl.l.1", RulesHash: "rules-v1", RosterSlots: []draft.RosterSlot{
					{Position: draft.PositionCenter, Count: 1, Starting: true},
					{Position: draft.PositionLeftWing, Count: 1, Starting: true},
					{Position: draft.SlotBench, Count: 1},
				}, Categories: []draftrank.Category{{StatID: 1, Abbr: "SOG", Name: "Shots"}},
			}, PoolSize: len(players), Projection: draftrank.ProjectionInfo{SnapshotID: uuid.New(), ModelVersion: "projection-v1"},
				Versions: map[draftrank.Scenario]string{
					draftrank.ScenarioBaseline: "ranking-baseline-v1", draftrank.ScenarioBase: "ranking-base-v1",
				},
				Scenarios: []draftrank.Scenario{draftrank.ScenarioBaseline, draftrank.ScenarioBase},
			}}, Players: players},
		Order: []PickSlot{
			{Key: draftsession.PickKey{Round: 1, Pick: 1}, TeamID: 2},
			{Key: draftsession.PickKey{Round: 1, Pick: 2}, TeamID: 1},
			{Key: draftsession.PickKey{Round: 1, Pick: 3}, TeamID: 2},
			{Key: draftsession.PickKey{Round: 1, Pick: 4}, TeamID: 1},
		},
		GeneratedAt: now,
	}
}

func player(key string, id int, eligibility []string, value float64, rank int) draftrank.Player {
	return draftrank.Player{
		PlayerKey: key, YahooPlayerID: id, Name: key,
		EligiblePositions: baseEligibility(eligibility), RosterEligiblePositions: eligibility,
		Placements: map[draftrank.Scenario]draftrank.Placement{
			draftrank.ScenarioBaseline: placement(value, rank),
			draftrank.ScenarioBase:     placement(value, rank),
		},
	}
}

func baseEligibility(eligibility []string) []string {
	var base []string
	for _, position := range eligibility {
		switch position {
		case draft.PositionCenter, draft.PositionLeftWing, draft.PositionRightWing, draft.PositionDefense, draft.PositionGoalie:
			base = append(base, position)
		}
	}
	return base
}

func placement(value float64, rank int) draftrank.Placement {
	return draftrank.Placement{
		AdjustedValue: value, Value: value, Uncertainty: 0.1, Tier: rank,
		PositionRanks: map[string]int{draft.PositionCenter: rank, draft.PositionLeftWing: rank},
		Contributions: []draftrank.Contribution{{StatID: 1, Abbr: "SOG", Adjusted: value}},
	}
}

func candidateByKey(candidates []Candidate, key string) Candidate {
	for _, candidate := range candidates {
		if candidate.PlayerKey == key {
			return candidate
		}
	}
	return Candidate{}
}
