package draft

import (
	"math"
	"strings"
	"testing"

	"github.com/sperano/puckdb/internal/projection"
)

const (
	rankingTestSeason     = 2026
	rankingTestNHLSeason  = 20262027
	rankingTestGoalStat   = 1
	rankingTestAssistStat = 2
	rankingTestWinStat    = 19
	rankingTestGAAStat    = 23
	rankingTestSaveStat   = 26
)

func testEstimate(value float64) projection.Estimate {
	return projection.Estimate{Mean: value, Low: value, High: value}
}

func TestBuildRanking_TargetLeagueFixturesUseOwnWeights(t *testing.T) {
	slots := []RosterSlot{{Position: PositionCenter, Count: 1, Starting: true}}
	players := []projection.PlayerProjection{
		testPlayer("goal-scorer", projection.PlayerKindSkater, 0, map[projection.Stat]float64{
			projection.StatGoals: 10, projection.StatAssists: 0,
		}),
		testPlayer("playmaker", projection.PlayerKindSkater, 0, map[projection.Stat]float64{
			projection.StatGoals: 0, projection.StatAssists: 10,
		}),
	}
	firstRules, projected, pool := testRankingInput("point", []StatCategory{
		testWeightedCategory(rankingTestGoalStat, 2),
		testWeightedCategory(rankingTestAssistStat, 1),
	}, slots, players...)
	firstRules.Rules.LeagueKey = "470.l.1001"
	first, err := BuildRanking(firstRules, projected, pool, RankingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secondRules := firstRules
	secondRules.Rules.LeagueKey = "470.l.1002"
	secondRules.Rules.Categories = []StatCategory{
		testWeightedCategory(rankingTestGoalStat, 1),
		testWeightedCategory(rankingTestAssistStat, 2),
	}
	second, err := BuildRanking(secondRules, projected, pool, RankingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Players[0].PlayerKey != "goal-scorer" || second.Players[0].PlayerKey != "playmaker" {
		t.Fatalf("league-specific weights did not change order: first=%+v second=%+v", first.Players, second.Players)
	}
}

func TestBuildRanking_ExplainsLinemateContextAdjustment(t *testing.T) {
	player := testPlayer("context-player", projection.PlayerKindSkater, 0, map[projection.Stat]float64{
		projection.StatGoals: 20,
	})
	player.LinemateContext = &projection.LinemateContext{
		ObservedPointsPer60: 3,
		AveragePointsPer60:  2,
		SharedTOISeconds:    12_000,
		AdjustmentFactor:    0.9,
	}
	rules, projected, pool := testRankingInput(
		"point",
		[]StatCategory{testWeightedCategory(rankingTestGoalStat, 1)},
		[]RosterSlot{{Position: PositionCenter, Count: 1, Starting: true}},
		player,
	)
	ranking, err := BuildRanking(rules, projected, pool, RankingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	explanation := strings.Join(ranking.Players[0].Explanations, "\n")
	for _, expected := range []string{"linemate quality 3.00", "position-group average 2.00", "adjusted down 10.0%"} {
		if !strings.Contains(explanation, expected) {
			t.Fatalf("linemate explanation missing %q: %s", expected, explanation)
		}
	}
}

func testPlayer(key string, kind projection.PlayerKind, uncertainty float64, values map[projection.Stat]float64) projection.PlayerProjection {
	projected := projection.PlayerProjection{
		PlayerKey: key, Kind: kind, Uncertainty: uncertainty,
		Values: make(map[projection.Stat]projection.Estimate, len(values)),
	}
	for stat, value := range values {
		projected.Values[stat] = testEstimate(value)
	}
	return projected
}

func testRankingInput(scoringType string, categories []StatCategory, slots []RosterSlot, players ...projection.PlayerProjection) (Snapshot, projection.Snapshot, []PoolPlayer) {
	rules := Snapshot{Rules: Rules{
		LeagueKey: "470.l.1001", Season: rankingTestSeason, ScoringType: scoringType,
		NumTeams: 1, Categories: categories, RosterSlots: slots,
	}, Source: SourceYahooAPI}
	projected := projection.Snapshot{
		TargetSeason: rankingTestNHLSeason, SourceDataHash: "fixture-source",
		Config: projection.Config{ModelVersion: projection.ModelVersion}, Players: players,
	}
	pool := make([]PoolPlayer, 0, len(players))
	for _, player := range players {
		position := PositionCenter
		if player.Kind == projection.PlayerKindGoalie {
			position = PositionGoalie
		}
		pool = append(pool, PoolPlayer{PlayerKey: player.PlayerKey, Name: player.PlayerKey, EligiblePositions: []string{position}})
	}
	return rules, projected, pool
}

func testCategory(id int, direction Direction) StatCategory {
	return StatCategory{StatID: id, Enabled: true, Direction: direction}
}

func testWeightedCategory(id int, weight float64) StatCategory {
	return StatCategory{StatID: id, Enabled: true, Weight: &weight}
}

func rankedByKey(t *testing.T, ranking RankingSnapshot, key string) RankedPlayer {
	t.Helper()
	for _, player := range ranking.Players {
		if player.PlayerKey == key {
			return player
		}
	}
	t.Fatalf("ranking missing %q", key)
	return RankedPlayer{}
}

func TestBuildRanking_LeagueWeightsAndFixedFilter(t *testing.T) {
	slots := []RosterSlot{{Position: PositionCenter, Count: 1, Starting: true}, {Position: PositionGoalie, Count: 1, Starting: true}}
	skater := testPlayer("skater", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 10})
	goalie := testPlayer("goalie", projection.PlayerKindGoalie, 0, map[projection.Stat]float64{projection.StatWins: 5})
	rules, projected, pool := testRankingInput("point", []StatCategory{
		testWeightedCategory(rankingTestGoalStat, 2), testWeightedCategory(rankingTestWinStat, 5),
	}, slots, skater, goalie)
	first, err := BuildRanking(rules, projected, pool, RankingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Players[0].PlayerKey != "goalie" || rankedByKey(t, first, "goalie").OfficialScore != 25 {
		t.Fatalf("unexpected points ranking: %+v", first.Players)
	}
	filtered := first.Filter(PositionCenter, PositionLeftWing, PositionCenter)
	if len(filtered) != 1 || filtered[0].OverallRank != rankedByKey(t, first, "skater").OverallRank || filtered[0].Value != rankedByKey(t, first, "skater").Value {
		t.Fatalf("filter changed fixed ranking: %+v", filtered)
	}
	rules.Rules.Categories = []StatCategory{testWeightedCategory(rankingTestGoalStat, 5), testWeightedCategory(rankingTestWinStat, 1)}
	second, err := BuildRanking(rules, projected, pool, RankingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Players[0].PlayerKey != "skater" || first.Version == second.Version {
		t.Fatalf("league weights did not change order and identity: %+v", second.Players)
	}
}

func TestBuildRanking_LowerIsBetterAndGoalieRatioExposure(t *testing.T) {
	slots := []RosterSlot{{Position: PositionGoalie, Count: 1, Starting: true}}
	players := []projection.PlayerProjection{
		testPlayer("a", projection.PlayerKindGoalie, 0, map[projection.Stat]float64{projection.StatGoalsAgainstAvg: 2, projection.StatTOISeconds: 1_000, projection.StatSavePercentage: .92, projection.StatShotsAgainst: 100}),
		testPlayer("b", projection.PlayerKindGoalie, 0, map[projection.Stat]float64{projection.StatGoalsAgainstAvg: 2, projection.StatTOISeconds: 10_000, projection.StatSavePercentage: .92, projection.StatShotsAgainst: 1_000}),
		testPlayer("c", projection.PlayerKindGoalie, 0, map[projection.Stat]float64{projection.StatGoalsAgainstAvg: 3, projection.StatTOISeconds: 10_000, projection.StatSavePercentage: .90, projection.StatShotsAgainst: 1_000}),
	}
	rules, projected, pool := testRankingInput("roto", []StatCategory{testCategory(rankingTestGAAStat, LowerIsBetter), testCategory(rankingTestSaveStat, HigherIsBetter)}, slots, players...)
	ranking, err := BuildRanking(rules, projected, pool, RankingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := rankedByKey(t, ranking, "a"), rankedByKey(t, ranking, "b"), rankedByKey(t, ranking, "c")
	if !(b.OfficialScore > a.OfficialScore && a.OfficialScore > c.OfficialScore) {
		t.Fatalf("ratio opportunity or lower-is-better direction wrong: a=%f b=%f c=%f", a.OfficialScore, b.OfficialScore, c.OfficialScore)
	}
	if b.Contributions[0].Opportunity != 10_000 || a.Contributions[0].Opportunity != 1_000 {
		t.Fatalf("GAA exposure must use TOI: a=%+v b=%+v", a.Contributions, b.Contributions)
	}
	if b.Contributions[1].Opportunity != 1_000 || a.Contributions[1].Opportunity != 100 {
		t.Fatalf("SV%% exposure must use shots against: a=%+v b=%+v", a.Contributions, b.Contributions)
	}
}

func TestBuildRanking_ReplacementBenchAndMultiPosition(t *testing.T) {
	slots := []RosterSlot{
		{Position: PositionCenter, Count: 1, Starting: true},
		{Position: PositionLeftWing, Count: 1, Starting: true},
		{Position: SlotBench, Count: 1},
		{Position: SlotInjuredReserve, Count: 2},
	}
	players := []projection.PlayerProjection{
		testPlayer("flex", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 10}),
		testPlayer("center", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 9}),
		testPlayer("wing", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 8}),
		testPlayer("waiver", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 7}),
	}
	rules, projected, pool := testRankingInput("point", []StatCategory{testWeightedCategory(rankingTestGoalStat, 1)}, slots, players...)
	pool[0].EligiblePositions = []string{PositionCenter, PositionLeftWing}
	pool[2].EligiblePositions = []string{PositionLeftWing}
	pool[3].EligiblePositions = []string{PositionLeftWing}
	withBench, err := BuildRanking(rules, projected, pool, RankingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	withoutBench, err := BuildRanking(rules, projected, pool, RankingOptions{BenchPolicy: BenchExcluded})
	if err != nil {
		t.Fatal(err)
	}
	if rankedByKey(t, withBench, "wing").Value != 1 || rankedByKey(t, withoutBench, "wing").Value != 0 {
		t.Fatalf("bench policy did not move replacement frontier")
	}
	if rankedByKey(t, withBench, "waiver").Value != 0 {
		t.Fatalf("undrafted player should anchor its own replacement frontier")
	}
	view := withBench.Filter(PositionCenter, PositionLeftWing)
	if len(view) != len(players) || view[0].PlayerKey != "flex" || len(view[0].PositionRanks) != 2 {
		t.Fatalf("multi-position view duplicates or loses rows: %+v", view)
	}
}

func TestBuildRanking_CapsMissingTiesAndIdentity(t *testing.T) {
	slots := []RosterSlot{{Position: PositionCenter, Count: 1, Starting: true}}
	players := []projection.PlayerProjection{
		testPlayer("a", projection.PlayerKindSkater, .1, map[projection.Stat]float64{projection.StatGoals: 20, projection.StatGamesPlayed: 80}),
		testPlayer("b", projection.PlayerKindSkater, .9, map[projection.Stat]float64{projection.StatGoals: 20, projection.StatGamesPlayed: 80}),
	}
	rules, projected, pool := testRankingInput("point", []StatCategory{testWeightedCategory(rankingTestGoalStat, 1)}, slots, players...)
	rules.Rules.Settings = map[string]string{"max_games_played": "40"}
	if _, err := BuildRanking(rules, projected, pool, RankingOptions{}); err == nil {
		t.Fatal("unverified workload cap interpretation must fail closed")
	}
	capOptions := RankingOptions{WorkloadCapPolicy: WorkloadCapsPerPlayer}
	ranking, err := BuildRanking(rules, projected, pool, capOptions)
	if err != nil {
		t.Fatal(err)
	}
	if ranking.Players[0].PlayerKey != "a" || ranking.Players[0].OfficialScore != 10 {
		t.Fatalf("game cap or stable key tie wrong: %+v", ranking.Players)
	}
	preferred, err := BuildRanking(rules, projected, pool, RankingOptions{
		WorkloadCapPolicy:  WorkloadCapsPerPlayer,
		CategoryWeights:    map[int]float64{rankingTestGoalStat: 0},
		UncertaintyPenalty: 1,
	})
	if err != nil || preferred.Version == ranking.Version || preferred.Players[0].Value != ranking.Players[0].Value {
		t.Fatalf("explicit strategy changed official value or lacked identity: %v", err)
	}
	pool[0].EligiblePositions = []string{PositionCenter, PositionLeftWing}
	changed, err := BuildRanking(rules, projected, pool, capOptions)
	if err != nil || changed.Version == ranking.Version {
		t.Fatalf("eligibility must affect identity: %v", err)
	}
	projected.Config.SeasonDecay = .5
	changedConfig, err := BuildRanking(rules, projected, pool, capOptions)
	if err != nil || changedConfig.Version == changed.Version {
		t.Fatalf("projection configuration must affect identity: %v", err)
	}
	pool[0].Name = "renamed"
	renamed, err := BuildRanking(rules, projected, pool, capOptions)
	if err != nil || renamed.Version == changedConfig.Version {
		t.Fatalf("displayed player name must affect identity: %v", err)
	}
	projected.Players[0].Values[projection.StatGoals] = testEstimate(math.NaN())
	if _, err := BuildRanking(rules, projected, pool, capOptions); err == nil {
		t.Fatal("nonfinite projection must fail")
	}
	projected.Players[0].Values = map[projection.Stat]projection.Estimate{}
	if _, err := BuildRanking(rules, projected, pool, capOptions); err == nil {
		t.Fatal("missing projection must fail")
	}
}

func TestBuildRanking_SeasonIdentityAndHeadToHead(t *testing.T) {
	slots := []RosterSlot{{Position: PositionCenter, Count: 1, Starting: true}}
	players := []projection.PlayerProjection{
		testPlayer("uncertain", projection.PlayerKindSkater, 1, map[projection.Stat]float64{projection.StatGoals: 20}),
		testPlayer("stable", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 10}),
		testPlayer("poor-stable", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 5}),
		testPlayer("poor-uncertain", projection.PlayerKindSkater, 1, map[projection.Stat]float64{projection.StatGoals: 5}),
	}
	rules, projected, pool := testRankingInput("roto", []StatCategory{testCategory(rankingTestGoalStat, HigherIsBetter)}, slots, players...)
	seasonLong, err := BuildRanking(rules, projected, pool, RankingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rules.Rules.ScoringType = "head"
	headToHead, err := BuildRanking(rules, projected, pool, RankingOptions{})
	if err != nil || rankedByKey(t, headToHead, "uncertain").OfficialScore >= rankedByKey(t, seasonLong, "uncertain").OfficialScore {
		t.Fatalf("head-to-head reliability assumption had no effect: %v", err)
	}
	if rankedByKey(t, headToHead, "poor-uncertain").OfficialScore >= rankedByKey(t, headToHead, "poor-stable").OfficialScore {
		t.Fatal("head-to-head uncertainty rewarded a below-average projection")
	}
	projected.TargetSeason = 20272028
	if _, err := BuildRanking(rules, projected, pool, RankingOptions{}); err == nil {
		t.Fatal("unrelated hockey seasons must not match")
	}
}

func TestBuildRanking_MultiPositionReplacementUsesLegalRematch(t *testing.T) {
	slots := []RosterSlot{
		{Position: PositionCenter, Count: 1, Starting: true},
		{Position: PositionLeftWing, Count: 1, Starting: true},
	}
	players := []projection.PlayerProjection{
		testPlayer("flex", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 10}),
		testPlayer("wing", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 9}),
		testPlayer("center", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 8}),
		testPlayer("center-depth", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 7}),
	}
	rules, projected, pool := testRankingInput("point", []StatCategory{
		testWeightedCategory(rankingTestGoalStat, 1),
	}, slots, players...)
	pool[0].EligiblePositions = []string{PositionCenter, PositionLeftWing}
	pool[1].EligiblePositions = []string{PositionLeftWing}
	ranking, err := BuildRanking(rules, projected, pool, RankingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	flex := rankedByKey(t, ranking, "flex")
	if flex.BaselineValue != 8 || flex.Value != 2 {
		t.Fatalf("flex replacement ignored legal rematch: %+v", flex)
	}
}

