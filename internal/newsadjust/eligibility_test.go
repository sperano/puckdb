package newsadjust

import (
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/projection"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exclusion tests: an exclude_event override removes the event from the
// scenarios it covers, including the supersessions and closures the event
// would otherwise create, and leaves every other scenario as it was.

var testReturnAt = time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)

const (
	testCorrectionGames = 5
	testOriginalGames   = 10
)

func exclusionOf(id, eventID string) Override {
	o := testOverride(id, OverrideExcludeEvent, 0)
	o.EventID, o.Reason = eventID, "manager rejects this report"
	return o
}

// preseasonReturn is a confirmed return before the season naming targets.
func preseasonReturn(id string, targets ...string) Event {
	ret := testEvent(id, testGoalieKey, EventReturn, Duration{})
	ret.ReportedAt, ret.RecordedAt, ret.EffectiveFrom = testReturnAt, testReturnAt, testReturnAt
	ret.Supersedes = targets
	return ret
}

// correctionOf is a confirmed report that corrects target's game count.
func correctionOf(id, target string, games int) Event {
	fix := testEvent(id, testGoalieKey, EventSuspension, Duration{Kind: DurationGames, Games: games})
	fix.ReportedAt = testReportedAt.Add(24 * time.Hour)
	fix.RecordedAt = fix.ReportedAt.Add(time.Hour)
	fix.Supersedes = []string{target}
	return fix
}

func goalieRequest(events []Event, overrides ...Override) Request {
	req := testRequest(testBaseline(testGoalie(testGoalieKey, testGoalieStarts)), events...)
	req.Overrides = overrides
	return req
}

// assertSameScenarios checks that two results give the player the same
// effect and the same adjusted projection in the listed scenarios.
func assertSameScenarios(t *testing.T, want, got Result, key string, scenarios ...Scenario) {
	t.Helper()
	for _, s := range scenarios {
		assert.Equal(t, missed(want, key, s), missed(got, key, s), "missed games in %s", s)
		assert.Equal(t, playerIn(t, want.Snapshots[s], key), playerIn(t, got.Snapshots[s], key), "projection in %s", s)
	}
}

func TestApply_ExcludedReturnDoesNotCloseItsTarget(t *testing.T) {
	suspension := testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite})
	result := mustApply(t, goalieRequest([]Event{suspension, preseasonReturn("back", "susp")}, exclusionOf("x", "back")))

	assert.Equal(t, OutcomeSkipped, decisionFor(t, result, "back").Outcome)
	assert.Contains(t, decisionFor(t, result, "back").Reason, "excluded by override x")
	assert.NotContains(t, decisionFor(t, result, "susp").Reason, "closed by")
	policy := DefaultPolicy().MissedGames[DurationIndefinite]
	assert.Equal(t, policy.Base, missed(result, testGoalieKey, ScenarioBase))
	assertSameScenarios(t, mustApply(t, goalieRequest([]Event{suspension})), result, testGoalieKey, Scenarios...)
}

func TestApply_ExcludedReturnNamingNothingClosesNothing(t *testing.T) {
	suspension := testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationWeekToWeek})
	result := mustApply(t, goalieRequest([]Event{suspension, preseasonReturn("back")}, exclusionOf("x", "back")))

	assertSameScenarios(t, mustApply(t, goalieRequest([]Event{suspension})), result, testGoalieKey, Scenarios...)
}

func TestApply_ExcludedCorrectionLeavesItsPredecessorInForce(t *testing.T) {
	original := testEvent("orig", testGoalieKey, EventSuspension, Duration{Kind: DurationGames, Games: testOriginalGames})
	fix := correctionOf("fix", "orig", testCorrectionGames)

	corrected := mustApply(t, goalieRequest([]Event{original, fix}))
	assert.Equal(t, float64(testCorrectionGames), missed(corrected, testGoalieKey, ScenarioBase), "the correction applies when not excluded")

	result := mustApply(t, goalieRequest([]Event{original, fix}, exclusionOf("x", "fix")))
	assert.Equal(t, OutcomeApplied, decisionFor(t, result, "orig").Outcome)
	assert.Equal(t, OutcomeSkipped, decisionFor(t, result, "fix").Outcome)
	for _, s := range Scenarios {
		assert.Equal(t, float64(testOriginalGames), missed(result, testGoalieKey, s))
	}
	assertSameScenarios(t, mustApply(t, goalieRequest([]Event{original})), result, testGoalieKey, Scenarios...)
}

