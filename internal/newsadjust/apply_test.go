package newsadjust

import (
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApply_IndefiniteSuspensionUsesLabeledScenarios(t *testing.T) {
	goalie := testGoalie(testGoalieKey, testGoalieStarts)
	suspension := testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite})
	result := mustApply(t, testRequest(testBaseline(goalie), suspension))

	policy := DefaultPolicy().MissedGames[DurationIndefinite]
	assert.Equal(t, policy.Conservative, missed(result, testGoalieKey, ScenarioConservative))
	assert.Equal(t, policy.Base, missed(result, testGoalieKey, ScenarioBase))
	assert.Equal(t, policy.Optimistic, missed(result, testGoalieKey, ScenarioOptimistic))

	for _, s := range Scenarios {
		available := (testSeasonGames - missed(result, testGoalieKey, s)) / testSeasonGames
		assert.InDelta(t, testGoalieStarts*available, meanIn(t, result, s, testGoalieKey, projection.StatGamesStarted), testFloatDelta)
		assert.InDelta(t, testGoalieWins*available, meanIn(t, result, s, testGoalieKey, projection.StatWins), testFloatDelta)
		// Availability never degrades the per-start ratios.
		assert.Equal(t, goalie.Values[projection.StatSavePercentage], playerIn(t, result.Snapshots[s], testGoalieKey).Values[projection.StatSavePercentage])
		assert.Equal(t, goalie.Values[projection.StatGoalsAgainstAvg], playerIn(t, result.Snapshots[s], testGoalieKey).Values[projection.StatGoalsAgainstAvg])
	}
	adjustment := adjustmentFor(t, result, testGoalieKey)
	assert.Greater(t, adjustment.Uncertainty, goalie.Uncertainty)
	assert.True(t, containsText(adjustment.Assumptions, DefaultCalibration), "unknown-duration defaults must be labeled: %v", adjustment.Assumptions)
	base := playerIn(t, result.Snapshots[ScenarioBase], testGoalieKey).Values[projection.StatGamesStarted]
	assert.InDelta(t, testGoalieStarts*(testSeasonGames-policy.Conservative)/testSeasonGames, base.Low, testFloatDelta,
		"each scenario's interval covers the conservative outcome")
	assert.InDelta(t, testGoalieStarts, base.High, testFloatDelta, "and the optimistic one")
}

func TestApply_AttributedGameCountIsCertainInEveryScenario(t *testing.T) {
	goalie := testGoalie(testGoalieKey, testGoalieStarts)
	result := mustApply(t, testRequest(testBaseline(goalie),
		testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationGames, Games: 10})))

	for _, s := range Scenarios {
		assert.Equal(t, 10.0, missed(result, testGoalieKey, s))
	}
	assert.Equal(t, goalie.Uncertainty, adjustmentFor(t, result, testGoalieKey).Uncertainty)
}

func TestApply_DuplicateReportsOfOneIncidentDoNotCompound(t *testing.T) {
	goalie := testGoalie(testGoalieKey, testGoalieStarts)
	official := testEvent("official", testGoalieKey, EventSuspension, Duration{Kind: DurationGames, Games: 10})
	mirror := official
	mirror.ID = "team-site"
	mirror.Evidence = []EvidenceRef{testEvidence(testEvidenceID+1, news.KindOfficial, testReportedAt)}
	wire := official
	wire.ID, wire.ReportedAt = "wire", testReportedAt.Add(time.Hour)
	wire.Evidence = []EvidenceRef{testEvidence(testEvidenceID+2, news.KindReporting, wire.ReportedAt)}
	for _, e := range []*Event{&official, &mirror, &wire} {
		e.IncidentID = 7
	}
	result := mustApply(t, testRequest(testBaseline(goalie), official, mirror, wire))

	assert.Equal(t, 10.0, missed(result, testGoalieKey, ScenarioBase))
	assert.Equal(t, OutcomeApplied, decisionFor(t, result, "official").Outcome)
	assert.Equal(t, OutcomeMerged, decisionFor(t, result, "team-site").Outcome)
	assert.Equal(t, OutcomeMerged, decisionFor(t, result, "wire").Outcome)
	assert.Empty(t, adjustmentFor(t, result, testGoalieKey).Alerts)
}