func TestBuildRanking_GoalieStartCapAndMissingRatioExposure(t *testing.T) {
	slots := []RosterSlot{{Position: PositionGoalie, Count: 1, Starting: true}}
	goalie := testPlayer("starter", projection.PlayerKindGoalie, .2, map[projection.Stat]float64{
		projection.StatWins: 20, projection.StatGamesPlayed: 60,
		projection.StatGamesStarted: 40, projection.StatSavePercentage: .92,
		projection.StatShotsAgainst: 1_000,
	})
	rules, projected, pool := testRankingInput("point", []StatCategory{testWeightedCategory(rankingTestWinStat, 1)}, slots, goalie)
	rules.Rules.Settings = map[string]string{"max_goalie_starts": "10"}
	if _, err := BuildRanking(rules, projected, pool, RankingOptions{}); err == nil {
		t.Fatal("unverified goalie cap interpretation must fail closed")
	}
	capOptions := RankingOptions{WorkloadCapPolicy: WorkloadCapsPerPlayer}
	ranking, err := BuildRanking(rules, projected, pool, capOptions)
	if err != nil {
		t.Fatal(err)
	}
	if got := ranking.Players[0].OfficialScore; got != 5 {
		t.Fatalf("goalie starts cap: score %.1f, want 5", got)
	}
	rules.Rules.ScoringType = "roto"
	rules.Rules.Categories = []StatCategory{testCategory(rankingTestSaveStat, HigherIsBetter)}
	delete(projected.Players[0].Values, projection.StatShotsAgainst)
	if _, err := BuildRanking(rules, projected, pool, capOptions); err == nil {
		t.Fatal("ratio without shots against must fail")
	}
}

