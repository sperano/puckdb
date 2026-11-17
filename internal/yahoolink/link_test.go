package yahoolink

import (
	"context"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPGStore_ListUserTeamsFiltersByAllowlist: the instance's Yahoo league
// allowlist (empty = every league) limits which of a user's links are named.
func TestPGStore_ListUserTeamsFiltersByAllowlist(t *testing.T) {
	pool := pgtest.Open(t)
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

// TestPGStore_ListUserTeamsReturnsLeagueNameAndSeason: each link carries
// its league's name and season (the Yahoo start year), per league.
func TestPGStore_ListUserTeamsReturnsLeagueNameAndSeason(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	current, currentTeam := seedLeagueTeamInSeason(t, pool, "This Year", "Crapettes", 2026)
	past, pastTeam := seedLeagueTeamInSeason(t, pool, "Last Year", "Vintage", 2025)
	user := insertUser(t, pool, "seasons")
	_, err := store.Link(ctx, user, current, currentTeam)
	require.NoError(t, err)
	_, err = store.Link(ctx, user, past, pastTeam)
	require.NoError(t, err)

	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 2)
	byLeague := map[int32]Link{links[0].LeagueID: links[0], links[1].LeagueID: links[1]}
	assert.Equal(t, "Crapettes", byLeague[current].LeagueName)
	assert.Equal(t, int32(2026), byLeague[current].Season)
	assert.Equal(t, "This Year", byLeague[current].TeamName)
	assert.Equal(t, "Vintage", byLeague[past].LeagueName)
	assert.Equal(t, int32(2025), byLeague[past].Season)
	assert.Equal(t, "Last Year", byLeague[past].TeamName)
}

func TestPGStore_FindUser(t *testing.T) {
	pool := pgtest.Open(t)
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
	pool := pgtest.Open(t)
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
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	username := uniqueUsername("dupe")
	insertUser(t, pool, username)
	insertUser(t, pool, username)

	_, err := store.FindUser(ctx, testInstance, username)
	require.ErrorIs(t, err, ErrAmbiguousUser)
}

func TestPGStore_ListInstanceLinks(t *testing.T) {
	pool := pgtest.Open(t)
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
	pool := pgtest.Open(t)
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

// TestPGStore_ListUserTeamsListsATeamOnce: a team reached through a team
// link and a guid link is listed once, under the manual link; between two
// email links, under the guid link.
func TestPGStore_ListUserTeamsListsATeamOnce(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	tests := []struct {
		name       string
		teamSource string
		guidSource string
		wantSource string
		wantGUID   bool
	}{
		{name: "manual team link wins", teamSource: SourceManual, guidSource: SourceEmail, wantSource: SourceManual},
		{name: "manual guid link wins", teamSource: SourceEmail, guidSource: SourceManual, wantSource: SourceManual, wantGUID: true},
		{name: "guid link wins between email links", teamSource: SourceEmail, guidSource: SourceEmail, wantSource: SourceEmail, wantGUID: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guid := newGUID()
			league, team := seedLeagueTeam(t, pool, "Twice")
			seedManagerGUID(t, pool, league, team, 1, guid, "")
			user := insertUser(t, pool, "twice")
			_, err := pool.Exec(ctx, `INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source) VALUES ($1, $2, $3, $4)`,
				user, league, team, tt.teamSource)
			require.NoError(t, err)
			_, err = pool.Exec(ctx, `INSERT INTO app_user_yahoo_managers (user_id, guid, source) VALUES ($1, $2, $3)`,
				user, guid, tt.guidSource)
			require.NoError(t, err)

			links, err := store.ListUserTeams(ctx, user, nil)
			require.NoError(t, err)
			require.Len(t, links, 1)
			assert.Equal(t, tt.wantSource, links[0].Source)
			wantGUID := ""
			if tt.wantGUID {
				wantGUID = guid
			}
			assert.Equal(t, wantGUID, links[0].GUID)

			instance, err := store.ListInstanceLinks(ctx, testInstance)
			require.NoError(t, err)
			var mine []Link
			for _, l := range instance {
				if l.Link.LeagueID == league {
					mine = append(mine, l.Link)
				}
			}
			require.Len(t, mine, 1, "the admin list picks the same single link")
			assert.Equal(t, tt.wantSource, mine[0].Source)
			assert.Equal(t, wantGUID, mine[0].GUID)
		})
	}
}

// TestPGStore_ListUserTeamsFiltersGUIDTeamsByAllowlist: the allowlist
// limits the teams a guid link reaches too.
func TestPGStore_ListUserTeamsFiltersGUIDTeamsByAllowlist(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	guid := newGUID()
	allowed, allowedTeam := seedLeagueTeam(t, pool, "Allowed")
	seedManagerGUID(t, pool, allowed, allowedTeam, 1, guid, "")
	other, otherTeam := seedLeagueTeamInSeason(t, pool, "Other", "Vintage", 2025)
	seedManagerGUID(t, pool, other, otherTeam, 1, guid, "")
	user := insertUser(t, pool, "guid-allowlisted")
	_, err := store.Link(ctx, user, allowed, allowedTeam)
	require.NoError(t, err)

	all, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []teamKey{{allowed, allowedTeam}, {other, otherTeam}}, teamKeys(all))
	for _, l := range all {
		require.Equal(t, guid, l.GUID, "both teams come through the guid link")
	}

	scoped, err := store.ListUserTeams(ctx, user, []int32{allowed})
	require.NoError(t, err)
	assert.Equal(t, []teamKey{{allowed, allowedTeam}}, teamKeys(scoped))
}

