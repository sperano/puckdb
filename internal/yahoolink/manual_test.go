package yahoolink

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPGStore_LinkAndUnlink(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	league, team := seedLeagueTeam(t, pool, "Manual Add")
	user := insertUser(t, pool, "admin-add")

	link, err := store.Link(ctx, user, league, team)
	require.NoError(t, err)
	assert.Equal(t, SourceManual, link.Source)
	assert.Empty(t, link.GUID, "a team with no manager is linked by itself")

	removed, err := store.Unlink(ctx, user, "", league, team)
	require.NoError(t, err)
	assert.Equal(t, Unlinked{Team: true}, removed)

	removed, err = store.Unlink(ctx, user, "", league, team)
	require.NoError(t, err)
	assert.Equal(t, Unlinked{}, removed, "a second unlink finds nothing")
}

// TestPGStore_UnlinkRefusesEmailSourcedLinkStillMatched: unlinking a team
// SyncEmailLinks would just recreate on the user's next login is refused,
// not silently undone (the blocking bug: unlink-team on an email-matched
// team was undone by the next request).
func TestPGStore_UnlinkRefusesEmailSourcedLinkStillMatched(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	q := sqlcdb.New(pool)
	store := NewPGStore(pool)

	email := uniqueUsername("stays") + "@example.com"
	league, team := seedLeagueTeam(t, pool, "Stays Linked")
	seedManager(t, pool, league, team, 1, email)
	user := insertUser(t, pool, "stays-linked")
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))

	_, err := store.Unlink(ctx, user, email, league, team)
	require.ErrorIs(t, err, ErrEmailLinkStillMatches)

	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 1, "the refused unlink changed nothing")
}

// TestPGStore_UnlinkRemovesEmailSourcedLinkOnceUnmatched: once the login
// email no longer matches, the same email-sourced link unlinks normally.
func TestPGStore_UnlinkRemovesEmailSourcedLinkOnceUnmatched(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	q := sqlcdb.New(pool)
	store := NewPGStore(pool)

	email := uniqueUsername("leaves") + "@example.com"
	league, team := seedLeagueTeam(t, pool, "Leaves Linked")
	seedManager(t, pool, league, team, 1, email)
	user := insertUser(t, pool, "leaves-linked")
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))

	removed, err := store.Unlink(ctx, user, uniqueUsername("someoneelse")+"@example.com", league, team)
	require.NoError(t, err)
	assert.Equal(t, Unlinked{Team: true}, removed)

	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	assert.Empty(t, links)
}

// TestPGStore_LinkPromotesEmailLinkToManual: an operator's Link on an
// already email-linked pair promotes it to manual, so a later resync under
// an email that no longer matches never removes it.
func TestPGStore_LinkPromotesEmailLinkToManual(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	q := sqlcdb.New(pool)
	store := NewPGStore(pool)

	email := uniqueUsername("promote") + "@example.com"
	league, team := seedLeagueTeam(t, pool, "Promote Me")
	seedManager(t, pool, league, team, 1, email)
	user := insertUser(t, pool, "promote-me")
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))

	link, err := store.Link(ctx, user, league, team)
	require.NoError(t, err)
	assert.Equal(t, SourceManual, link.Source)

	require.NoError(t, SyncEmailLinks(ctx, q, user, uniqueUsername("someoneelse")+"@example.com"))
	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, SourceManual, links[0].Source)
}

// TestPGStore_UnlinkRefusesManualLinkStillMatched: a manual link (an
// operator's promotion of a matching email link, here) is refused the same
// as an email-sourced one, since SyncEmailLinks would otherwise recreate it
// as SourceEmail on the user's next authenticated request, undoing the
// unlink.
func TestPGStore_UnlinkRefusesManualLinkStillMatched(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	q := sqlcdb.New(pool)
	store := NewPGStore(pool)

	email := uniqueUsername("manual-stays") + "@example.com"
	league, team := seedLeagueTeam(t, pool, "Manual Stays Linked")
	seedManager(t, pool, league, team, 1, email)
	user := insertUser(t, pool, "manual-stays-linked")
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))

	link, err := store.Link(ctx, user, league, team)
	require.NoError(t, err)
	require.Equal(t, SourceManual, link.Source)

	_, err = store.Unlink(ctx, user, email, league, team)
	require.ErrorIs(t, err, ErrEmailLinkStillMatches)

	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 1, "the refused unlink changed nothing")
	assert.Equal(t, SourceManual, links[0].Source)
}

