package newsadjust

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestApply_RumorStaysAnAlertByDefault(t *testing.T) {
	goalie := testGoalie(testGoalieKey, testGoalieStarts)
	rumor := testEvent("rumor", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite})
	rumor.Status = StatusRumor
	result := mustApply(t, testRequest(testBaseline(goalie), rumor))

	assert.Equal(t, OutcomeAlert, decisionFor(t, result, "rumor").Outcome)
	for _, s := range Scenarios {
		assert.Equal(t, goalie.Values, playerIn(t, result.Snapshots[s], testGoalieKey).Values)
	}
	assert.True(t, containsText(adjustmentFor(t, result, testGoalieKey).Alerts, "rumor"))
}

func TestApply_RumorPolicyCanApplyConservativeOnly(t *testing.T) {
	rumor := testEvent("rumor", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite})
	rumor.Status = StatusRumor
	req := testRequest(testBaseline(testGoalie(testGoalieKey, testGoalieStarts)), rumor)
	req.Policy.Rumors = RumorConservative
	result := mustApply(t, req)

	assert.Equal(t, DefaultPolicy().MissedGames[DurationIndefinite].Conservative, missed(result, testGoalieKey, ScenarioConservative))
	assert.Zero(t, missed(result, testGoalieKey, ScenarioBase))
	assert.Equal(t, []Scenario{ScenarioConservative}, decisionFor(t, result, "rumor").Scenarios)
}

func TestApply_RumoredReturnDoesNotRemoveAConfirmedPenalty(t *testing.T) {
	suspension := testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite})
	rumor := testEvent("rumor", testGoalieKey, EventReturn, Duration{})
	rumor.Status = StatusRumor
	rumor.ReportedAt = testReportedAt.Add(24 * time.Hour)
	rumor.EffectiveFrom = rumor.ReportedAt
	result := mustApply(t, testRequest(testBaseline(testGoalie(testGoalieKey, testGoalieStarts)), suspension, rumor))

	assert.Equal(t, DefaultPolicy().MissedGames[DurationIndefinite].Base, missed(result, testGoalieKey, ScenarioBase))
	assert.Equal(t, OutcomeAlert, decisionFor(t, result, "rumor").Outcome)
}

func TestApply_CorrectionAndRetractionSupersedeWithAuditTrail(t *testing.T) {
	original := testEvent("orig", testGoalieKey, EventSuspension, Duration{Kind: DurationGames, Games: 10})
	correction := testEvent("fix", testGoalieKey, EventSuspension, Duration{Kind: DurationGames, Games: 2})
	correction.Supersedes = []string{"orig"}
	retracted := testEvent("gone", testGoalieKey, EventInjury, Duration{Kind: DurationGames, Games: 20})
	retracted.Lifecycle = LifecycleRetracted
	result := mustApply(t, testRequest(testBaseline(testGoalie(testGoalieKey, testGoalieStarts)), original, correction, retracted))

	assert.Equal(t, 2.0, missed(result, testGoalieKey, ScenarioBase))
	assert.Equal(t, "superseded by event fix", decisionFor(t, result, "orig").Reason)
	assert.Equal(t, OutcomeSkipped, decisionFor(t, result, "gone").Outcome)
	assert.Len(t, adjustmentFor(t, result, testGoalieKey).Reasons, 3, "skipped events stay in the explanation")
}

func TestApply_EventForUnknownPlayerIsReported(t *testing.T) {
	event := testEvent("e", testOtherKey, EventInjury, Duration{Kind: DurationGames, Games: 2})
	result := mustApply(t, testRequest(testBaseline(testSkater(testSkaterKey)), event))

	assert.Equal(t, OutcomeSkipped, decisionFor(t, result, "e").Outcome)
	assert.Empty(t, result.Players)
}

func TestApply_ResolvedEventEndsAtItsRecordedEnd(t *testing.T) {
	injury := testEvent("inj", testGoalieKey, EventInjury, Duration{Kind: DurationMonthToMonth})
	injury.EffectiveFrom = testSeasonStart
	injury.Lifecycle, injury.EffectiveUntil = LifecycleResolved, dateAtGame(6)
	result := mustApply(t, testRequest(testBaseline(testGoalie(testGoalieKey, testGoalieStarts)), injury))

	for _, s := range Scenarios {
		assert.InDelta(t, 6, missed(result, testGoalieKey, s), 1e-6)
	}
}