func TestApply_ExclusionResetAndExpiryRestoreTheEventsLinks(t *testing.T) {
	suspension := testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite})
	events := []Event{suspension, preseasonReturn("back", "susp")}
	withReturn := mustApply(t, goalieRequest(events))
	withoutReturn := mustApply(t, goalieRequest([]Event{suspension}))

	reset := exclusionOf("x", "back")
	reset.ResetAt, reset.ResetReason = testReturnAt.Add(24*time.Hour), "return confirmed after all"
	req := goalieRequest(events, reset)
	req.AsOf = reset.ResetAt.Add(-time.Hour)
	assertSameScenarios(t, withoutReturn, mustApply(t, req), testGoalieKey, Scenarios...)
	req.AsOf = reset.ResetAt
	restored := mustApply(t, req)
	assertSameScenarios(t, withReturn, restored, testGoalieKey, Scenarios...)
	assert.Contains(t, decisionFor(t, restored, "susp").Reason, "closed by return back")

	expiring := exclusionOf("x", "back")
	expiring.ExpiresAt = testReturnAt.Add(12 * time.Hour)
	req = goalieRequest(events, expiring)
	req.AsOf = expiring.ExpiresAt.Add(-time.Hour)
	assertSameScenarios(t, withoutReturn, mustApply(t, req), testGoalieKey, Scenarios...)
	req.AsOf = expiring.ExpiresAt
	assertSameScenarios(t, withReturn, mustApply(t, req), testGoalieKey, Scenarios...)
}

func TestApply_ExclusionScopeMatrix(t *testing.T) {
	policy := DefaultPolicy().MissedGames[DurationWeekToWeek]
	unchanged := map[Scenario]float64{
		ScenarioConservative: policy.Conservative, ScenarioBase: policy.Base, ScenarioOptimistic: policy.Optimistic,
	}
	removed := map[Scenario]float64{ScenarioConservative: 0, ScenarioBase: 0, ScenarioOptimistic: 0}
	for name, tc := range map[string]struct {
		mutate func(*Override)
		league string
		want   map[Scenario]float64
	}{
		"global":          {mutate: func(*Override) {}, want: removed},
		"optimistic only": {mutate: func(o *Override) { o.Scenario = ScenarioOptimistic }, want: map[Scenario]float64{ScenarioConservative: policy.Conservative, ScenarioBase: policy.Base, ScenarioOptimistic: 0}},
		"base only":       {mutate: func(o *Override) { o.Scenario = ScenarioBase }, want: map[Scenario]float64{ScenarioConservative: policy.Conservative, ScenarioBase: 0, ScenarioOptimistic: policy.Optimistic}},
		"same league":     {mutate: func(o *Override) { o.LeagueKey = testLeague1001 }, league: testLeague1001, want: removed},
		"other league":    {mutate: func(o *Override) { o.LeagueKey = testLeague1001 }, league: testLeague1002, want: unchanged},
		"no league":       {mutate: func(o *Override) { o.LeagueKey = testLeague1001 }, want: unchanged},
		"other player":    {mutate: func(o *Override) { o.PlayerKey = testBackupKey }, want: unchanged},
		"reset":           {mutate: func(o *Override) { o.ResetAt, o.ResetReason = testOverrideAt.Add(time.Hour), "wrong call" }, want: unchanged},
		"reset later":     {mutate: func(o *Override) { o.ResetAt, o.ResetReason = testDraftAt.Add(time.Hour), "wrong call" }, want: removed},
		"expired":         {mutate: func(o *Override) { o.ExpiresAt = testOverrideAt.Add(time.Hour) }, want: unchanged},
		"expires later":   {mutate: func(o *Override) { o.ExpiresAt = testDraftAt.Add(time.Hour) }, want: removed},
		"created later":   {mutate: func(o *Override) { o.CreatedAt = testDraftAt.Add(time.Hour) }, want: unchanged},
	} {
		t.Run(name, func(t *testing.T) {
			exclusion := exclusionOf("x", "susp")
			tc.mutate(&exclusion)
			req := goalieRequest([]Event{testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationWeekToWeek})}, exclusion)
			req.LeagueKey = tc.league
			result := mustApply(t, req)
			for _, s := range Scenarios {
				assert.Equal(t, tc.want[s], missed(result, testGoalieKey, s), "missed games in %s", s)
			}
		})
	}
}

