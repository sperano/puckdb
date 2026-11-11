package cmd

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/sperano/puckdb/internal/yahoolink"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envMauriceTeamsTestPGURL names the throwaway PostgreSQL this test migrates
// (see CLAUDE.md, "Database-backed tests"); unset skips it.

// newMauriceTeamsLeagueID returns a Yahoo league ID no fixture elsewhere in
// the test suite uses: tests share the database and never truncate it.
func newMauriceTeamsLeagueID() int32 { return 10_000_000 + rand.Int32N(1_000_000_000-10_000_000) }

func seedMauriceTeamsLeagueTeam(t *testing.T, pool *pgxpool.Pool, leagueID int32) {
	t.Helper()
	ctx := context.Background()
	key := fmt.Sprintf("465.l.%d", leagueID)
	_, err := pool.Exec(ctx, `INSERT INTO yahoo_leagues (id, league_key, name, season) VALUES ($1, $2, 'maurice teams test', 20262027)`, leagueID, key)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO yahoo_teams (league_id, id, team_key, name) VALUES ($1, 1, $2, $3)`,
		leagueID, key+".t.1", fmt.Sprintf("Team %d", leagueID))
	require.NoError(t, err)
}

// TestNewMauriceTeamLister_FiltersByAllowlist: the lister built from
// --maurice-yahoo-leagues (newMauriceTeamLister) forwards the allowlist to
// yahoolink.PGStore.ListUserTeams, so only an allowed league's team is
// named to Maurice.
func TestNewMauriceTeamLister_FiltersByAllowlist(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := yahoolink.NewPGStore(pool)

	allowed := newMauriceTeamsLeagueID()
	other := newMauriceTeamsLeagueID()
	seedMauriceTeamsLeagueTeam(t, pool, allowed)
	seedMauriceTeamsLeagueTeam(t, pool, other)

	var userID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO app_users (instance, auth_provider, auth_subject, username) VALUES ('maurice-teams-test', 'authentik', $1, 'u') RETURNING id`,
		fmt.Sprintf("sub-%d", allowed)).Scan(&userID))

	_, err := store.Link(ctx, userID, allowed, 1)
	require.NoError(t, err)
	_, err = store.Link(ctx, userID, other, 1)
	require.NoError(t, err)

	lister := newMauriceTeamLister(pool, []int32{allowed})
	teams, err := lister.ListUserTeams(ctx, uuid.UUID(userID.Bytes).String())
	require.NoError(t, err)
	require.Len(t, teams, 1)
	assert.Equal(t, allowed, teams[0].LeagueID)
	assert.Equal(t, fmt.Sprintf("Team %d", allowed), teams[0].TeamName)
}