// TestPGStore_UnlinkOtherTeamNotRefusedByEmailMatch: the still-matches
// check compares both LeagueID and TeamID, not just one of them: a second
// team in the same league the login email does not manage unlinks
// normally even though the email matches team 1 in that league.
func TestPGStore_UnlinkOtherTeamNotRefusedByEmailMatch(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)

	email := uniqueUsername("other-team") + "@example.com"
	league, matchedTeam := seedLeagueTeam(t, pool, "Matched Team")
	otherTeam := int32(2)
	seedTeam(t, pool, league, otherTeam, "Other Team")
	seedManager(t, pool, league, matchedTeam, 1, email)
	user := insertUser(t, pool, "other-team-user")

	_, err := store.Link(ctx, user, league, otherTeam)
	require.NoError(t, err)

	removed, err := store.Unlink(ctx, user, email, league, otherTeam)
	require.NoError(t, err)
	assert.Equal(t, Unlinked{Team: true}, removed, "a different team in the same league is not refused by the email match on team 1")
}

// TestPGStore_UnlinkNotHeldPairWithMatchingEmailReturnsFalse: Unlink checks
// whether the user holds the pair before checking the email match, so an
// unheld pair the login email matches still reports "nothing to remove",
// not ErrEmailLinkStillMatches.
func TestPGStore_UnlinkNotHeldPairWithMatchingEmailReturnsFalse(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)

	email := uniqueUsername("not-held") + "@example.com"
	league, team := seedLeagueTeam(t, pool, "Not Held")
	seedManager(t, pool, league, team, 1, email)
	user := insertUser(t, pool, "not-held-user")

	removed, err := store.Unlink(ctx, user, email, league, team)
	require.NoError(t, err)
	assert.Equal(t, Unlinked{}, removed)
}

// TestPGStore_UnlinkSameTeamIDOtherLeagueNotRefusedByEmailMatch: the
// still-matches check compares LeagueID and TeamID together, not TeamID
// alone, which would wrongly collide across leagues (team IDs are unique
// only within a league): a link to team 1 in league B unlinks normally even
// though the email matches team 1 in a different league A.
func TestPGStore_UnlinkSameTeamIDOtherLeagueNotRefusedByEmailMatch(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)

	email := uniqueUsername("cross-league") + "@example.com"
	leagueA, teamA := seedLeagueTeam(t, pool, "League A Team")
	seedManager(t, pool, leagueA, teamA, 1, email)
	leagueB, teamB := seedLeagueTeam(t, pool, "League B Team")
	require.Equal(t, teamA, teamB, "both leagues must assign the same team id for this test to be meaningful")
	user := insertUser(t, pool, "cross-league-user")

	_, err := store.Link(ctx, user, leagueB, teamB)
	require.NoError(t, err)

	removed, err := store.Unlink(ctx, user, email, leagueB, teamB)
	require.NoError(t, err)
	assert.Equal(t, Unlinked{Team: true}, removed, "a matching team id in a different league is not refused by the email match in league A")
}

func TestPGStore_LinkRefusesUnknownTeam(t *testing.T) {
	pool := pgtest.Open(t)
	store := NewPGStore(pool)
	user := insertUser(t, pool, "admin-unknown")

	_, err := store.Link(context.Background(), user, newLeagueID(), 999)
	require.ErrorIs(t, err, ErrUnknownTeam)
}

// TestSingleGUID: only a team whose every manager resolves to one and the
// same guid is linked by guid.
func TestSingleGUID(t *testing.T) {
	manager := func(id int32, guid string) sqlcdb.ListYahooTeamManagerGUIDsRow {
		return sqlcdb.ListYahooTeamManagerGUIDsRow{
			ManagerID: pgtype.Int4{Int32: id, Valid: true},
			Guid:      pgtype.Text{String: guid, Valid: guid != ""},
		}
	}
	tests := []struct {
		name     string
		managers []sqlcdb.ListYahooTeamManagerGUIDsRow
		want     string
		wantOK   bool
	}{
		{name: "one resolved manager", managers: []sqlcdb.ListYahooTeamManagerGUIDsRow{manager(1, "G1")}, want: "G1", wantOK: true},
		{name: "co-managers of one account", managers: []sqlcdb.ListYahooTeamManagerGUIDsRow{manager(1, "G1"), manager(2, "G1")}, want: "G1", wantOK: true},
		{name: "co-managers of two accounts", managers: []sqlcdb.ListYahooTeamManagerGUIDsRow{manager(1, "G1"), manager(2, "G2")}},
		{name: "a co-manager with no guid", managers: []sqlcdb.ListYahooTeamManagerGUIDsRow{manager(1, "G1"), manager(2, "")}},
		{name: "unresolved manager", managers: []sqlcdb.ListYahooTeamManagerGUIDsRow{manager(1, "")}},
		{name: "team with no manager", managers: []sqlcdb.ListYahooTeamManagerGUIDsRow{{}}},
		{name: "no row", managers: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := singleGUID(tt.managers)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantOK, ok)
		})
	}
}

