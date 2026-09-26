package newsadjust

import (
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/projection"
	"github.com/stretchr/testify/assert"
)

func roleEvent(id, player string, eventType EventType, role RoleChange) Event {
	e := testEvent(id, player, eventType, Duration{})
	e.Role = &role
	return e
}

func TestApply_TradeWithBiggerRoleKeepsPerMinuteRates(t *testing.T) {
	skater := testSkater(testSkaterKey)
	newTeam := int64(22)
	trade := roleEvent("trade", testSkaterKey, EventTrade, RoleChange{TeamID: &newTeam, IceTime: DirectionUp})
	result := mustApply(t, testRequest(testBaseline(skater), trade))

	base := playerIn(t, result.Snapshots[ScenarioBase], testSkaterKey)
	assert.Equal(t, newTeam, *base.TeamID)
	factor := DefaultPolicy().IceTimeUp.Base
	assert.InDelta(t, testSkaterTOI*factor, base.Values[projection.StatTOISeconds].Mean, testFloatDelta)
	shotsPerSecond := skater.Values[projection.StatShotsOnGoal].Mean / skater.Values[projection.StatTOISeconds].Mean
	assert.InDelta(t, shotsPerSecond, base.Values[projection.StatShotsOnGoal].Mean/base.Values[projection.StatTOISeconds].Mean, testFloatDelta)
	assert.InDelta(t, base.Values[projection.StatPoints].Mean,
		base.Values[projection.StatGoals].Mean+base.Values[projection.StatAssists].Mean, testFloatDelta)
	assert.Equal(t, float64(testSkaterGames), base.Values[projection.StatGamesPlayed].Mean, "a role change does not change games")
	conservative := playerIn(t, result.Snapshots[ScenarioConservative], testSkaterKey)
	assert.Equal(t, float64(testSkaterTOI), conservative.Values[projection.StatTOISeconds].Mean)
	assert.True(t, containsText(adjustmentFor(t, result, testSkaterKey).Assumptions, "team context"))
}

func TestApply_PowerPlayPromotionIsAPositiveOpportunity(t *testing.T) {
	skater := testSkater(testSkaterKey)
	promotion := roleEvent("pp1", testSkaterKey, EventRoleChange, RoleChange{PowerPlay: DirectionUp})
	result := mustApply(t, testRequest(testBaseline(skater), promotion))

	factor := DefaultPolicy().PowerPlayUp.Base
	gain := testSkaterPPP * (factor - 1)
	assert.InDelta(t, testSkaterPPP*factor, meanIn(t, result, ScenarioBase, testSkaterKey, projection.StatPowerPlayPoints), testFloatDelta)
	assert.InDelta(t, testSkaterGoals+testSkaterAssist+gain, meanIn(t, result, ScenarioBase, testSkaterKey, projection.StatPoints), testFloatDelta)
	assert.Equal(t, float64(testSkaterTOI), meanIn(t, result, ScenarioBase, testSkaterKey, projection.StatTOISeconds),
		"power-play role alone leaves even-strength ice time")
	assert.Greater(t, meanIn(t, result, ScenarioOptimistic, testSkaterKey, projection.StatPoints),
		meanIn(t, result, ScenarioBase, testSkaterKey, projection.StatPoints))
}

func TestApply_TemporaryRoleCoversPartOfTheSeason(t *testing.T) {
	skater := testSkater(testSkaterKey)
	fillIn := roleEvent("fill-in", testSkaterKey, EventRoleChange, RoleChange{PowerPlay: DirectionUp})
	fillIn.EffectiveFrom = testSeasonStart
	fillIn.EffectiveUntil = dateAtGame(testSeasonGames / 2)
	result := mustApply(t, testRequest(testBaseline(skater), fillIn))

	expected := 1 + (DefaultPolicy().PowerPlayUp.Base-1)/2
	assert.InDelta(t, expected, adjustmentFor(t, result, testSkaterKey).Effects[ScenarioBase].PowerPlay, 1e-6)
}

func TestApply_BackupNamedStarterGetsStartsNotBetterRatios(t *testing.T) {
	const backupStarts = 25
	backup := testGoalie(testBackupKey, backupStarts)
	named := roleEvent("starter", testBackupKey, EventRoleChange, RoleChange{GoalieRole: GoalieRoleStarter})
	result := mustApply(t, testRequest(testBaseline(backup), named))

	share := DefaultPolicy().GoalieStartShare[GoalieRoleStarter]
	for _, s := range Scenarios {
		assert.InDelta(t, share.at(s)*testSeasonGames, meanIn(t, result, s, testBackupKey, projection.StatGamesStarted), 1e-6)
		assert.Equal(t, backup.Values[projection.StatSavePercentage], playerIn(t, result.Snapshots[s], testBackupKey).Values[projection.StatSavePercentage])
	}
}