// TestPGStore_ListUserTeamsPicksTheEarliestLink: a co-managed team reached
// through two of a user's email guid links is listed once, under the
// earlier link, even when its guid sorts last; another user on the same
// team keeps their own row in the admin list.
func TestPGStore_ListUserTeamsPicksTheEarliestLink(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	guidA := newGUID()
	guidB := guidA + "B"
	league, team := seedLeagueTeam(t, pool, "Two Accounts")
	seedManagerGUID(t, pool, league, team, 1, guidA, "")
	seedManagerGUID(t, pool, league, team, 2, guidB, "")
	username, otherUsername := uniqueUsername("earliest"), uniqueUsername("earliest-other")
	user := insertUser(t, pool, username)
	earlier := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	later := earlier.AddDate(0, 1, 0)
	for _, l := range []struct {
		guid     string
		linkedAt time.Time
	}{{guidA, later}, {guidB, earlier}} {
		_, err := pool.Exec(ctx, `INSERT INTO app_user_yahoo_managers (user_id, guid, source, linked_at) VALUES ($1, $2, 'email', $3)`,
			user, l.guid, l.linkedAt)
		require.NoError(t, err)
	}
	otherUser := insertUser(t, pool, otherUsername)
	_, err := pool.Exec(ctx, `INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source) VALUES ($1, $2, $3, 'manual')`,
		otherUser, league, team)
	require.NoError(t, err)

	links, err := store.ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, guidB, links[0].GUID)
	assert.True(t, earlier.Equal(links[0].LinkedAt), "linked_at %s", links[0].LinkedAt)

	instance, err := store.ListInstanceLinks(ctx, testInstance)
	require.NoError(t, err)
	got := map[string]Link{}
	for _, l := range instance {
		if l.Link.LeagueID == league {
			require.NotContains(t, got, l.Username, "one row per user and team")
			got[l.Username] = l.Link
		}
	}
	require.Len(t, got, 2, "each user keeps their own row")
	assert.Equal(t, guidB, got[username].GUID, "the admin list picks the same earliest link")
	assert.True(t, earlier.Equal(got[username].LinkedAt))
	assert.Equal(t, SourceManual, got[otherUsername].Source)
	assert.Empty(t, got[otherUsername].GUID)
}

// TestPGStore_ListInstanceLinksHidesADeselectedTeam: a team the user
// deselected in the picker is left out of the admin list, though the
// operator's manual link to it stays.
func TestPGStore_ListInstanceLinksHidesADeselectedTeam(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	league, team1 := seedLeagueTeam(t, pool, "Deselected")
	seedTeam(t, pool, league, 2, "Kept")
	username := uniqueUsername("deselecting")
	user := insertUser(t, pool, username)
	for _, team := range []int32{team1, 2} {
		_, err := store.Link(ctx, user, league, team)
		require.NoError(t, err)
	}
	_, err := newTestPicker(pool, testSeason, league).SetTeams(ctx, userIDText(user), []TeamRef{{league, 2}})
	require.NoError(t, err)

	links, err := store.ListInstanceLinks(ctx, testInstance)
	require.NoError(t, err)
	var listed []teamKey
	for _, l := range links {
		if l.Username == username {
			listed = append(listed, teamKey{l.Link.LeagueID, l.Link.TeamID})
		}
	}
	assert.Equal(t, []teamKey{{league, 2}}, listed)
	assert.Equal(t, 1, countTeamLinksTo(t, pool, user, league, team1), "the manual link stays, hidden")
}
