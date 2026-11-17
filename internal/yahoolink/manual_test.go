package yahoolink

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// TestPGStore_LinkClearsADeselectionOnTheGUIDBranch: link-team on a
// deselected team whose manager resolves to a guid links the account as
// manual and removes the deselection.
func TestPGStore_LinkClearsADeselectionOnTheGUIDBranch(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	guid := newGUID()
	league, team := seedLeagueTeam(t, pool, "Guid Team")
	seedManagerGUID(t, pool, league, team, 1, guid, "")
	user := insertUser(t, pool, "deselected")
	picker := newTestPicker(pool, testSeason, league)
	_, err := picker.SetTeams(ctx, userIDText(user), []TeamRef{{league, team}})
	require.NoError(t, err)
	_, err = picker.SetTeams(ctx, userIDText(user), nil)
	require.NoError(t, err)
	require.Equal(t, 1, countRejections(t, pool, user), "fixture: the team is deselected")

	link, err := store.Link(ctx, user, league, team)
	require.NoError(t, err)
	assert.Equal(t, guid, link.GUID, "the guid branch")
	assert.Equal(t, 0, countRejections(t, pool, user))
	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, Link{LeagueID: league, TeamID: team, GUID: guid, Source: SourceManual}, Link{
		LeagueID: links[0].LeagueID, TeamID: links[0].TeamID, GUID: links[0].GUID, Source: links[0].Source,
	})
}

// TestPGStore_LinkAndUnlinkWaitForTheUsersSelection: Link and Unlink take
// the user row lock SetTeams holds, so a deselection committed while they
// wait is seen, and cleared, instead of hiding the operator's change.
func TestPGStore_LinkAndUnlinkWaitForTheUsersSelection(t *testing.T) {
	pool := pgtest.Open(t)
	store := NewPGStore(pool)
	tests := []struct {
		name string
		op   func(ctx context.Context, user pgtype.UUID, league, team int32) (any, error)
		want any
	}{
		{name: "link", op: func(ctx context.Context, user pgtype.UUID, league, team int32) (any, error) {
			l, err := store.Link(ctx, user, league, team)
			return l.Source, err
		}, want: SourceManual},
		{name: "unlink", op: func(ctx context.Context, user pgtype.UUID, league, team int32) (any, error) {
			return store.Unlink(ctx, user, "", league, team)
		}, want: Unlinked{Rejection: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), lockWaitTimeout)
			defer cancel()
			league, team := seedLeagueTeam(t, pool, "Raced")
			seedManagerGUID(t, pool, league, team, 1, hiddenGUID, "")
			user := insertUser(t, pool, "raced")

			selection, err := pool.Begin(ctx)
			require.NoError(t, err)
			defer func() { _ = selection.Rollback(context.Background()) }()
			var pid int32
			require.NoError(t, selection.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid))
			q := sqlcdb.New(selection)
			require.NoError(t, markOfferSeen(ctx, q, user))
			require.NoError(t, rejectTeams(ctx, q, user, []teamKey{{league, team}}))

			type result struct {
				got any
				err error
			}
			done := make(chan result, 1)
			go func() {
				got, err := tt.op(ctx, user, league, team)
				done <- result{got, err}
			}()
			waitUntilBlockedBy(ctx, t, pool, pid, done)
			require.NoError(t, selection.Commit(ctx))

			got := <-done
			require.NoError(t, got.err)
			assert.Equal(t, tt.want, got.got)
			assert.Equal(t, 0, countRejections(t, pool, user), "the deselection committed meanwhile is cleared")
		})
	}
}

// TestPGStore_LinkAndUnlinkRefuseAMissingUser: both fail with
// ErrUserNotFound for an ID no app_users row has, changing nothing.
func TestPGStore_LinkAndUnlinkRefuseAMissingUser(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	league, team := seedLeagueTeam(t, pool, "Nobody's")
	seedManagerGUID(t, pool, league, team, 1, hiddenGUID, "")
	missing := pgtype.UUID{Bytes: [16]byte{0xde, 0xad, 0xbe, 0xef}, Valid: true}
	want := fmt.Sprintf("%s: id %s", ErrUserNotFound, missing)

	_, err := store.Link(ctx, missing, league, team)
	require.ErrorIs(t, err, ErrUserNotFound)
	assert.EqualError(t, err, want)
	_, err = store.Unlink(ctx, missing, "", league, team)
	require.ErrorIs(t, err, ErrUserNotFound)
	assert.EqualError(t, err, want)
}
