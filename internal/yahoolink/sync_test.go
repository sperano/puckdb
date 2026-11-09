package yahoolink

import (
	"context"
	"strings"
	"testing"

	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSyncEmailLinks_MatchesSeveralTeamsCaseInsensitively: a user who
// manages several teams (the owner's three pools) gets every one of them
// linked at once, matched against a differently-cased stored email.
func TestSyncEmailLinks_MatchesSeveralTeamsCaseInsensitively(t *testing.T) {
	pool := openPool(t)
	q := sqlcdb.New(pool)
	ctx := context.Background()

	email := uniqueUsername("owner") + "@example.com"
	league1, team1 := seedLeagueTeam(t, pool, "Les Peter Stastny")
	league2, team2 := seedLeagueTeam(t, pool, "Second Pool")
	seedManager(t, pool, league1, team1, 1, strings.ToUpper(email))
	seedManager(t, pool, league2, team2, 1, email)
	user := insertUser(t, pool, "owner")

	require.NoError(t, SyncEmailLinks(ctx, q, user, strings.ToUpper(email)))

	store := NewPGStore(pool)
	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 2)
	for _, l := range links {
		assert.Equal(t, SourceEmail, l.Source)
	}
}

// TestSyncEmailLinks_ManualLinkSurvivesAResync: a resync under an email that
// matches nothing never removes a manually-added link, and never promotes
// one made by email either way.
func TestSyncEmailLinks_ManualLinkSurvivesAResync(t *testing.T) {
	pool := openPool(t)
	q := sqlcdb.New(pool)
	ctx := context.Background()

	email := uniqueUsername("friend") + "@example.com"
	manualLeague, manualTeam := seedLeagueTeam(t, pool, "Manual Team")
	emailLeague, emailTeam := seedLeagueTeam(t, pool, "Email Team")
	seedManager(t, pool, emailLeague, emailTeam, 1, email)
	user := insertUser(t, pool, "friend")

	store := NewPGStore(pool)
	_, err := store.Link(ctx, user, manualLeague, manualTeam)
	require.NoError(t, err)
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))

	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 2)

	// The manager row's email changes (or the user's login email does): the
	// email-sourced link is dropped, the manual one is not.
	require.NoError(t, SyncEmailLinks(ctx, q, user, uniqueUsername("someoneelse")+"@example.com"))
	links, err = store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, manualTeam, links[0].TeamID)
	assert.Equal(t, SourceManual, links[0].Source)
}

// TestSyncEmailLinks_EmptyEmailMatchesNothing: yahoo_team_managers.email
// defaults to ”, which must never match an empty login email.
func TestSyncEmailLinks_EmptyEmailMatchesNothing(t *testing.T) {
	pool := openPool(t)
	q := sqlcdb.New(pool)
	ctx := context.Background()
	league, team := seedLeagueTeam(t, pool, "No Email")
	seedManager(t, pool, league, team, 1, "")
	user := insertUser(t, pool, "noemail")

	require.NoError(t, SyncEmailLinks(ctx, q, user, ""))

	store := NewPGStore(pool)
	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	assert.Empty(t, links)
}
