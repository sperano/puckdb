package newsadjust

import (
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/projection"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testOverrideAt = time.Date(2026, time.September, 22, 0, 0, 0, 0, time.UTC)

func testOverride(id string, kind OverrideKind, value float64) Override {
	return Override{
		ID: id, PlayerKey: testGoalieKey, Kind: kind, Value: value,
		Reason: "manager expects an early return", CreatedBy: "eric", CreatedAt: testOverrideAt,
	}
}

func suspendedGoalieRequest(overrides ...Override) Request {
	req := testRequest(testBaseline(testGoalie(testGoalieKey, testGoalieStarts)),
		testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite}))
	req.Overrides = overrides
	return req
}

func TestApply_MissedGamesOverrideKeepsOriginalValues(t *testing.T) {
	result := mustApply(t, suspendedGoalieRequest(testOverride("o1", OverrideMissedGames, 10)))

	adjustment := adjustmentFor(t, result, testGoalieKey)
	require.Len(t, adjustment.Overrides, len(Scenarios))
	policy := DefaultPolicy().MissedGames[DurationIndefinite]
	for _, applied := range adjustment.Overrides {
		assert.Equal(t, 10.0, applied.Value)
		assert.Equal(t, policy.at(applied.Scenario), applied.Original)
		assert.Equal(t, 10.0, adjustment.Effects[applied.Scenario].MissedGames)
	}
	assert.Equal(t, "manager expects an early return", adjustment.Overrides[0].Override.Reason)
}

func TestApply_OverrideResetAndExpiryAreReplayable(t *testing.T) {
	reset := testOverride("o1", OverrideMissedGames, 10)
	reset.ResetAt, reset.ResetReason = testOverrideAt.Add(72*time.Hour), "suspension confirmed long"
	req := suspendedGoalieRequest(reset)
	req.AsOf = testOverrideAt.Add(24 * time.Hour)
	assert.Equal(t, 10.0, missed(mustApply(t, req), testGoalieKey, ScenarioBase), "active before its reset")

	req.AsOf = reset.ResetAt
	assert.Equal(t, DefaultPolicy().MissedGames[DurationIndefinite].Base, missed(mustApply(t, req), testGoalieKey, ScenarioBase),
		"a reset returns to the news-derived value")

	expiring := testOverride("o2", OverrideMissedGames, 10)
	expiring.ExpiresAt = testOverrideAt.Add(time.Hour)
	req = suspendedGoalieRequest(expiring)
	assert.Equal(t, DefaultPolicy().MissedGames[DurationIndefinite].Base, missed(mustApply(t, req), testGoalieKey, ScenarioBase))
}

func TestApply_OverrideScopeAndSpecificity(t *testing.T) {
	global := testOverride("global", OverrideMissedGames, 10)
	league := testOverride("league", OverrideMissedGames, 30)
	league.LeagueKey = testLeague1001
	scenario := testOverride("scenario", OverrideMissedGames, 0)
	scenario.Scenario, scenario.CreatedAt = ScenarioOptimistic, testOverrideAt.Add(-time.Hour)

	req := suspendedGoalieRequest(global, league, scenario)
	req.LeagueKey = testLeague1002
	other := mustApply(t, req)
	assert.Equal(t, 10.0, missed(other, testGoalieKey, ScenarioBase), "the other league sees only the global override")
	assert.Zero(t, missed(other, testGoalieKey, ScenarioOptimistic), "a scenario override beats an all-scenario one")

	req.LeagueKey = testLeague1001
	scoped := mustApply(t, req)
	assert.Equal(t, 30.0, missed(scoped, testGoalieKey, ScenarioBase))
	assert.Equal(t, 30.0, missed(scoped, testGoalieKey, ScenarioOptimistic), "league scope is more specific than scenario scope")
	assert.Len(t, scoped.Shadowed, 2)
	assert.NotEqual(t, other.ID, scoped.ID)
}

