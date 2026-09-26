package graph

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/newsadjust"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGqlLeague_WeightsOnlyForPointsLeagues(t *testing.T) {
	categories := []draftrank.Category{{StatID: 1, Abbr: "G", Weight: 0}}
	points := gqlLeague(draftrank.League{Format: "points", Categories: categories})
	require.NotNil(t, points.Categories[0].Weight, "a points league reports even a zero weight")
	roto := gqlLeague(draftrank.League{Format: "categories", Categories: categories})
	assert.Nil(t, roto.Categories[0].Weight)
	assert.NotNil(t, roto.Categories[0].PositionTypes)
	assert.Nil(t, roto.RulesFetchedAt)
}

func TestGqlRefresh(t *testing.T) {
	assert.Nil(t, gqlRefresh(nil))
	started := time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC)
	got := gqlRefresh(&draftrank.Refresh{ID: uuid.New(), State: draftrank.RefreshFailed, Code: draftrank.IssueMissingProjections, Error: "x", StartedAt: started})
	assert.Equal(t, model.DraftRefreshStateFailed, got.State)
	assert.Equal(t, model.DraftIssueCodeMissingProjections, *got.Code)
	assert.Nil(t, got.SnapshotID)
	assert.Nil(t, got.FinishedAt)
}

func TestIssueCodesMatchTheSchema(t *testing.T) {
	for _, code := range []draftrank.IssueCode{
		draftrank.IssueMissingRules, draftrank.IssueUnsupportedScoring, draftrank.IssueMissingPool, draftrank.IssueMissingProjections,
		draftrank.IssueRankingFailed, draftrank.IssueInternalError, draftrank.IssueRefreshCanceled, draftrank.IssueNewsAdjustmentsDown,
		draftrank.IssueProvisionalRules, draftrank.IssueNewsSourceStale, draftrank.IssueNewsSourceFailing, draftrank.IssueNewsSourceMissing,
		draftrank.IssueStaleSnapshot, draftrank.IssueStalePool, draftrank.IssueOverridesChanged, draftrank.IssueRulesChanged, draftrank.IssueNewerSnapshot,
		draftrank.IssueScenarioFallback, draftrank.IssueNotComputed, draftrank.IssueRefreshRunning, draftrank.IssueRefreshFailed,
		draftrank.IssueRefreshInterrupted,
	} {
		assert.True(t, model.DraftIssueCode(code).IsValid(), "issue code %s is in the GraphQL enum", code)
	}
	for _, s := range draftrank.Scenarios {
		assert.True(t, gqlScenario(s).IsValid(), "scenario %s", s)
	}
	for _, status := range []draftrank.Status{draftrank.StatusReady, draftrank.StatusNotComputed, draftrank.StatusRefreshing, draftrank.StatusFailed} {
		assert.True(t, model.DraftRankingStatus(status).IsValid(), "status %s", status)
	}
}

func TestOverrideFromInput(t *testing.T) {
	expires := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.FixedZone("EDT", -4*3600))
	o := overrideFromInput(model.DraftOverrideCreateInput{
		PlayerKey: " 465.p.1 ", LeagueKey: new("465.l.1001"), Kind: model.DraftOverrideKindInput,
		Scenario: new(model.DraftScenarioOptimistic), Input: new(model.DraftOverrideInputToiPerGame), Value: new(1200.0),
		Reason: "coach said top line", ExpiresAt: &expires,
	})
	assert.Equal(t, newsadjust.Override{
		PlayerKey: "465.p.1", LeagueKey: "465.l.1001", Kind: newsadjust.OverrideInput, Scenario: newsadjust.ScenarioOptimistic,
		Input: newsadjust.InputTOIPerGame, Value: 1200, Reason: "coach said top line", ExpiresAt: expires.UTC(),
	}, o)
	back := gqlOverride(draftrank.OverrideStatus{Override: o, State: draftrank.OverrideActive})
	assert.Equal(t, model.DraftOverrideKindInput, back.Kind)
	assert.Equal(t, model.DraftOverrideInputToiPerGame, *back.Input)
	assert.Equal(t, model.DraftScenarioOptimistic, *back.Scenario)
	assert.Equal(t, model.DraftOverrideStateActive, back.State)
}