func TestApply_IncorporatedEventLaterClosedRaisesAlert(t *testing.T) {
	goalie := testGoalie(testGoalieKey, testGoalieStarts)
	goalie.Provider, goalie.ProviderVersion = "acme", "v1"
	goalie.IncorporatesNewsThrough = testReportedAt.Add(time.Hour)
	suspension := testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite})
	back := testEvent("back", testGoalieKey, EventReturn, Duration{})
	back.ReportedAt = testReportedAt.Add(72 * time.Hour)
	back.EffectiveFrom = back.ReportedAt
	result := mustApply(t, testRequest(testBaseline(goalie), suspension, back))

	assert.True(t, containsText(adjustmentFor(t, result, testGoalieKey).Alerts, "refresh that projection"))
}

func TestApply_UnnamedReturnLeavesFinishedAbsencesAlone(t *testing.T) {
	served := testEvent("served", testGoalieKey, EventSuspension, Duration{Kind: DurationGames, Games: 2})
	served.EffectiveFrom = dateAtGame(5)
	injury := testEvent("injury", testGoalieKey, EventInjury, Duration{Kind: DurationDayToDay})
	injury.IncidentID = 2
	injury.EffectiveFrom = dateAtGame(50)
	back := testEvent("back", testGoalieKey, EventReturn, Duration{})
	back.EffectiveFrom = dateAtGame(52)
	back.ReportedAt, back.RecordedAt = back.EffectiveFrom, back.EffectiveFrom
	req := testRequest(testBaseline(testGoalie(testGoalieKey, testGoalieStarts)), served, injury, back)
	req.AsOf = back.RecordedAt.Add(time.Hour)
	result := mustApply(t, req)

	for _, s := range Scenarios {
		assert.InDelta(t, 4, missed(result, testGoalieKey, s), 1e-6, "2 served games plus 2 injured, in %s", s)
	}
	assert.NotContains(t, decisionFor(t, result, "served").Reason, "closed by return")
}

func TestApply_PriorSeasonReportsDoNotEmptyTheTargetSeason(t *testing.T) {
	lastSeason := time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC)
	outForSeason := testEvent("season", testGoalieKey, EventInjury, Duration{Kind: DurationSeason})
	stale := testEvent("stale", testSkaterKey, EventInjury, Duration{Kind: DurationWeekToWeek})
	for _, e := range []*Event{&outForSeason, &stale} {
		e.ReportedAt, e.RecordedAt, e.EffectiveFrom = lastSeason, lastSeason, lastSeason
	}
	stale.IncidentID = 3
	result := mustApply(t, testRequest(testBaseline(testGoalie(testGoalieKey, testGoalieStarts), testSkater(testSkaterKey)), outForSeason, stale))

	for _, s := range Scenarios {
		assert.Zero(t, missed(result, testGoalieKey, s), "last season's season-ending report ends with it")
	}
	assert.True(t, containsText(adjustmentFor(t, result, testGoalieKey).Assumptions, "previous season"))
	assert.Equal(t, DefaultPolicy().MissedGames[DurationWeekToWeek].Conservative, missed(result, testSkaterKey, ScenarioConservative))
	assert.Zero(t, missed(result, testSkaterKey, ScenarioBase), "an old injury with no update counts only conservatively")
}

func TestApply_InSeasonReturnDoesNotReviveLastSeasonsInjury(t *testing.T) {
	lastSeason := time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC)
	stale := testEvent("stale", testSkaterKey, EventInjury, Duration{Kind: DurationWeekToWeek})
	stale.ReportedAt, stale.RecordedAt, stale.EffectiveFrom = lastSeason, lastSeason, lastSeason
	fresh := testEvent("fresh", testSkaterKey, EventInjury, Duration{Kind: DurationDayToDay})
	fresh.IncidentID = 4
	fresh.EffectiveFrom = dateAtGame(20)
	back := testEvent("back", testSkaterKey, EventReturn, Duration{})
	back.EffectiveFrom = dateAtGame(22)
	back.ReportedAt, back.RecordedAt = back.EffectiveFrom, back.EffectiveFrom
	req := testRequest(testBaseline(testSkater(testSkaterKey)), stale, fresh, back)
	req.AsOf = back.RecordedAt.Add(time.Hour)
	result := mustApply(t, req)

	assert.InDelta(t, 2, missed(result, testSkaterKey, ScenarioBase), 1e-6)
	assert.InDelta(t, 2, missed(result, testSkaterKey, ScenarioOptimistic), 1e-6)
	assert.NotContains(t, decisionFor(t, result, "stale").Reason, "closed by return")
}