func TestBuildRanking_RejectsUnmodeledWorkloadLimits(t *testing.T) {
	slots := []RosterSlot{{Position: PositionCenter, Count: 1, Starting: true}}
	player := testPlayer("center", projection.PlayerKindSkater, 0, map[projection.Stat]float64{
		projection.StatGoals: 10,
	})
	rules, projected, pool := testRankingInput("point", []StatCategory{
		testWeightedCategory(rankingTestGoalStat, 1),
	}, slots, player)
	rules.Rules.Settings = map[string]string{"min_goalie_appearances": "3"}
	_, err := BuildRanking(rules, projected, pool, RankingOptions{})
	if err == nil || !strings.Contains(err.Error(), "unmodeled workload limit") {
		t.Fatalf("goalie minimum must fail closed: %v", err)
	}
	rules.Rules.Settings = nil
	rules.Rules.UnmodeledSettings = []string{"settings.goalie_limits.minimum_starts"}
	_, err = BuildRanking(rules, projected, pool, RankingOptions{})
	if err == nil || !strings.Contains(err.Error(), "unmodeled workload limit") {
		t.Fatalf("nested workload limit must fail closed: %v", err)
	}
}

func TestBuildRanking_TeamCountMovesReplacementAndFilterCopies(t *testing.T) {
	slots := []RosterSlot{{Position: PositionCenter, Count: 1, Starting: true}}
	players := []projection.PlayerProjection{
		testPlayer("a", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 10}),
		testPlayer("b", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 8}),
		testPlayer("c", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 6}),
	}
	rules, projected, pool := testRankingInput("point", []StatCategory{testWeightedCategory(rankingTestGoalStat, 1)}, slots, players...)
	oneTeam, err := BuildRanking(rules, projected, pool, RankingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rules.Rules.NumTeams = 2
	twoTeams, err := BuildRanking(rules, projected, pool, RankingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rankedByKey(t, oneTeam, "a").BaselineValue != 8 || rankedByKey(t, twoTeams, "a").BaselineValue != 6 {
		t.Fatal("team count did not change replacement frontier")
	}
	view := twoTeams.Filter(PositionCenter)
	view[0].PositionRanks[PositionCenter] = 99
	view[0].Contributions[0].Official = 99
	if twoTeams.Players[0].PositionRanks[PositionCenter] == 99 || twoTeams.Players[0].Contributions[0].Official == 99 {
		t.Fatal("filter view mutated ranking snapshot")
	}
}

func TestBuildRanking_PositionalScarcityAndRanks(t *testing.T) {
	slots := []RosterSlot{
		{Position: PositionCenter, Count: 1, Starting: true},
		{Position: PositionLeftWing, Count: 1, Starting: true},
	}
	players := []projection.PlayerProjection{
		testPlayer("center-star", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 10}),
		testPlayer("wing-star", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 10}),
		testPlayer("center-backup", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 9}),
		testPlayer("center-third", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 8}),
		testPlayer("wing-backup", projection.PlayerKindSkater, 0, map[projection.Stat]float64{projection.StatGoals: 2}),
	}
	rules, projected, pool := testRankingInput("point", []StatCategory{testWeightedCategory(rankingTestGoalStat, 1)}, slots, players...)
	pool[1].EligiblePositions = []string{PositionLeftWing}
	pool[4].EligiblePositions = []string{PositionLeftWing}
	ranking, err := BuildRanking(rules, projected, pool, RankingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wing, center := rankedByKey(t, ranking, "wing-star"), rankedByKey(t, ranking, "center-star")
	if wing.OfficialScore != center.OfficialScore || wing.Value != 8 || center.Value != 1 || wing.OverallRank >= center.OverallRank {
		t.Fatalf("position scarcity did not change order: wing=%+v center=%+v", wing, center)
	}
	if wing.Tier >= center.Tier || wing.PositionRanks[PositionLeftWing] != 1 || center.PositionRanks[PositionCenter] != 1 {
		t.Fatalf("tiers or eligible position ranks wrong: wing=%+v center=%+v", wing, center)
	}
	if len(wing.Contributions) != 1 || wing.Contributions[0].Official != 10 || len(wing.Explanations) == 0 {
		t.Fatalf("contribution or explanation missing: %+v", wing)
	}
}