// TestPGStore_LinkLinksTheAccountAcrossSeasons: linking a team whose
// manager has a real guid links the account, which gives the user its
// other seasons' teams too: one under another email, and the current
// season's, whose hidden guid the shared email recovers.
func TestPGStore_LinkLinksTheAccountAcrossSeasons(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	guid := newGUID()
	email := uniqueUsername("returning") + "@example.com"
	past, pastTeam := seedLeagueTeamInSeason(t, pool, "Last Year", "Vintage", 2025)
	seedManagerGUID(t, pool, past, pastTeam, 1, guid, email)
	older, olderTeam := seedLeagueTeamInSeason(t, pool, "Long Ago", "Old Pool", 2019)
	seedManagerGUID(t, pool, older, olderTeam, 1, guid, uniqueUsername("oldaddress")+"@example.com")
	current, currentTeam := seedLeagueTeamInSeason(t, pool, "This Year", "Crapettes", 2026)
	seedManagerGUID(t, pool, current, currentTeam, 1, hiddenGUID, strings.ToUpper(email))
	user := insertUser(t, pool, "returning")

	link, err := store.Link(ctx, user, past, pastTeam)
	require.NoError(t, err)
	assert.Equal(t, Link{LeagueID: past, TeamID: pastTeam, GUID: guid, Source: SourceManual, LinkedAt: link.LinkedAt}, link)

	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	assert.ElementsMatch(t, []teamKey{{past, pastTeam}, {older, olderTeam}, {current, currentTeam}}, teamKeys(links))
	for _, l := range links {
		assert.Equal(t, guid, l.GUID)
		assert.Equal(t, SourceManual, l.Source)
	}
}

// TestPGStore_GUIDLinkPicksUpANewSeason: a season imported after the link
// needs no new link.
func TestPGStore_GUIDLinkPicksUpANewSeason(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	guid := newGUID()
	email := uniqueUsername("newseason") + "@example.com"
	past, pastTeam := seedLeagueTeamInSeason(t, pool, "Last Year", "Vintage", 2025)
	seedManagerGUID(t, pool, past, pastTeam, 1, guid, email)
	user := insertUser(t, pool, "newseason")
	_, err := store.Link(ctx, user, past, pastTeam)
	require.NoError(t, err)

	next, nextTeam := seedLeagueTeamInSeason(t, pool, "Next Year", "Crapettes", 2026)
	seedManagerGUID(t, pool, next, nextTeam, 3, hiddenGUID, email)

	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	assert.ElementsMatch(t, []teamKey{{past, pastTeam}, {next, nextTeam}}, teamKeys(links))
}

// TestPGStore_LinkFallsBackToTheTeam: a team whose managers resolve to no
// single guid is linked by itself, and gives the user no other team: a
// hidden or deleted guid with no email to recover it from, an email whose
// older rows carry two guids, and a team co-managed by two accounts.
func TestPGStore_LinkFallsBackToTheTeam(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)

	ambiguousEmail := uniqueUsername("twoaccounts") + "@example.com"
	for _, guid := range []string{newGUID(), newGUID()} {
		l, tm := seedLeagueTeamInSeason(t, pool, "Earlier", "Vintage", 2024)
		seedManagerGUID(t, pool, l, tm, 1, guid, ambiguousEmail)
	}
	coManagerGUID := newGUID()
	elsewhere, elsewhereTeam := seedLeagueTeamInSeason(t, pool, "Co-manager's other team", "Vintage", 2024)
	seedManagerGUID(t, pool, elsewhere, elsewhereTeam, 1, coManagerGUID, "")

	tests := []struct {
		name string
		seed func(league, team int32)
	}{
		{name: "hidden guid, unknown email", seed: func(l, tm int32) {
			seedManagerGUID(t, pool, l, tm, 1, hiddenGUID, uniqueUsername("newcomer")+"@example.com")
		}},
		{name: "hidden guid, no email", seed: func(l, tm int32) { seedManagerGUID(t, pool, l, tm, 1, hiddenGUID, "") }},
		{name: "deleted account", seed: func(l, tm int32) { seedManagerGUID(t, pool, l, tm, 1, "--", "") }},
		{name: "empty guid", seed: func(l, tm int32) { seedManagerGUID(t, pool, l, tm, 1, "", "") }},
		{name: "email with two guids", seed: func(l, tm int32) { seedManagerGUID(t, pool, l, tm, 1, hiddenGUID, ambiguousEmail) }},
		{name: "co-managed by two accounts", seed: func(l, tm int32) {
			seedManagerGUID(t, pool, l, tm, 1, newGUID(), "")
			seedManagerGUID(t, pool, l, tm, 2, coManagerGUID, "")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			league, team := seedLeagueTeam(t, pool, "Fallback")
			tt.seed(league, team)
			user := insertUser(t, pool, "fallback")

			link, err := store.Link(ctx, user, league, team)
			require.NoError(t, err)
			assert.Equal(t, Link{LeagueID: league, TeamID: team, Source: SourceManual, LinkedAt: link.LinkedAt}, link)

			links, err := store.ListUserTeams(ctx, user, nil)
			require.NoError(t, err)
			assert.Equal(t, []teamKey{{league, team}}, teamKeys(links))
			assert.Empty(t, links[0].GUID)
		})
	}
}