func TestApply_NewerRoleReportReplacesOlder(t *testing.T) {
	skater := testSkater(testSkaterKey)
	up := roleEvent("up", testSkaterKey, EventRoleChange, RoleChange{IceTime: DirectionUp})
	down := roleEvent("down", testSkaterKey, EventRoleChange, RoleChange{IceTime: DirectionDown})
	down.ReportedAt = up.ReportedAt.Add(24 * time.Hour)
	result := mustApply(t, testRequest(testBaseline(skater), up, down))

	assert.Equal(t, DefaultPolicy().IceTimeDown.Base, adjustmentFor(t, result, testSkaterKey).Effects[ScenarioBase].IceTime)
	assert.Equal(t, OutcomeSkipped, decisionFor(t, result, "up").Outcome)
}

func TestApply_GoalieRoleForSkaterIsAnAlertNotAnEffect(t *testing.T) {
	skater := testSkater(testSkaterKey)
	wrong := roleEvent("wrong", testSkaterKey, EventRoleChange, RoleChange{GoalieRole: GoalieRoleStarter})
	result := mustApply(t, testRequest(testBaseline(skater), wrong))

	assert.Equal(t, skater.Values, playerIn(t, result.Snapshots[ScenarioBase], testSkaterKey).Values)
	assert.True(t, containsText(adjustmentFor(t, result, testSkaterKey).Alerts, "ignored"))
}

func TestApply_InjuryDuringNewRolePropagatesBoth(t *testing.T) {
	skater := testSkater(testSkaterKey)
	role := roleEvent("pp1", testSkaterKey, EventRoleChange, RoleChange{PowerPlay: DirectionUp})
	injury := testEvent("injury", testSkaterKey, EventInjury, Duration{Kind: DurationGames, Games: 8})
	result := mustApply(t, testRequest(testBaseline(skater), role, injury))

	available := (testSeasonGames - 8.0) / testSeasonGames
	assert.InDelta(t, testSkaterGames*available, meanIn(t, result, ScenarioBase, testSkaterKey, projection.StatGamesPlayed), testFloatDelta)
	assert.InDelta(t, testSkaterPPP*available*DefaultPolicy().PowerPlayUp.Base,
		meanIn(t, result, ScenarioBase, testSkaterKey, projection.StatPowerPlayPoints), testFloatDelta)
}

func TestApply_RepeatedRoleReportsDoNotCompound(t *testing.T) {
	skater := testSkater(testSkaterKey)
	first := roleEvent("first", testSkaterKey, EventRoleChange, RoleChange{PowerPlay: DirectionUp})
	repeat := roleEvent("repeat", testSkaterKey, EventRoleChange, RoleChange{PowerPlay: DirectionUp})
	repeat.ReportedAt = first.ReportedAt.Add(time.Hour)
	result := mustApply(t, testRequest(testBaseline(skater), first, repeat))

	assert.Equal(t, DefaultPolicy().PowerPlayUp.Base, adjustmentFor(t, result, testSkaterKey).Effects[ScenarioBase].PowerPlay)
}

func TestApply_ReversedRoleKeepsItsEarlierPeriod(t *testing.T) {
	skater := testSkater(testSkaterKey)
	promoted := roleEvent("promoted", testSkaterKey, EventRoleChange, RoleChange{PowerPlay: DirectionUp})
	promoted.EffectiveFrom = testSeasonStart
	demoted := roleEvent("demoted", testSkaterKey, EventRoleChange, RoleChange{PowerPlay: DirectionDown})
	demoted.EffectiveFrom = dateAtGame(testSeasonGames / 2)
	demoted.ReportedAt, demoted.RecordedAt = demoted.EffectiveFrom, demoted.EffectiveFrom
	req := testRequest(testBaseline(skater), promoted, demoted)
	req.AsOf = demoted.RecordedAt.Add(time.Hour)
	result := mustApply(t, req)

	policy := DefaultPolicy()
	expected := 1 + (policy.PowerPlayUp.Base-1)/2 + (policy.PowerPlayDown.Base-1)/2
	assert.InDelta(t, expected, adjustmentFor(t, result, testSkaterKey).Effects[ScenarioBase].PowerPlay, 1e-6)
	assert.Equal(t, OutcomeApplied, decisionFor(t, result, "promoted").Outcome)
}