func TestApply_OptimisticOnlyExclusionLeavesOtherScenariosUnchanged(t *testing.T) {
	suspension := testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationWeekToWeek})
	exclusion := exclusionOf("x", "susp")
	exclusion.Scenario = ScenarioOptimistic
	plain := mustApply(t, goalieRequest([]Event{suspension}))
	result := mustApply(t, goalieRequest([]Event{suspension}, exclusion))

	for _, s := range []Scenario{ScenarioConservative, ScenarioBase} {
		assert.Equal(t, plain.Players[0].Effects[s], result.Players[0].Effects[s], "effect in %s", s)
		for stat, estimate := range playerIn(t, plain.Snapshots[s], testGoalieKey).Values {
			assert.Equal(t, estimate.Mean, playerIn(t, result.Snapshots[s], testGoalieKey).Values[stat].Mean, "%s mean in %s", stat, s)
		}
	}
	assert.Zero(t, missed(result, testGoalieKey, ScenarioOptimistic))
	assert.Equal(t, float64(testGoalieStarts), meanIn(t, result, ScenarioOptimistic, testGoalieKey, projection.StatGamesStarted))
	decision := decisionFor(t, result, "susp")
	assert.Equal(t, OutcomeApplied, decision.Outcome)
	assert.Equal(t, []Scenario{ScenarioConservative, ScenarioBase}, decision.Scenarios)
	assert.Contains(t, decision.Reason, "excluded by override x: manager rejects this report in optimistic")
}

func TestApply_ScenarioScopedReturnExclusionClosesOnlyElsewhere(t *testing.T) {
	suspension := testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationWeekToWeek})
	exclusion := exclusionOf("x", "back")
	exclusion.Scenario = ScenarioConservative
	result := mustApply(t, goalieRequest([]Event{suspension, preseasonReturn("back", "susp")}, exclusion))

	assert.Equal(t, DefaultPolicy().MissedGames[DurationWeekToWeek].Conservative, missed(result, testGoalieKey, ScenarioConservative))
	assert.Zero(t, missed(result, testGoalieKey, ScenarioBase))
	assert.Zero(t, missed(result, testGoalieKey, ScenarioOptimistic))
	assert.Contains(t, decisionFor(t, result, "susp").Reason, "closed by return back at 2026-09-25T12:00:00Z in base, optimistic")
	ret := decisionFor(t, result, "back")
	assert.Equal(t, OutcomeApplied, ret.Outcome)
	assert.Equal(t, []Scenario{ScenarioBase, ScenarioOptimistic}, ret.Scenarios)
}

func TestApply_ScenarioScopedCorrectionExclusionKeepsPredecessorThere(t *testing.T) {
	original := testEvent("orig", testGoalieKey, EventSuspension, Duration{Kind: DurationGames, Games: testOriginalGames})
	exclusion := exclusionOf("x", "fix")
	exclusion.Scenario = ScenarioConservative
	result := mustApply(t, goalieRequest([]Event{original, correctionOf("fix", "orig", testCorrectionGames)}, exclusion))

	assert.Equal(t, float64(testOriginalGames), missed(result, testGoalieKey, ScenarioConservative))
	assert.Equal(t, float64(testCorrectionGames), missed(result, testGoalieKey, ScenarioBase))
	assert.Equal(t, float64(testCorrectionGames), missed(result, testGoalieKey, ScenarioOptimistic))
	orig := decisionFor(t, result, "orig")
	assert.Equal(t, []Scenario{ScenarioConservative}, orig.Scenarios)
	assert.Contains(t, orig.Reason, "superseded by event fix in base, optimistic")
}

