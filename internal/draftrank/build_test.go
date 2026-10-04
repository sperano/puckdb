package draftrank_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixtureSnapshot(t *testing.T) *draftrank.Snapshot {
	t.Helper()
	snapshot, err := draftfixtures.Snapshot(uuid.NewSHA1(uuid.NameSpaceOID, []byte("snapshot")), draftfixtures.AsOf)
	require.NoError(t, err)
	return snapshot
}

func fixtureInput(t *testing.T) draftrank.BuildInput {
	t.Helper()
	input, err := draftfixtures.BuildInput()
	require.NoError(t, err)
	return input
}

func playerByKey(t *testing.T, snapshot *draftrank.Snapshot, key string) draftrank.Player {
	t.Helper()
	for _, player := range snapshot.Players {
		if player.PlayerKey == key {
			return player
		}
	}
	t.Fatalf("snapshot has no player %s", key)
	return draftrank.Player{}
}

func TestBuild_RanksEveryScenarioWithOneRankingModel(t *testing.T) {
	snapshot := fixtureSnapshot(t)

	assert.Equal(t, draftrank.Scenarios, snapshot.Scenarios)
	require.Len(t, snapshot.Players, len(draftfixtures.Pool()))
	for _, s := range draftrank.Scenarios {
		assert.Contains(t, snapshot.Versions[s], draft.RankingVersion, "scenario %s version", s)
	}
	for i, player := range snapshot.Players {
		assert.Equal(t, i+1, player.Placements[draftrank.ScenarioBaseline].OverallRank, "players are in baseline order")
		assert.Len(t, player.Placements, len(draftrank.Scenarios))
	}
	assert.Equal(t, []string{draft.PositionCenter, draft.PositionLeftWing}, playerByKey(t, snapshot, draftfixtures.CenterWing).EligiblePositions)
	assert.Equal(t, draftfixtures.LeagueKey, snapshot.League.LeagueKey)
	assert.Equal(t, "points", snapshot.League.Format)
	assert.Len(t, snapshot.League.Categories, 3)
	assert.Equal(t, draftfixtures.FetchedAt, snapshot.PoolFetchedAt)
	assert.Equal(t, draftfixtures.NewsSourceID, snapshot.News[0].SourceID)
}

func TestBuild_PreservesFullYahooRosterEligibility(t *testing.T) {
	input := fixtureInput(t)
	for i := range input.Pool {
		if input.Pool[i].PlayerKey == draftfixtures.CenterWing {
			input.Pool[i].EligiblePositions = append(input.Pool[i].EligiblePositions, draft.SlotInjuredReserve)
		}
	}
	snapshot, err := draftrank.Build(input)
	require.NoError(t, err)
	dual := playerByKey(t, &snapshot, draftfixtures.CenterWing)
	assert.Equal(t, []string{draft.PositionCenter, draft.PositionLeftWing}, dual.EligiblePositions)
	assert.Equal(t, []string{draft.PositionCenter, draft.PositionLeftWing, draft.SlotInjuredReserve}, dual.RosterEligiblePositions)
}

func TestBuild_SuspensionLowersOnlyTheSuspendedPlayer(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	suspended := playerByKey(t, snapshot, draftfixtures.SuspendedKey)
	dual := playerByKey(t, snapshot, draftfixtures.CenterWing)

	require.NotNil(t, suspended.Adjustment, "the suspended player carries the adjustment and its evidence")
	require.NotEmpty(t, suspended.Adjustment.Reasons)
	assert.NotEmpty(t, suspended.Adjustment.Reasons[0].Evidence)
	assert.Nil(t, dual.Adjustment)
	assert.Less(t, suspended.Placements[draftrank.ScenarioBase].OfficialScore, suspended.Placements[draftrank.ScenarioBaseline].OfficialScore)
	assert.Greater(t, suspended.Placements[draftrank.ScenarioBase].OverallRank, suspended.Placements[draftrank.ScenarioBaseline].OverallRank)
	assert.Less(t, dual.Placements[draftrank.ScenarioBase].OverallRank, dual.Placements[draftrank.ScenarioBaseline].OverallRank)
}