func TestApply_ProjectionThatIncorporatesTheEventIsNotPenalizedAgain(t *testing.T) {
	goalie := testGoalie(testGoalieKey, testGoalieStarts)
	goalie.Source, goalie.Provider, goalie.ProviderVersion = projection.SourceImported, "acme", "2026.09"
	goalie.IncorporatesNewsThrough = testReportedAt.Add(time.Hour)
	incorporated := testEvent("old", testGoalieKey, EventSuspension, Duration{Kind: DurationGames, Games: 10})
	result := mustApply(t, testRequest(testBaseline(goalie), incorporated))

	assert.Equal(t, OutcomeSkipped, decisionFor(t, result, "old").Outcome)
	assert.Contains(t, decisionFor(t, result, "old").Reason, "already incorporates")
	assert.Equal(t, goalie.Values, playerIn(t, result.Snapshots[ScenarioConservative], testGoalieKey).Values)

	later := testEvent("new", testGoalieKey, EventInjury, Duration{Kind: DurationGames, Games: 4})
	later.ReportedAt = testReportedAt.Add(48 * time.Hour)
	later.RecordedAt, later.EffectiveFrom = later.ReportedAt, later.ReportedAt
	result = mustApply(t, testRequest(testBaseline(goalie), incorporated, later))
	assert.Equal(t, 4.0, missed(result, testGoalieKey, ScenarioBase), "news after the provider's cutoff still applies")
}

func TestApply_ReinstatementRemovesObsoletePenalty(t *testing.T) {
	goalie := testGoalie(testGoalieKey, testGoalieStarts)
	suspension := testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite})
	reinstated := testEvent("back", testGoalieKey, EventReturn, Duration{})
	reinstated.ReportedAt = time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)
	reinstated.RecordedAt, reinstated.EffectiveFrom = reinstated.ReportedAt, reinstated.ReportedAt

	result := mustApply(t, testRequest(testBaseline(goalie), suspension, reinstated))
	for _, s := range Scenarios {
		assert.Zero(t, missed(result, testGoalieKey, s))
		assert.Equal(t, float64(testGoalieStarts), meanIn(t, result, s, testGoalieKey, projection.StatGamesStarted))
	}
	assert.Contains(t, decisionFor(t, result, "susp").Reason, "closed by return back")
	assert.Equal(t, OutcomeApplied, decisionFor(t, result, "back").Outcome)

	before := testRequest(testBaseline(goalie), suspension, reinstated)
	before.AsOf = time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC)
	replay := mustApply(t, before)
	assert.Equal(t, DefaultPolicy().MissedGames[DurationIndefinite].Base, missed(replay, testGoalieKey, ScenarioBase),
		"a replay before the reinstatement sees the penalty")
}

func TestApply_MidSeasonReturnFixesAnUnknownDuration(t *testing.T) {
	goalie := testGoalie(testGoalieKey, testGoalieStarts)
	suspension := testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite})
	reinstated := testEvent("back", testGoalieKey, EventReturn, Duration{})
	reinstated.Supersedes = []string{"susp"}
	reinstated.EffectiveFrom = dateAtGame(20)
	reinstated.ReportedAt, reinstated.RecordedAt = reinstated.EffectiveFrom, reinstated.EffectiveFrom
	req := testRequest(testBaseline(goalie), suspension, reinstated)
	req.AsOf = reinstated.RecordedAt.Add(time.Hour)

	result := mustApply(t, req)
	for _, s := range Scenarios {
		assert.InDelta(t, 20, missed(result, testGoalieKey, s), 1e-6)
	}
}

func TestApply_ConflictingReportsSpanTheScenarios(t *testing.T) {
	goalie := testGoalie(testGoalieKey, testGoalieStarts)
	official := testEvent("official", testGoalieKey, EventSuspension, Duration{Kind: DurationGames, Games: 5})
	report := testEvent("report", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite})
	report.ReportedAt = testReportedAt.Add(time.Hour)
	report.Evidence = []EvidenceRef{testEvidence(testEvidenceID+1, news.KindReporting, report.ReportedAt)}
	official.IncidentID, report.IncidentID = 9, 9
	result := mustApply(t, testRequest(testBaseline(goalie), official, report))

	assert.Equal(t, DefaultPolicy().MissedGames[DurationIndefinite].Conservative, missed(result, testGoalieKey, ScenarioConservative))
	assert.Equal(t, 5.0, missed(result, testGoalieKey, ScenarioBase), "base follows the official report")
	assert.Zero(t, missed(result, testGoalieKey, ScenarioOptimistic))
	assert.True(t, containsText(adjustmentFor(t, result, testGoalieKey).Alerts, "conflicting reports"))
}

