package yahoolink

import (
	"context"
	"testing"

	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPGStore_LinkAndUnlink(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	league, team := seedLeagueTeam(t, pool, "Manual Add")
	user := insertUser(t, pool, "admin-add")

	link, err := store.Link(ctx, user, league, team)
	require.NoError(t, err)
	assert.Equal(t, SourceManual, link.Source)

	removed, err := store.Unlink(ctx, user, "", league, team)
	require.NoError(t, err)
	assert.True(t, removed)

	removed, err = store.Unlink(ctx, user, "", league, team)
	require.NoError(t, err)
	assert.False(t, removed, "a second unlink finds nothing")
}

// TestPGStore_UnlinkRefusesEmailSourcedLinkStillMatched: unlinking a team
// SyncEmailLinks would just recreate on the user's next login is refused,
// not silently undone (the blocking bug: unlink-team on an email-matched
// team was undone by the next request).
func TestPGStore_UnlinkRefusesEmailSourcedLinkStillMatched(t *testing.T) {
	pool := openPool(t)
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
	pool := openPool(t)
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
	assert.True(t, removed)

	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	assert.Empty(t, links)
}

// TestPGStore_LinkPromotesEmailLinkToManual: an operator's Link on an
// already email-linked pair promotes it to manual, so a later resync under
// an email that no longer matches never removes it.
func TestPGStore_LinkPromotesEmailLinkToManual(t *testing.T) {
	pool := openPool(t)
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
	pool := openPool(t)
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
	pool := openPool(t)
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
	assert.True(t, removed, "a different team in the same league is not refused by the email match on team 1")
}

// TestPGStore_UnlinkNotHeldPairWithMatchingEmailReturnsFalse: Unlink checks
// whether the user holds the pair before checking the email match, so an
// unheld pair the login email matches still reports "nothing to remove",
// not ErrEmailLinkStillMatches.
func TestPGStore_UnlinkNotHeldPairWithMatchingEmailReturnsFalse(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()
	store := NewPGStore(pool)

	email := uniqueUsername("not-held") + "@example.com"
	league, team := seedLeagueTeam(t, pool, "Not Held")
	seedManager(t, pool, league, team, 1, email)
	user := insertUser(t, pool, "not-held-user")

	removed, err := store.Unlink(ctx, user, email, league, team)
	require.NoError(t, err)
	assert.False(t, removed)
}

func TestPGStore_LinkRefusesUnknownTeam(t *testing.T) {
	pool := openPool(t)
	store := NewPGStore(pool)
	user := insertUser(t, pool, "admin-unknown")

	_, err := store.Link(context.Background(), user, newLeagueID(), 999)
	require.ErrorIs(t, err, ErrUnknownTeam)
}

// TestPGStore_ListUserTeamsFiltersByAllowlist: the instance's Yahoo league
// allowlist (empty = every league) limits which of a user's links are named.
func TestPGStore_ListUserTeamsFiltersByAllowlist(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	allowed, allowedTeam := seedLeagueTeam(t, pool, "Allowed")
	other, otherTeam := seedLeagueTeam(t, pool, "Other")
	user := insertUser(t, pool, "allowlisted")
	_, err := store.Link(ctx, user, allowed, allowedTeam)
	require.NoError(t, err)
	_, err = store.Link(ctx, user, other, otherTeam)
	require.NoError(t, err)

	all, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	assert.Len(t, all, 2)

	scoped, err := store.ListUserTeams(ctx, user, []int32{allowed})
	require.NoError(t, err)
	require.Len(t, scoped, 1)
	assert.Equal(t, allowedTeam, scoped[0].TeamID)
	assert.Equal(t, "Allowed", scoped[0].TeamName)
}

func TestPGStore_FindUser(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	username := uniqueUsername("findme")
	insertUser(t, pool, username)

	found, err := store.FindUser(ctx, testInstance, username)
	require.NoError(t, err)
	assert.Equal(t, username, found.Username)

	_, err = store.FindUser(ctx, testInstance, uniqueUsername("no-such-user"))
	require.ErrorIs(t, err, ErrUserNotFound)
}

// TestPGStore_FindUserScopedToInstance: a same-named user on another
// instance must not make the lookup ambiguous or match the wrong user.
func TestPGStore_FindUserScopedToInstance(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	username := uniqueUsername("scoped")
	want := insertUser(t, pool, username)
	insertUserOnInstance(t, pool, "yahoolink-other-instance", username)

	found, err := store.FindUser(ctx, testInstance, username)
	require.NoError(t, err)
	assert.Equal(t, want, found.ID)
}

func TestPGStore_FindUserAmbiguous(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	username := uniqueUsername("dupe")
	insertUser(t, pool, username)
	insertUser(t, pool, username)

	_, err := store.FindUser(ctx, testInstance, username)
	require.ErrorIs(t, err, ErrAmbiguousUser)
}

func TestPGStore_ListInstanceLinks(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	league, team := seedLeagueTeam(t, pool, "Instance List")
	user := insertUser(t, pool, "instance-lister")
	_, err := store.Link(ctx, user, league, team)
	require.NoError(t, err)

	links, err := store.ListInstanceLinks(ctx, testInstance)
	require.NoError(t, err)
	var found bool
	for _, l := range links {
		if l.Username == "instance-lister" && l.Link.TeamID == team {
			found = true
		}
	}
	assert.True(t, found)
}

// TestPGStore_ListInstanceLinksScopedToInstance: another instance's link
// must not appear, even for a user sharing the same username.
func TestPGStore_ListInstanceLinksScopedToInstance(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	username := uniqueUsername("cross-instance")
	league, team := seedLeagueTeam(t, pool, "Cross Instance")
	user := insertUser(t, pool, username)
	_, err := store.Link(ctx, user, league, team)
	require.NoError(t, err)

	otherLeague, otherTeam := seedLeagueTeam(t, pool, "Cross Instance Other")
	otherUser := insertUserOnInstance(t, pool, "yahoolink-other-instance", username)
	_, err = store.Link(ctx, otherUser, otherLeague, otherTeam)
	require.NoError(t, err)

	links, err := store.ListInstanceLinks(ctx, testInstance)
	require.NoError(t, err)
	var matches []InstanceLink
	for _, l := range links {
		if l.Username == username {
			matches = append(matches, l)
		}
	}
	require.Len(t, matches, 1, "only this instance's link for the shared username must appear")
	assert.Equal(t, league, matches[0].Link.LeagueID, "must be this instance's league, not the other instance's")
	assert.NotEqual(t, otherLeague, matches[0].Link.LeagueID)
}
