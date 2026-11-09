package yahoolink

import (
	"context"
	"testing"

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

	removed, err := store.Unlink(ctx, user, league, team)
	require.NoError(t, err)
	assert.True(t, removed)

	removed, err = store.Unlink(ctx, user, league, team)
	require.NoError(t, err)
	assert.False(t, removed, "a second unlink finds nothing")
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
