package draftrank_test

// PostgreSQL-backed test of the team-environment adjustment end to end: a
// skater whose league-season roster club differs from their history club
// gets a scaled projection and a ranking explanation. Skips unless
// PUCKDB_TEST_PG_URL names a test database (see CLAUDE.md "Database-backed
// tests").

import (
	"context"
	"strings"
	"testing"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The history club outscores and outshoots its opponent in every game, so a
// move from it to the opponent lowers the mover's team-dependent rates.
const (
	strongClubGoals = 5
	weakClubGoals   = 1
	strongClubShots = 40
	weakClubShots   = 20
)

// firstSkater returns the fixture pool's first skater and its NHL ID.
func firstSkater(t *testing.T) draft.PoolPlayer {
	t.Helper()
	for _, p := range draftfixtures.Pool() {
		if p.EligiblePositions[0] != draft.PositionGoalie {
			return p
		}
	}
	t.Fatal("fixture pool has no skater")
	return draft.PoolPlayer{}
}

func TestRefresher_StandInLeagueExplainsTeamChange(t *testing.T) {
	pool := openDraftTestDB(t)
	resetStandIn(t, pool)
	t.Cleanup(func() { resetStandIn(t, pool) })
	seedStandInRules(t, pool)
	seedHistory(t, pool)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `UPDATE games SET home_team_score = $1, away_team_score = $2,
		home_team_sog = $3, away_team_sog = $4 WHERE season = $5`,
		strongClubGoals, weakClubGoals, strongClubShots, weakClubShots, historySeason)
	require.NoError(t, err)

	mover := firstSkater(t)
	ownSeason := draftrank.SeasonID(draftfixtures.Season)
	for _, season := range []int{historySeason, ownSeason} {
		seedStandInSeasonTeam(t, pool, season, standInTeamID, standInTeamAbbrev)
		seedStandInSeasonTeam(t, pool, season, standInSecondTeamID, standInSecondTeamAbbrev)
	}
	for _, p := range draftfixtures.Pool() {
		team := int64(standInTeamID)
		if p.NHLPlayerID == mover.NHLPlayerID {
			team = standInSecondTeamID
		}
		insertStandInRoster(t, pool, ownSeason, team, p.NHLPlayerID, p.EligiblePositions[0])
	}

	outcome, err := refreshFixture(t, pool, refreshRunID)
	require.NoError(t, err, outcome.Error)
	require.Equal(t, draftrank.RefreshSucceeded, outcome.State, outcome.Error)
	snapshot, err := draftrank.NewPGStore(pool).LoadSnapshot(ctx, outcome.SnapshotID)
	require.NoError(t, err)

	assumptionContains(t, snapshot.Assumptions, "provisional pool from NHL 2026-27 rosters")
	assumptionContains(t, snapshot.Assumptions, "team environment: 1 skater(s) changed clubs for 2026-27")
	for _, p := range snapshot.Players {
		explained := teamExplanation(p.Placements[draftrank.ScenarioBaseline].Explanations)
		if p.NHLPlayerID != mover.NHLPlayerID {
			assert.Empty(t, explained, "%s did not change clubs", p.Name)
			continue
		}
		assert.Equal(t, standInSecondTeamAbbrev, p.Team)
		assert.Contains(t, explained, "team environment: EDM → TOR (100% of weighted history with other clubs)")
		assert.Contains(t, explained, "goals ×0.9", "a move to the weaker club lowers goals within the cap")
	}
}

func teamExplanation(explanations []string) string {
	for _, e := range explanations {
		if strings.HasPrefix(e, "team environment:") {
			return e
		}
	}
	return ""
}