// TestPGStore_LinkPromotesEmailGUIDLinkToManual: an operator's Link on a
// team whose account the user already holds by email makes that guid link
// manual, so a resync under another email keeps it.
func TestPGStore_LinkPromotesEmailGUIDLinkToManual(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	q := sqlcdb.New(pool)
	store := NewPGStore(pool)
	guid := newGUID()
	email := uniqueUsername("promoted") + "@example.com"
	league, team := seedLeagueTeam(t, pool, "Promoted")
	seedManagerGUID(t, pool, league, team, 1, guid, email)
	user := insertUser(t, pool, "promoted")
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))

	link, err := store.Link(ctx, user, league, team)
	require.NoError(t, err)
	assert.Equal(t, SourceManual, link.Source)
	assert.Equal(t, guid, link.GUID)

	require.NoError(t, SyncEmailLinks(ctx, q, user, uniqueUsername("someoneelse")+"@example.com"))
	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, SourceManual, links[0].Source)
	assert.Equal(t, guid, links[0].GUID)
}

// TestPGStore_UnlinkRemovesTheAccount: unlinking a team reached through a
// guid link removes that link, so every team of the account goes, and a
// team link to the same team goes with it.
func TestPGStore_UnlinkRemovesTheAccount(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	guid := newGUID()
	league, team := seedLeagueTeam(t, pool, "Account Team")
	seedManagerGUID(t, pool, league, team, 1, guid, "")
	other, otherTeam := seedLeagueTeamInSeason(t, pool, "Account Other Season", "Vintage", 2025)
	seedManagerGUID(t, pool, other, otherTeam, 1, guid, "")
	user := insertUser(t, pool, "unlink-account")
	_, err := store.Link(ctx, user, league, team)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source) VALUES ($1, $2, $3, 'manual')`,
		user, league, team)
	require.NoError(t, err)

	removed, err := store.Unlink(ctx, user, "", league, team)
	require.NoError(t, err)
	assert.Equal(t, Unlinked{Team: true, GUIDs: []string{guid}}, removed)

	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	assert.Empty(t, links)
}

// TestPGStore_UnlinkRefusesAccountTheEmailStillResolves: the login email
// matches only another season's manager row of the account, yet a resync
// would recreate the guid link, so unlinking this team is refused.
func TestPGStore_UnlinkRefusesAccountTheEmailStillResolves(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	q := sqlcdb.New(pool)
	store := NewPGStore(pool)
	guid := newGUID()
	email := uniqueUsername("resolves") + "@example.com"
	league, team := seedLeagueTeamInSeason(t, pool, "Old Address", "Vintage", 2019)
	seedManagerGUID(t, pool, league, team, 1, guid, uniqueUsername("oldaddress")+"@example.com")
	current, currentTeam := seedLeagueTeam(t, pool, "Current Address")
	seedManagerGUID(t, pool, current, currentTeam, 1, guid, email)
	user := insertUser(t, pool, "resolves")
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))

	_, err := store.Unlink(ctx, user, email, league, team)
	require.ErrorIs(t, err, ErrEmailLinkStillMatches)

	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	assert.Len(t, links, 2, "the refused unlink changed nothing")
}