func TestApply_ExclusionOfAnotherPlayersEventIsIgnoredWithAlerts(t *testing.T) {
	exclusion := exclusionOf("x", "susp")
	exclusion.PlayerKey = testBackupKey
	suspension := testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationWeekToWeek})
	req := testRequest(testBaseline(testGoalie(testGoalieKey, testGoalieStarts), testGoalie(testBackupKey, testGoalieStarts)), suspension)
	req.Overrides = []Override{exclusion}
	result := mustApply(t, req)

	assert.Equal(t, OutcomeApplied, decisionFor(t, result, "susp").Outcome)
	assertSameScenarios(t, mustApply(t, goalieRequest([]Event{suspension})), result, testGoalieKey, Scenarios...)
	for _, key := range []string{testGoalieKey, testBackupKey} {
		assert.True(t, containsText(adjustmentFor(t, result, key).Alerts, "the exclusion is ignored"), key)
	}
	for _, s := range Scenarios {
		assert.Equal(t, testGoalie(testBackupKey, testGoalieStarts).Values, playerIn(t, result.Snapshots[s], testBackupKey).Values)
	}
}

func TestApply_ScenarioScopedRoleExclusionKeepsEarlierSegment(t *testing.T) {
	skater := testSkater(testSkaterKey)
	down := testEvent("down", testSkaterKey, EventRoleChange, Duration{})
	down.Role = &RoleChange{IceTime: DirectionDown}
	up := testEvent("up", testSkaterKey, EventRoleChange, Duration{})
	up.Role = &RoleChange{IceTime: DirectionUp}
	up.EffectiveFrom = dateAtGame(testSeasonGames / 2)
	up.ReportedAt, up.RecordedAt = testDraftAt.Add(-time.Hour), testDraftAt.Add(-time.Hour)
	exclusion := Override{ID: "x", PlayerKey: testSkaterKey, Kind: OverrideExcludeEvent, EventID: "up",
		Scenario: ScenarioBase, Reason: "doubt the promotion", CreatedAt: testOverrideAt}
	req := testRequest(testBaseline(skater), down, up)
	req.Overrides = []Override{exclusion}
	result := mustApply(t, req)
	onlyDown := mustApply(t, testRequest(testBaseline(skater), down))
	both := mustApply(t, testRequest(testBaseline(skater), down, up))

	require.NotEqual(t, onlyDown.Players[0].Effects[ScenarioBase].IceTime, both.Players[0].Effects[ScenarioBase].IceTime)
	assert.Equal(t, onlyDown.Players[0].Effects[ScenarioBase], result.Players[0].Effects[ScenarioBase],
		"in base the excluded report neither applies nor ends the earlier one")
	for _, s := range []Scenario{ScenarioConservative, ScenarioOptimistic} {
		assert.Equal(t, both.Players[0].Effects[s], result.Players[0].Effects[s], "effect in %s", s)
	}
	assert.Equal(t, []Scenario{ScenarioConservative, ScenarioOptimistic}, decisionFor(t, result, "up").Scenarios)
}

func TestApply_ExcludedReturnWarnsWhenItsTargetRecordsItsOwnEnd(t *testing.T) {
	resolved := testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite})
	resolved.Lifecycle, resolved.EffectiveUntil = LifecycleResolved, testReturnAt
	result := mustApply(t, goalieRequest([]Event{resolved, preseasonReturn("back", "susp")}, exclusionOf("x", "back")))

	assert.True(t, containsText(adjustmentFor(t, result, testGoalieKey).Alerts, "the exclusion does not reopen it"))
	assert.Zero(t, missed(result, testGoalieKey, ScenarioBase), "the stored end still applies")
}