func TestApply_OverlappingAbsencesAreUnioned(t *testing.T) {
	goalie := testGoalie(testGoalieKey, testGoalieStarts)
	suspension := testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationGames, Games: 10})
	injury := testEvent("injury", testGoalieKey, EventInjury, Duration{Kind: DurationGames, Games: 15})
	injury.IncidentID = 2
	later := testEvent("later", testGoalieKey, EventInjury, Duration{Kind: DurationGames, Games: 5})
	later.IncidentID = 3
	later.EffectiveFrom = dateAtGame(40)
	result := mustApply(t, testRequest(testBaseline(goalie), suspension, injury, later))

	assert.InDelta(t, 20, missed(result, testGoalieKey, ScenarioBase), 1e-6)
}

func TestApply_ReplayAtHistoricalAsOfIgnoresLaterVersions(t *testing.T) {
	goalie := testGoalie(testGoalieKey, testGoalieStarts)
	first := testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite})
	second := first
	second.Version, second.Duration = 2, Duration{Kind: DurationGames, Games: 5}
	second.RecordedAt = time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC)

	early := testRequest(testBaseline(goalie), first, second)
	early.AsOf = time.Date(2026, time.September, 15, 0, 0, 0, 0, time.UTC)
	historical := mustApply(t, early)
	assert.Equal(t, DefaultPolicy().MissedGames[DurationIndefinite].Base, missed(historical, testGoalieKey, ScenarioBase))

	onlyKnown := early
	onlyKnown.Events = []Event{first}
	assert.Equal(t, historical.ID, mustApply(t, onlyKnown).ID, "versions recorded later do not change a historical replay")

	current := mustApply(t, testRequest(testBaseline(goalie), first, second))
	assert.Equal(t, 5.0, missed(current, testGoalieKey, ScenarioBase))
	assert.NotEqual(t, historical.ID, current.ID)
}

func TestApply_IsDeterministicAndLeavesBaselineUntouched(t *testing.T) {
	goalie, skater := testGoalie(testGoalieKey, testGoalieStarts), testSkater(testSkaterKey)
	baseline := testBaseline(goalie, skater)
	events := []Event{
		testEvent("a", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite}),
		testEvent("b", testSkaterKey, EventInjury, Duration{Kind: DurationWeekToWeek}),
		testEvent("c", testGoalieKey, EventInjury, Duration{Kind: DurationGames, Games: 3}),
	}
	first := mustApply(t, testRequest(baseline, events...))
	shuffled := append([]Event(nil), events...)
	rand.New(rand.NewPCG(1, 2)).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	second := mustApply(t, testRequest(baseline, shuffled...))

	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, first.Snapshots, second.Snapshots)
	assert.Equal(t, testGoalie(testGoalieKey, testGoalieStarts).Values, baseline.Players[0].Values)
	for _, s := range Scenarios {
		assert.NotEqual(t, baseline.SourceDataHash, first.Snapshots[s].SourceDataHash)
	}
}

func TestApply_RejectsInconsistentInputs(t *testing.T) {
	goalie := testGoalie(testGoalieKey, testGoalieStarts)
	event := testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite})

	late := testRequest(testBaseline(goalie), event)
	late.AsOf = testBaselineAt.Add(-time.Hour)
	_, err := Apply(late)
	assert.ErrorContains(t, err, "after the adjustment as-of")

	changed := event
	changed.Duration = Duration{Kind: DurationGames, Games: 2}
	_, err = Apply(testRequest(testBaseline(goalie), event, changed))
	assert.ErrorContains(t, err, "two different contents")

	noEvidence := event
	noEvidence.Evidence = nil
	_, err = Apply(testRequest(testBaseline(goalie), noEvidence))
	assert.ErrorContains(t, err, "evidence")
}

func TestApply_CoverageWarningsBecomeAlerts(t *testing.T) {
	req := testRequest(testBaseline(testSkater(testSkaterKey)))
	req.CoverageWarnings = []string{"rotowire-nhl stale"}
	result := mustApply(t, req)
	require.Equal(t, []string{"rotowire-nhl stale"}, result.Alerts)
	assert.Empty(t, result.Players)
}

// dateAtGame is the date the fixture season reaches a number of team games.
func dateAtGame(games float64) time.Time {
	span := testSeasonEnd.Sub(testSeasonStart)
	return testSeasonStart.Add(time.Duration(float64(span) * games / testSeasonGames))
}

func containsText(values []string, fragment string) bool {
	for _, value := range values {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}
