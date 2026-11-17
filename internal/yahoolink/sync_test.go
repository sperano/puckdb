package yahoolink

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSyncEmailLinks_MatchesSeveralTeamsCaseInsensitively: a user who
// manages several teams (the owner's three pools) gets every one of them
// linked at once, matched against a differently-cased stored email.
func TestSyncEmailLinks_MatchesSeveralTeamsCaseInsensitively(t *testing.T) {
	pool := pgtest.Open(t)
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
	pool := pgtest.Open(t)
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
	pool := pgtest.Open(t)
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
	pool := pgtest.Open(t)
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
	pool := pgtest.Open(t)
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
	pool := pgtest.Open(t)
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

// TestSyncEmailLinks_LinksTheRecoveredAccount: the login email matches the
// 2025 row, with the account's real guid, and the current-season row, whose
// guid is hidden; the 2019 row carries the same guid under another email.
// The account is linked once, giving the user all three teams, the hidden
// one included.
func TestSyncEmailLinks_LinksTheRecoveredAccount(t *testing.T) {
	pool := pgtest.Open(t)
	q := sqlcdb.New(pool)
	ctx := context.Background()
	store := NewPGStore(pool)
	guid := newGUID()
	email := uniqueUsername("recovered") + "@example.com"
	past, pastTeam := seedLeagueTeamInSeason(t, pool, "Last Year", "Vintage", 2025)
	seedManagerGUID(t, pool, past, pastTeam, 1, guid, email)
	older, olderTeam := seedLeagueTeamInSeason(t, pool, "Long Ago", "Old Pool", 2019)
	seedManagerGUID(t, pool, older, olderTeam, 1, guid, uniqueUsername("oldaddress")+"@example.com")
	current, currentTeam := seedLeagueTeamInSeason(t, pool, "This Year", "Crapettes", 2026)
	seedManagerGUID(t, pool, current, currentTeam, 1, hiddenGUID, email)
	user := insertUser(t, pool, "recovered")

	require.NoError(t, SyncEmailLinks(ctx, q, user, email))

	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	assert.ElementsMatch(t, []teamKey{{past, pastTeam}, {older, olderTeam}, {current, currentTeam}}, teamKeys(links))
	for _, l := range links {
		assert.Equal(t, guid, l.GUID)
		assert.Equal(t, SourceEmail, l.Source)
	}
	assert.Equal(t, 0, countUserTeamLinks(t, pool, user), "a resolved row makes no team link")
}

// TestSyncEmailLinks_SplitsResolvedAndUnresolvedRows: an email whose rows
// carry two accounts links both, and its hidden-guid row, which the email
// cannot resolve to either, by team.
func TestSyncEmailLinks_SplitsResolvedAndUnresolvedRows(t *testing.T) {
	pool := pgtest.Open(t)
	q := sqlcdb.New(pool)
	ctx := context.Background()
	store := NewPGStore(pool)
	guid1, guid2 := newGUID(), newGUID()
	email := uniqueUsername("split") + "@example.com"
	first, firstTeam := seedLeagueTeam(t, pool, "First Account")
	seedManagerGUID(t, pool, first, firstTeam, 1, guid1, email)
	second, secondTeam := seedLeagueTeam(t, pool, "Second Account")
	seedManagerGUID(t, pool, second, secondTeam, 1, guid2, email)
	hidden, hiddenTeam := seedLeagueTeam(t, pool, "Hidden")
	seedManagerGUID(t, pool, hidden, hiddenTeam, 1, hiddenGUID, email)
	user := insertUser(t, pool, "split")

	require.NoError(t, SyncEmailLinks(ctx, q, user, email))

	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	got := map[teamKey]string{}
	for _, l := range links {
		got[teamKey{l.LeagueID, l.TeamID}] = l.GUID
	}
	assert.Equal(t, map[teamKey]string{
		{first, firstTeam}: guid1, {second, secondTeam}: guid2, {hidden, hiddenTeam}: "",
	}, got)
}

// TestSyncEmailLinks_TeamLinkGivesWayToTheAccount: an email-sourced team
// link made while its row had no recoverable guid is replaced by the guid
// link once an earlier season with that email and a real guid is imported.
func TestSyncEmailLinks_TeamLinkGivesWayToTheAccount(t *testing.T) {
	pool := pgtest.Open(t)
	q := sqlcdb.New(pool)
	ctx := context.Background()
	store := NewPGStore(pool)
	email := uniqueUsername("backfilled") + "@example.com"
	current, currentTeam := seedLeagueTeam(t, pool, "Hidden Now")
	seedManagerGUID(t, pool, current, currentTeam, 1, hiddenGUID, email)
	user := insertUser(t, pool, "backfilled")
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))
	require.Equal(t, 1, countUserTeamLinks(t, pool, user))

	guid := newGUID()
	past, pastTeam := seedLeagueTeamInSeason(t, pool, "Imported Later", "Vintage", 2025)
	seedManagerGUID(t, pool, past, pastTeam, 1, guid, email)
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))

	assert.Equal(t, 0, countUserTeamLinks(t, pool, user), "the stale team link is gone")
	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	assert.ElementsMatch(t, []teamKey{{current, currentTeam}, {past, pastTeam}}, teamKeys(links))
	for _, l := range links {
		assert.Equal(t, guid, l.GUID)
	}
}