func TestApply_ExcludedEventHasNoEffect(t *testing.T) {
	exclude := testOverride("x", OverrideExcludeEvent, 0)
	exclude.EventID = "susp"
	result := mustApply(t, suspendedGoalieRequest(exclude))

	decision := decisionFor(t, result, "susp")
	assert.Equal(t, OutcomeSkipped, decision.Outcome)
	assert.Contains(t, decision.Reason, "excluded by override x")
	assert.Zero(t, missed(result, testGoalieKey, ScenarioConservative))
}

func TestApply_InputOverrideSetsStartsAndIgnoresMismatchedInputs(t *testing.T) {
	starts := testOverride("starts", OverrideInput, 50)
	starts.Input = InputGamesStarted
	toi := testOverride("toi", OverrideInput, 1200)
	toi.Input = InputTOIPerGame
	result := mustApply(t, suspendedGoalieRequest(starts, toi))

	for _, s := range Scenarios {
		assert.InDelta(t, 50, meanIn(t, result, s, testGoalieKey, projection.StatGamesStarted), testFloatDelta)
	}
	adjustment := adjustmentFor(t, result, testGoalieKey)
	assert.True(t, containsText(adjustment.Alerts, "override toi ignored"))
	for _, applied := range adjustment.Overrides {
		assert.Equal(t, "starts", applied.Override.ID)
	}
}

func TestOverride_Validate(t *testing.T) {
	valid := testOverride("o", OverrideMissedGames, 3)
	require.NoError(t, valid.Validate())
	for name, mutate := range map[string]func(*Override){
		"no reason":        func(o *Override) { o.Reason = " " },
		"negative value":   func(o *Override) { o.Value = -1 },
		"unknown scenario": func(o *Override) { o.Scenario = "wild" },
		"expiry first":     func(o *Override) { o.ExpiresAt = o.CreatedAt },
		"exclusion value":  func(o *Override) { o.Kind, o.EventID = OverrideExcludeEvent, "e" },
		"unknown input":    func(o *Override) { o.Kind, o.Input = OverrideInput, "speed" },
	} {
		o := valid
		mutate(&o)
		assert.Error(t, o.Validate(), name)
	}
}

func TestApply_SkaterInputOverrides(t *testing.T) {
	const (
		overrideGames     = 60
		overrideTOIPerGam = 1500
		overridePPFactor  = 0.5
	)
	skater := testSkater(testSkaterKey)
	games := Override{ID: "gp", PlayerKey: testSkaterKey, Kind: OverrideInput, Input: InputGamesPlayed, Value: overrideGames,
		Reason: "load management", CreatedAt: testOverrideAt}
	toi := games
	toi.ID, toi.Input, toi.Value = "toi", InputTOIPerGame, overrideTOIPerGam
	pp := games
	pp.ID, pp.Input, pp.Value = "pp", InputPowerPlayFactor, overridePPFactor
	tooMany := games
	tooMany.ID, tooMany.Input, tooMany.Value, tooMany.LeagueKey = "gs", InputGamesStarted, 10, testLeague1001
	req := testRequest(testBaseline(skater))
	req.Overrides, req.LeagueKey = []Override{games, toi, pp, tooMany}, testLeague1001
	result := mustApply(t, req)

	base := playerIn(t, result.Snapshots[ScenarioBase], testSkaterKey)
	assert.InDelta(t, overrideGames, base.Values[projection.StatGamesPlayed].Mean, testFloatDelta)
	assert.InDelta(t, overrideGames*overrideTOIPerGam, base.Values[projection.StatTOISeconds].Mean, testFloatDelta)
	assert.InDelta(t, testSkaterPPP*float64(overrideGames)/testSkaterGames*overridePPFactor, base.Values[projection.StatPowerPlayPoints].Mean, testFloatDelta)
	adjustment := adjustmentFor(t, result, testSkaterKey)
	assert.True(t, containsText(adjustment.Alerts, "games_started applies to goalies"))
	assert.Len(t, adjustment.Overrides, 3*len(Scenarios))
	for _, applied := range adjustment.Overrides {
		if applied.Override.ID == "gp" {
			assert.Equal(t, float64(testSkaterGames), applied.Original, "the replaced value is retained")
		}
	}
}
