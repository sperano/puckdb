package yahoolink

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
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

// TestPGStore_UnlinkNotHeldPairWithMatchingEmailReturnsZeroUnlinked: Unlink checks
// whether the user holds the pair before checking the email match, so an
// unheld pair the login email matches still reports "nothing to remove",
// not ErrEmailLinkStillMatches.
func TestPGStore_UnlinkNotHeldPairWithMatchingEmailReturnsZeroUnlinked(t *testing.T) {
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

// TestPGStore_UnlinkRemovesAGUIDOnlyLink: a team Link reached through its
// account alone (no team link) unlinks that account, with its other
// season's team.
func TestPGStore_UnlinkRemovesAGUIDOnlyLink(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	guid := newGUID()
	league, team := seedLeagueTeam(t, pool, "Account Only")
	seedManagerGUID(t, pool, league, team, 1, guid, "")
	other, otherTeam := seedLeagueTeamInSeason(t, pool, "Account Only Other Season", "Vintage", 2025)
	seedManagerGUID(t, pool, other, otherTeam, 1, guid, "")
	user := insertUser(t, pool, "unlink-guid-only")
	_, err := store.Link(ctx, user, league, team)
	require.NoError(t, err)
	require.Equal(t, 0, countUserTeamLinks(t, pool, user), "Link made a guid link only")

	removed, err := store.Unlink(ctx, user, "", league, team)
	require.NoError(t, err)
	assert.Equal(t, Unlinked{GUIDs: []string{guid}}, removed)
	assert.True(t, removed.Removed())

	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	assert.Empty(t, links, "the other season's team went with the account")
}

// TestPGStore_UnlinkRefusesATeamTheEmailsAccountReaches: a team link is
// refused when the login email resolves to a guid one of the team's
// managers resolves to, even with no guid link held yet: the next login's
// sync would add that guid link, which reaches the team again.
func TestPGStore_UnlinkRefusesATeamTheEmailsAccountReaches(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	tests := []struct {
		name string
		// seed gives the team its managers; email is the login email, and
		// guid the account it resolves to.
		seed func(league, team int32, guid, email string)
	}{
		{name: "email on another league's row of a co-manager's account", seed: func(league, team int32, guid, email string) {
			seedManagerGUID(t, pool, league, team, 1, guid, "")
			seedManagerGUID(t, pool, league, team, 2, newGUID(), "")
			elsewhere, elsewhereTeam := seedLeagueTeamInSeason(t, pool, "Elsewhere", "Vintage", 2025)
			seedManagerGUID(t, pool, elsewhere, elsewhereTeam, 1, guid, email)
		}},
		{name: "email on the team's own resolved row", seed: func(league, team int32, guid, email string) {
			seedManagerGUID(t, pool, league, team, 1, guid, email)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guid := newGUID()
			email := uniqueUsername("reaches") + "@example.com"
			league, team := seedLeagueTeam(t, pool, "Reached")
			tt.seed(league, team, guid, email)
			user := insertUser(t, pool, "reaches")
			_, err := pool.Exec(ctx, `INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source) VALUES ($1, $2, $3, 'manual')`,
				user, league, team)
			require.NoError(t, err)

			_, err = store.Unlink(ctx, user, email, league, team)
			require.ErrorIs(t, err, ErrEmailLinkStillMatches)
			assert.Equal(t, 1, countUserTeamLinks(t, pool, user), "the refused unlink changed nothing")
		})
	}
}

// lockWaitTimeout bounds how long a test waits for a statement to block on
// another transaction's lock.
const lockWaitTimeout = 30 * time.Second

// TestPGStore_UnlinkReportsOnlyTheGUIDLinksItDeleted: a guid link another
// transaction deletes while Unlink waits on its row lock is not reported
// as removed by this call.
func TestPGStore_UnlinkReportsOnlyTheGUIDLinksItDeleted(t *testing.T) {
	pool := pgtest.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), lockWaitTimeout)
	defer cancel()
	store := NewPGStore(pool)
	guid := newGUID()
	league, team := seedLeagueTeam(t, pool, "Raced")
	seedManagerGUID(t, pool, league, team, 1, guid, "")
	user := insertUser(t, pool, "raced")
	_, err := store.Link(ctx, user, league, team)
	require.NoError(t, err)

	other, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = other.Rollback(context.Background()) }()
	var otherPID int32
	require.NoError(t, other.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&otherPID))
	_, err = other.Exec(ctx, `DELETE FROM app_user_yahoo_managers WHERE user_id = $1`, user)
	require.NoError(t, err)

	type result struct {
		removed Unlinked
		err     error
	}
	done := make(chan result, 1)
	go func() {
		removed, err := store.Unlink(ctx, user, "", league, team)
		done <- result{removed, err}
	}()
	waitUntilBlockedBy(ctx, t, pool, otherPID, done)
	require.NoError(t, other.Commit(ctx))

	got := <-done
	require.NoError(t, got.err)
	assert.Equal(t, Unlinked{}, got.removed)
}

// waitUntilBlockedBy returns once some backend waits on a lock pid holds,
// failing the test if Unlink's done channel delivers first or ctx ends.
func waitUntilBlockedBy[T any](ctx context.Context, t *testing.T, pool *pgxpool.Pool, pid int32, done <-chan T) {
	t.Helper()
	for {
		select {
		case got := <-done:
			t.Fatalf("returned before blocking on the other transaction: %+v", got)
		default:
		}
		var blocked bool
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1::integer = ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked))
		if blocked {
			return
		}
	}
}

// TestPGStore_UnlinkFailureRemovesNothing: when the guid links' delete
// fails, the team link deleted before it is rolled back too.
func TestPGStore_UnlinkFailureRemovesNothing(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	guid := newGUID()
	league, team := seedLeagueTeam(t, pool, "Half Unlinked")
	seedManagerGUID(t, pool, league, team, 1, guid, "")
	user := insertUser(t, pool, "half-unlinked")
	_, err := store.Link(ctx, user, league, team)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source) VALUES ($1, $2, $3, 'manual')`,
		user, league, team)
	require.NoError(t, err)
	failGUIDLinkDeletes(t, pool, user)

	removed, err := store.Unlink(ctx, user, "", league, team)
	require.ErrorContains(t, err, "unlink yahoo accounts")
	assert.Equal(t, Unlinked{}, removed)
	assert.Equal(t, 1, countUserTeamLinks(t, pool, user), "the team link delete was rolled back")
	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, guid, links[0].GUID, "the guid link is still there")
}

// failGUIDLinkDeletes installs, until the test ends, a trigger that makes
// any delete of userID's app_user_yahoo_managers rows raise an error.
func failGUIDLinkDeletes(t *testing.T, pool *pgxpool.Pool, userID pgtype.UUID) {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("yahoolink_test_fail_delete_%d", newLeagueID())
	uid, err := userID.Value()
	require.NoError(t, err)
	_, err = pool.Exec(ctx, fmt.Sprintf(`
CREATE FUNCTION %[1]s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.user_id = '%[2]s'::uuid THEN RAISE EXCEPTION 'injected delete failure'; END IF;
    RETURN OLD;
END $$;
CREATE TRIGGER %[1]s BEFORE DELETE ON app_user_yahoo_managers FOR EACH ROW EXECUTE FUNCTION %[1]s();`, name, uid))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER %[1]s ON app_user_yahoo_managers; DROP FUNCTION %[1]s();`, name))
		require.NoError(t, err)
	})
}