// TestSyncEmailLinks_StaleGUIDLinkDroppedManualKept: once the email no
// longer resolves to an account, its email-sourced guid link goes; a manual
// guid link stays.
func TestSyncEmailLinks_StaleGUIDLinkDroppedManualKept(t *testing.T) {
	pool := pgtest.Open(t)
	q := sqlcdb.New(pool)
	ctx := context.Background()
	store := NewPGStore(pool)
	email := uniqueUsername("staleguid") + "@example.com"
	emailGUID, manualGUID := newGUID(), newGUID()
	emailLeague, emailTeam := seedLeagueTeam(t, pool, "By Email")
	seedManagerGUID(t, pool, emailLeague, emailTeam, 1, emailGUID, email)
	manualLeague, manualTeam := seedLeagueTeam(t, pool, "By Hand")
	seedManagerGUID(t, pool, manualLeague, manualTeam, 1, manualGUID, "")
	user := insertUser(t, pool, "staleguid")
	_, err := store.Link(ctx, user, manualLeague, manualTeam)
	require.NoError(t, err)
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))
	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 2)

	require.NoError(t, SyncEmailLinks(ctx, q, user, uniqueUsername("someoneelse")+"@example.com"))
	links, err = store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, manualGUID, links[0].GUID)
	assert.Equal(t, SourceManual, links[0].Source)
}

// TestSyncEmailLinks_KeepsAManualGUIDLinkTheEmailMatches: a resync whose
// email resolves to a guid the user holds by hand leaves that link manual,
// with its linked_at, so a resync under another email still keeps it.
func TestSyncEmailLinks_KeepsAManualGUIDLinkTheEmailMatches(t *testing.T) {
	pool := pgtest.Open(t)
	q := sqlcdb.New(pool)
	ctx := context.Background()
	store := NewPGStore(pool)
	guid := newGUID()
	email := uniqueUsername("manualguid") + "@example.com"
	league, team := seedLeagueTeam(t, pool, "Manual Account")
	seedManagerGUID(t, pool, league, team, 1, guid, email)
	user := insertUser(t, pool, "manualguid")
	link, err := store.Link(ctx, user, league, team)
	require.NoError(t, err)
	require.Equal(t, guid, link.GUID)

	require.NoError(t, SyncEmailLinks(ctx, q, user, email))
	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, SourceManual, links[0].Source)
	assert.True(t, link.LinkedAt.Equal(links[0].LinkedAt), "linked_at kept: %s, was %s", links[0].LinkedAt, link.LinkedAt)

	require.NoError(t, SyncEmailLinks(ctx, q, user, uniqueUsername("someoneelse")+"@example.com"))
	links, err = store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, guid, links[0].GUID)
	assert.Equal(t, SourceManual, links[0].Source)
}

// countUserTeamLinks counts userID's app_user_yahoo_teams rows: team links
// only, including those ListUserTeams hides behind a guid link.
func countUserTeamLinks(t *testing.T, pool *pgxpool.Pool, userID pgtype.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM app_user_yahoo_teams WHERE user_id = $1`, userID).Scan(&n))
	return n
}
