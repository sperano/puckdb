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

// TestSyncEmailLinks_EmptyEmailClearsExistingEmailLink: an empty login
// email (the identity header was blank this time) drops every email-sourced
// link an earlier, non-empty login created.
func TestSyncEmailLinks_EmptyEmailClearsExistingEmailLink(t *testing.T) {
	pool := openPool(t)
	q := sqlcdb.New(pool)
	ctx := context.Background()
	store := NewPGStore(pool)

	email := uniqueUsername("hadone") + "@example.com"
	league, team := seedLeagueTeam(t, pool, "Had One")
	seedManager(t, pool, league, team, 1, email)
	user := insertUser(t, pool, "hadone")
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))
	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 1)

	require.NoError(t, SyncEmailLinks(ctx, q, user, ""))
	links, err = store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	assert.Empty(t, links)
}

// TestSyncEmailLinks_ManualLinkNeverDemotedByMatchingResync: a manual link
// whose pair a manager email now also matches stays manual (ON CONFLICT DO
// NOTHING never promotes it to email), and still survives once that email
// no longer matches.
func TestSyncEmailLinks_ManualLinkNeverDemotedByMatchingResync(t *testing.T) {
	pool := openPool(t)
	q := sqlcdb.New(pool)
	ctx := context.Background()
	store := NewPGStore(pool)

	email := uniqueUsername("ownsit") + "@example.com"
	league, team := seedLeagueTeam(t, pool, "Owned Manually")
	seedManager(t, pool, league, team, 1, email)
	user := insertUser(t, pool, "ownsit")
	_, err := store.Link(ctx, user, league, team)
	require.NoError(t, err)

	require.NoError(t, SyncEmailLinks(ctx, q, user, email))
	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, SourceManual, links[0].Source, "a matching resync never demotes a manual link")

	require.NoError(t, SyncEmailLinks(ctx, q, user, uniqueUsername("someoneelse")+"@example.com"))
	links, err = store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, SourceManual, links[0].Source)
}

// TestSyncEmailLinks_StaleDeleteMatchesLeagueAndTeam: two teams in the same
// league must be told apart by team_id, not just league_id, when dropping a
// stale email-sourced link.
func TestSyncEmailLinks_StaleDeleteMatchesLeagueAndTeam(t *testing.T) {
	pool := openPool(t)
	q := sqlcdb.New(pool)
	ctx := context.Background()
	store := NewPGStore(pool)

	email := uniqueUsername("twoteams") + "@example.com"
	league, teamA := seedLeagueTeam(t, pool, "Team A")
	teamB := teamA + 1
	seedTeam(t, pool, league, teamB, "Team B")
	seedManager(t, pool, league, teamA, 1, email)
	seedManager(t, pool, league, teamB, 2, email)
	user := insertUser(t, pool, "twoteams")

	require.NoError(t, SyncEmailLinks(ctx, q, user, email))
	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 2)

	_, err = pool.Exec(ctx, `UPDATE yahoo_team_managers SET email = $1 WHERE league_id = $2 AND team_id = $3`,
		uniqueUsername("someoneelse")+"@example.com", league, teamB)
	require.NoError(t, err)

	require.NoError(t, SyncEmailLinks(ctx, q, user, email))
	links, err = store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 1, "only team B's link, in the same league as team A's, must be dropped")
	assert.Equal(t, teamA, links[0].TeamID)
}