func TestBuildRanking_UncertaintySensitivityForGoalieAndRookie(t *testing.T) {
	slots := []RosterSlot{
		{Position: PositionCenter, Count: 2, Starting: true},
		{Position: PositionGoalie, Count: 2, Starting: true},
	}
	players := []projection.PlayerProjection{
		testPlayer("veteran", projection.PlayerKindSkater, .1, map[projection.Stat]float64{projection.StatGoals: 10}),
		testPlayer("rookie", projection.PlayerKindSkater, 1, map[projection.Stat]float64{projection.StatGoals: 10}),
		testPlayer("depth", projection.PlayerKindSkater, .1, map[projection.Stat]float64{projection.StatGoals: 5}),
		testPlayer("steady-goalie", projection.PlayerKindGoalie, .1, map[projection.Stat]float64{projection.StatWins: 10}),
		testPlayer("uncertain-goalie", projection.PlayerKindGoalie, 1, map[projection.Stat]float64{projection.StatWins: 10}),
		testPlayer("backup-goalie", projection.PlayerKindGoalie, .1, map[projection.Stat]float64{projection.StatWins: 5}),
	}
	rules, projected, pool := testRankingInput("point", []StatCategory{
		testWeightedCategory(rankingTestGoalStat, 1), testWeightedCategory(rankingTestWinStat, 1),
	}, slots, players...)
	base, err := BuildRanking(rules, projected, pool, RankingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rankedByKey(t, base, "rookie").Value != rankedByKey(t, base, "veteran").Value {
		t.Fatal("equal projected value should tie before explicit risk penalty")
	}
	cautious, err := BuildRanking(rules, projected, pool, RankingOptions{UncertaintyPenalty: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"veteran", "rookie"}, {"steady-goalie", "uncertain-goalie"}} {
		lowRisk := rankedByKey(t, cautious, pair[0])
		highRisk := rankedByKey(t, cautious, pair[1])
		if lowRisk.AdjustedValue <= highRisk.AdjustedValue || lowRisk.OverallRank >= highRisk.OverallRank {
			t.Fatalf("risk preference did not lower uncertain recommendation: %+v vs %+v", lowRisk, highRisk)
		}
	}
}