func TestBuild_IdentityIsDeterministicAndTracksOptions(t *testing.T) {
	input := fixtureInput(t)
	first, err := draftrank.Build(input)
	require.NoError(t, err)
	second, err := draftrank.Build(input)
	require.NoError(t, err)
	assert.Equal(t, first.Identity, second.Identity)

	input.Options.UncertaintyPenalty = 1
	penalized, err := draftrank.Build(input)
	require.NoError(t, err)
	assert.NotEqual(t, first.Identity, penalized.Identity)
}

func TestBuild_WithoutNewsKeepsBaselineAndSaysWhy(t *testing.T) {
	input := fixtureInput(t)
	input.Adjustment = nil
	input.Unavailable = []draftrank.Issue{{Code: draftrank.IssueNewsAdjustmentsDown, Message: "season dates are not imported"}}

	snapshot, err := draftrank.Build(input)
	require.NoError(t, err)
	assert.Equal(t, []draftrank.Scenario{draftrank.ScenarioBaseline}, snapshot.Scenarios)
	assert.Nil(t, snapshot.Adjustment)
	assert.Equal(t, input.Unavailable, snapshot.Unavailable)
	assert.Len(t, snapshot.Players[0].Placements, 1)
}

func TestBuild_FailuresCarryExplicitCodes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*draftrank.BuildInput)
		code   draftrank.IssueCode
	}{
		{"unsupported scoring type", func(in *draftrank.BuildInput) { in.Rules.Rules.ScoringType = "vegas" }, draftrank.IssueUnsupportedScoring},
		{"empty pool", func(in *draftrank.BuildInput) { in.Pool = nil }, draftrank.IssueMissingPool},
		{"pool player without projection", func(in *draftrank.BuildInput) {
			in.Baseline.Players = in.Baseline.Players[1:]
		}, draftrank.IssueRankingFailed},
		{"adjustment of another baseline", func(in *draftrank.BuildInput) {
			in.Baseline.SourceDataHash = "other"
		}, draftrank.IssueInternalError},
		{"unsupported roster slot", func(in *draftrank.BuildInput) {
			in.Rules.Rules.RosterSlots = append(in.Rules.Rules.RosterSlots, draft.RosterSlot{Position: "Util-X", Count: 1, Starting: true})
		}, draftrank.IssueRankingFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := fixtureInput(t)
			tt.mutate(&input)
			_, err := draftrank.Build(input)
			var coded *draftrank.RefreshError
			require.True(t, errors.As(err, &coded), "error %v has a code", err)
			assert.Equal(t, tt.code, coded.Code)
		})
	}
}

func TestBuild_MissingStatStopsInsteadOfGuessing(t *testing.T) {
	input := fixtureInput(t)
	input.Adjustment = nil
	for i := range input.Baseline.Players {
		if input.Baseline.Players[i].PlayerKey == draftfixtures.DepthCenter {
			delete(input.Baseline.Players[i].Values, projection.StatGoals)
		}
	}
	_, err := draftrank.Build(input)
	var coded *draftrank.RefreshError
	require.ErrorAs(t, err, &coded)
	assert.Equal(t, draftrank.IssueRankingFailed, coded.Code)
}

func TestSeasonID(t *testing.T) {
	assert.Equal(t, 20262027, draftrank.SeasonID(2026))
}

func TestLeagueOf_UnsupportedScoringStillDescribesLeague(t *testing.T) {
	rules := draftfixtures.Rules()
	rules.Rules.ScoringType = "vegas"
	league := draftrank.LeagueOf(rules)
	assert.Equal(t, draftfixtures.LeagueName, league.Name)
	assert.Empty(t, league.Format)
	assert.Equal(t, time.Date(2026, time.September, 20, 6, 0, 0, 0, time.UTC), league.FetchedAt)
}
