package yahoolink

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pastSeason is a Yahoo season before testSeason, for the picker's season
// filtering.
const pastSeason = testSeason - 1

// newTestPicker returns a Picker over pool for the leagueIDs allowlist, with
// current as the current NHL season (a Yahoo start year).
func newTestPicker(pool *pgxpool.Pool, current int32, leagueIDs ...int32) *Picker {
	return NewPicker(pool, leagueIDs, func() int32 { return current })
}

// seedNamedManager gives leagueID/teamID a manager with nickname, guid and
// email.
func seedNamedManager(t *testing.T, pool *pgxpool.Pool, leagueID, teamID, managerID int32, nickname, guid, email string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO yahoo_team_managers (league_id, team_id, id, nickname, guid, email) VALUES ($1, $2, $3, $4, $5, $6)`,
		leagueID, teamID, managerID, nickname, guid, email)
	require.NoError(t, err)
}

// userIDText is id in the text form the Picker takes.
func userIDText(id pgtype.UUID) string { return id.String() }

// choiceState is one choice as "league:team:source" (source empty when not
// linked), so a test asserts every choice's link at once.
func choiceState(c TeamChoices) []string {
	states := make([]string, len(c.Teams))
	for i, t := range c.Teams {
		states[i] = fmt.Sprintf("%d:%d:%s", t.LeagueID, t.TeamID, t.Source)
	}
	return states
}

// state is the choiceState entry of one team.
func state(leagueID, teamID int32, source string) string {
	return fmt.Sprintf("%d:%d:%s", leagueID, teamID, source)
}

// TestPicker_ChoicesAreTheDefaultSeasonsTeamsInTheAllowlist: the choices are
// the teams of the newest allowlisted season not after the current one, with
// league and team names and manager nicknames (blank ones left out), and
// nothing of another season or of a league outside the allowlist.
func TestPicker_ChoicesAreTheDefaultSeasonsTeamsInTheAllowlist(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()

	current, teamA := seedLeagueTeamInSeason(t, pool, "Les Peter Stastny", "Crapettes", testSeason)
	seedTeam(t, pool, current, 2, "Second Team")
	seedNamedManager(t, pool, current, teamA, 2, "Zed", hiddenGUID, "zed@example.com")
	seedNamedManager(t, pool, current, teamA, 1, "Eric", hiddenGUID, "eric@example.com")
	seedNamedManager(t, pool, current, 2, 1, "", hiddenGUID, "")
	past, _ := seedLeagueTeamInSeason(t, pool, "Old Team", "Old League", pastSeason)
	outside, _ := seedLeagueTeamInSeason(t, pool, "Elsewhere", "Not Allowed", testSeason)
	user := insertUser(t, pool, "picker")

	choices, err := newTestPicker(pool, testSeason, current, past).TeamChoices(ctx, userIDText(user))
	require.NoError(t, err)
	assert.Equal(t, TeamChoices{
		Season: testSeason,
		Teams: []TeamChoice{
			{LeagueID: current, LeagueName: "Crapettes", TeamID: teamA, TeamName: "Les Peter Stastny", ManagerNicknames: []string{"Eric", "Zed"}},
			{LeagueID: current, LeagueName: "Crapettes", TeamID: 2, TeamName: "Second Team", ManagerNicknames: []string{}},
		},
	}, choices, "only nicknames, ordered by manager ID, never an email or a guid")

	choices, err = newTestPicker(pool, testSeason, past).TeamChoices(ctx, userIDText(user))
	require.NoError(t, err)
	assert.Equal(t, pastSeason, int(choices.Season), "off-season: the newest allowlisted season not after the current one")
	assert.Equal(t, []string{state(past, 1, "")}, choiceState(choices))

	choices, err = newTestPicker(pool, pastSeason-1, current, past).TeamChoices(ctx, userIDText(user))
	require.NoError(t, err)
	assert.Equal(t, TeamChoices{Teams: []TeamChoice{}}, choices, "no season up to the current one: no choices")

	choices, err = newTestPicker(pool, testSeason).TeamChoices(ctx, userIDText(user))
	require.NoError(t, err)
	keys := teamKeysOf(choices)
	assert.Contains(t, keys, teamKey{current, teamA}, "an empty allowlist allows every league")
	assert.Contains(t, keys, teamKey{outside, 1})
	assert.NotContains(t, keys, teamKey{past, 1}, "but only the default season's")
}

// teamKeysOf returns the (league, team) pairs of the choices.
func teamKeysOf(c TeamChoices) []teamKey {
	keys := make([]teamKey, len(c.Teams))
	for i, t := range c.Teams {
		keys[i] = teamKey{t.LeagueID, t.TeamID}
	}
	return keys
}

// TestPicker_SetTeamsReplacesTheSelection: a pick is a SourceSelf team link,
// a second call replaces the first one's selection, and both mark the offer
// as seen.
func TestPicker_SetTeamsReplacesTheSelection(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()

	league, team1 := seedLeagueTeam(t, pool, "One")
	seedTeam(t, pool, league, 2, "Two")
	user := insertUser(t, pool, "picker")
	picker := newTestPicker(pool, testSeason, league)

	choices, err := picker.SetTeams(ctx, userIDText(user), []TeamRef{{league, team1}, {league, team1}})
	require.NoError(t, err)
	assert.True(t, choices.Offered)
	assert.Equal(t, []string{state(league, team1, SourceSelf), state(league, 2, "")}, choiceState(choices))

	choices, err = picker.SetTeams(ctx, userIDText(user), []TeamRef{{league, 2}})
	require.NoError(t, err)
	assert.Equal(t, []string{state(league, team1, ""), state(league, 2, SourceSelf)}, choiceState(choices))

	links, err := NewPGStore(pool).ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	assert.Equal(t, []teamKey{{league, 2}}, teamKeys(links), "the prompt's teams follow the selection")
	assert.Equal(t, 0, countTeamLinksTo(t, pool, user, league, team1), "the deselected pick's own link is gone")
}

// countTeamLinksTo counts user's app_user_yahoo_teams rows for the team.
func countTeamLinksTo(t *testing.T, pool *pgxpool.Pool, user pgtype.UUID, leagueID, teamID int32) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM app_user_yahoo_teams WHERE user_id = $1 AND league_id = $2 AND team_id = $3`,
		user, leagueID, teamID).Scan(&n))
	return n
}

// TestPicker_DeselectedEmailMatchSurvivesTheNextLoginSync: teams the login
// email matches, deselected, stay deselected through the next
// SyncEmailLinks, both through a guid link and as a team link (a hidden
// guid the email cannot recover: it carries two real guids), while the
// guid links' other-season teams stay (links outside the choices are
// untouched).
func TestPicker_DeselectedEmailMatchSurvivesTheNextLoginSync(t *testing.T) {
	pool := pgtest.Open(t)
	q := sqlcdb.New(pool)
	ctx := context.Background()

	email := uniqueUsername("owner") + "@example.com"
	guid1, guid2 := newGUID(), newGUID()
	realGUID, realTeam := seedLeagueTeam(t, pool, "Real Guid")
	seedManagerGUID(t, pool, realGUID, realTeam, 1, guid1, email)
	hidden, hiddenTeam := seedLeagueTeam(t, pool, "Hidden Guid")
	seedManagerGUID(t, pool, hidden, hiddenTeam, 1, hiddenGUID, email)
	old1, oldTeam1 := seedLeagueTeamInSeason(t, pool, "Old One", testLeagueName, pastSeason)
	seedManagerGUID(t, pool, old1, oldTeam1, 1, guid1, email)
	old2, oldTeam2 := seedLeagueTeamInSeason(t, pool, "Old Two", testLeagueName, pastSeason)
	seedManagerGUID(t, pool, old2, oldTeam2, 1, guid2, email)
	user := insertUser(t, pool, "owner")
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))

	picker := newTestPicker(pool, testSeason, realGUID, hidden, old1, old2)
	choices, err := picker.TeamChoices(ctx, userIDText(user))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{state(realGUID, realTeam, SourceEmail), state(hidden, hiddenTeam, SourceEmail)}, choiceState(choices))
	require.Equal(t, 1, countTeamLinksTo(t, pool, user, hidden, hiddenTeam), "fixture: the hidden-guid team is a team link")

	_, err = picker.SetTeams(ctx, userIDText(user), nil)
	require.NoError(t, err)
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))

	choices, err = picker.TeamChoices(ctx, userIDText(user))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{state(realGUID, realTeam, ""), state(hidden, hiddenTeam, "")}, choiceState(choices))
	assert.Equal(t, 0, countTeamLinksTo(t, pool, user, hidden, hiddenTeam), "the resync made no team link to the deselected team")
	links, err := NewPGStore(pool).ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	assert.ElementsMatch(t, []teamKey{{old1, oldTeam1}, {old2, oldTeam2}}, teamKeys(links), "the guid links' other-season teams stay")
}

// TestPicker_PickingAResolvedTeamLinksItsAccount: a team whose managers all
// resolve to one guid gets a SourceSelf guid link, as link-team does, so
// the account's other-season team follows; the account's other team in the
// choices, not picked, is deselected, so the selection is exactly the pick.
// A co-managed team gets a team link.
func TestPicker_PickingAResolvedTeamLinksItsAccount(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()

	guid, coGUID1, coGUID2 := newGUID(), newGUID(), newGUID()
	email := uniqueUsername("acct") + "@example.com"
	picked, pickedTeam := seedLeagueTeam(t, pool, "Picked")
	seedManagerGUID(t, pool, picked, pickedTeam, 1, hiddenGUID, email)
	seedTeam(t, pool, picked, 2, "Same Account")
	seedManagerGUID(t, pool, picked, 2, 1, guid, "")
	old, oldTeam := seedLeagueTeamInSeason(t, pool, "Old", testLeagueName, pastSeason)
	seedManagerGUID(t, pool, old, oldTeam, 1, guid, email)
	seedTeam(t, pool, picked, 3, "Co-managed")
	seedManagerGUID(t, pool, picked, 3, 1, coGUID1, "")
	seedManagerGUID(t, pool, picked, 3, 2, coGUID2, "")
	user := insertUser(t, pool, "picker")
	picker := newTestPicker(pool, testSeason, picked, old)

	choices, err := picker.SetTeams(ctx, userIDText(user), []TeamRef{{picked, pickedTeam}})
	require.NoError(t, err)
	assert.Equal(t, []string{
		state(picked, pickedTeam, SourceSelf), state(picked, 2, ""), state(picked, 3, ""),
	}, choiceState(choices), "the account's other team in the choices is not picked")

	var source string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT source FROM app_user_yahoo_managers WHERE user_id = $1 AND guid = $2`, user, guid).Scan(&source))
	assert.Equal(t, SourceSelf, source)
	assert.Equal(t, 0, countTeamLinksTo(t, pool, user, picked, pickedTeam), "a guid link, not a team link")
	links, err := NewPGStore(pool).ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	assert.ElementsMatch(t, []teamKey{{picked, pickedTeam}, {old, oldTeam}}, teamKeys(links), "the other season follows the account")

	choices, err = picker.SetTeams(ctx, userIDText(user), []TeamRef{{picked, 2}, {picked, 3}})
	require.NoError(t, err)
	assert.Equal(t, []string{
		state(picked, pickedTeam, ""), state(picked, 2, SourceSelf), state(picked, 3, SourceSelf),
	}, choiceState(choices), "picking a deselected team clears its deselection")
	assert.Equal(t, 1, countTeamLinksTo(t, pool, user, picked, 3), "a co-managed team gets a team link")
}

// TestPicker_RejectsATeamOutsideTheChoicesAndChangesNothing: a team of
// another season or outside the allowlist fails with ErrTeamNotOffered and
// rolls back, the offer flag included.
func TestPicker_RejectsATeamOutsideTheChoicesAndChangesNothing(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()

	league, team := seedLeagueTeam(t, pool, "Allowed")
	past, pastTeam := seedLeagueTeamInSeason(t, pool, "Past", testLeagueName, pastSeason)
	outside, outsideTeam := seedLeagueTeam(t, pool, "Outside")
	user := insertUser(t, pool, "picker")
	picker := newTestPicker(pool, testSeason, league, past)

	for _, ref := range []TeamRef{{past, pastTeam}, {outside, outsideTeam}, {league, team + 1}} {
		_, err := picker.SetTeams(ctx, userIDText(user), []TeamRef{{league, team}, ref})
		require.ErrorIs(t, err, ErrTeamNotOffered)
		assert.EqualError(t, err, fmt.Sprintf("%s: league %d team %d", ErrTeamNotOffered, ref.LeagueID, ref.TeamID))
	}
	choices, err := picker.TeamChoices(ctx, userIDText(user))
	require.NoError(t, err)
	assert.False(t, choices.Offered)
	assert.Equal(t, []string{state(league, team, "")}, choiceState(choices))
}

// TestPicker_OfferFlag: unseen until DismissTeamOffer, which links nothing;
// reading the choices never marks it.
func TestPicker_OfferFlag(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()

	league, team := seedLeagueTeam(t, pool, "Team")
	user := insertUser(t, pool, "picker")
	picker := newTestPicker(pool, testSeason, league)

	choices, err := picker.TeamChoices(ctx, userIDText(user))
	require.NoError(t, err)
	assert.False(t, choices.Offered)
	choices, err = picker.TeamChoices(ctx, userIDText(user))
	require.NoError(t, err)
	assert.False(t, choices.Offered, "reading the choices does not mark the offer")

	require.NoError(t, picker.DismissTeamOffer(ctx, userIDText(user)))
	choices, err = picker.TeamChoices(ctx, userIDText(user))
	require.NoError(t, err)
	assert.True(t, choices.Offered)
	assert.Equal(t, []string{state(league, team, "")}, choiceState(choices))

	missing := pgtype.UUID{Bytes: [16]byte{0xde, 0xad}, Valid: true}
	err = picker.DismissTeamOffer(ctx, userIDText(missing))
	require.ErrorIs(t, err, ErrUserNotFound)
	_, err = picker.TeamChoices(ctx, "not-a-uuid")
	require.ErrorContains(t, err, `invalid user id "not-a-uuid"`)
}

// TestPicker_LeavesAnotherUsersLinksAlone: one user's selection and
// deselection change nothing for another user linked to the same teams.
func TestPicker_LeavesAnotherUsersLinksAlone(t *testing.T) {
	pool := pgtest.Open(t)
	q := sqlcdb.New(pool)
	ctx := context.Background()

	email := uniqueUsername("other") + "@example.com"
	league, team := seedLeagueTeam(t, pool, "Shared")
	seedManagerGUID(t, pool, league, team, 1, hiddenGUID, email)
	seedTeam(t, pool, league, 2, "Other Pick")
	other := insertUser(t, pool, "other")
	require.NoError(t, SyncEmailLinks(ctx, q, other, email))
	user := insertUser(t, pool, "picker")
	picker := newTestPicker(pool, testSeason, league)

	_, err := picker.SetTeams(ctx, userIDText(user), []TeamRef{{league, team}})
	require.NoError(t, err)
	_, err = picker.SetTeams(ctx, userIDText(user), []TeamRef{{league, 2}})
	require.NoError(t, err)

	choices, err := picker.TeamChoices(ctx, userIDText(other))
	require.NoError(t, err)
	assert.Equal(t, []string{state(league, team, SourceEmail), state(league, 2, "")}, choiceState(choices))
	assert.False(t, choices.Offered)
}

// TestPicker_ManualLinkDeselectedIsHiddenNotRemoved: deselecting a team an
// operator linked hides it while the manual link stays; picking it again
// shows it through that manual link.
func TestPicker_ManualLinkDeselectedIsHiddenNotRemoved(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()

	league, team := seedLeagueTeam(t, pool, "Operator's")
	user := insertUser(t, pool, "picker")
	store := NewPGStore(pool)
	_, err := store.Link(ctx, user, league, team)
	require.NoError(t, err)
	picker := newTestPicker(pool, testSeason, league)

	choices, err := picker.SetTeams(ctx, userIDText(user), nil)
	require.NoError(t, err)
	assert.Equal(t, []string{state(league, team, "")}, choiceState(choices))
	assert.Equal(t, 1, countTeamLinksTo(t, pool, user, league, team), "the manual link stays")

	choices, err = picker.SetTeams(ctx, userIDText(user), []TeamRef{{league, team}})
	require.NoError(t, err)
	assert.Equal(t, []string{state(league, team, SourceManual)}, choiceState(choices))
}

// TestLink_ClearsTheUsersDeselection: an operator's link-team on a team the
// user deselected (an email match) shows it again, as a manual link that
// survives the resync.
func TestLink_ClearsTheUsersDeselection(t *testing.T) {
	pool := pgtest.Open(t)
	q := sqlcdb.New(pool)
	ctx := context.Background()

	email := uniqueUsername("owner") + "@example.com"
	league, team := seedLeagueTeam(t, pool, "Email Team")
	seedManagerGUID(t, pool, league, team, 1, hiddenGUID, email)
	user := insertUser(t, pool, "owner")
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))
	picker := newTestPicker(pool, testSeason, league)
	_, err := picker.SetTeams(ctx, userIDText(user), nil)
	require.NoError(t, err)

	_, err = NewPGStore(pool).Link(ctx, user, league, team)
	require.NoError(t, err)
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))

	choices, err := picker.TeamChoices(ctx, userIDText(user))
	require.NoError(t, err)
	assert.Equal(t, []string{state(league, team, SourceManual)}, choiceState(choices))
	assert.Equal(t, 0, countRejections(t, pool, user))
}

// countRejections counts user's app_user_yahoo_team_rejections rows.
func countRejections(t *testing.T, pool *pgxpool.Pool, user pgtype.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM app_user_yahoo_team_rejections WHERE user_id = $1`, user).Scan(&n))
	return n
}

// TestUnlink_ClearsTheUsersDeselection: unlink-team removes a deselection
// once the email no longer matches the team (reporting it), and keeps it
// while the email matches, since the resync would link the team again.
func TestUnlink_ClearsTheUsersDeselection(t *testing.T) {
	pool := pgtest.Open(t)
	q := sqlcdb.New(pool)
	ctx := context.Background()

	email := uniqueUsername("owner") + "@example.com"
	league, team := seedLeagueTeam(t, pool, "Email Team")
	seedManagerGUID(t, pool, league, team, 1, hiddenGUID, email)
	user := insertUser(t, pool, "owner")
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))
	_, err := newTestPicker(pool, testSeason, league).SetTeams(ctx, userIDText(user), nil)
	require.NoError(t, err)
	store := NewPGStore(pool)

	removed, err := store.Unlink(ctx, user, email, league, team)
	require.NoError(t, err)
	assert.Equal(t, Unlinked{}, removed, "the email still matches: nothing to remove, the deselection stays")
	assert.Equal(t, 1, countRejections(t, pool, user))

	removed, err = store.Unlink(ctx, user, "changed@example.com", league, team)
	require.NoError(t, err)
	assert.Equal(t, Unlinked{Rejection: true}, removed)
	assert.True(t, removed.Removed())
	assert.Equal(t, 0, countRejections(t, pool, user))
}

// TestListUserTeams_OwnPickWinsOverAGuidLink: a co-managed team the user
// picked (a SourceSelf team link) and later reaches through an email guid
// link to one of its managers is listed by the pick.
func TestListUserTeams_OwnPickWinsOverAGuidLink(t *testing.T) {
	pool := pgtest.Open(t)
	q := sqlcdb.New(pool)
	ctx := context.Background()

	email := uniqueUsername("co") + "@example.com"
	league, team := seedLeagueTeam(t, pool, "Co-managed")
	seedManagerGUID(t, pool, league, team, 1, newGUID(), email)
	seedManagerGUID(t, pool, league, team, 2, newGUID(), "")
	user := insertUser(t, pool, "co")
	_, err := newTestPicker(pool, testSeason, league).SetTeams(ctx, userIDText(user), []TeamRef{{league, team}})
	require.NoError(t, err)
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))

	links, err := NewPGStore(pool).ListUserTeams(ctx, user, nil)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, SourceSelf, links[0].Source)
	assert.Empty(t, links[0].GUID, "the team link, not the guid link")
}

// TestPicker_PickPromotesAnEmailLinkToSelf: picking a team the user reaches
// only through an email link (a guid link, and a team link: a hidden guid
// the email cannot recover, since it carries two real guids) makes it the
// user's pick, so it stays once the login email no longer matches.
func TestPicker_PickPromotesAnEmailLinkToSelf(t *testing.T) {
	pool := pgtest.Open(t)
	q := sqlcdb.New(pool)
	ctx := context.Background()

	email := uniqueUsername("owner") + "@example.com"
	guid := newGUID()
	byGUID, byGUIDTeam := seedLeagueTeam(t, pool, "By Guid")
	seedManagerGUID(t, pool, byGUID, byGUIDTeam, 1, guid, email)
	old, oldTeam := seedLeagueTeamInSeason(t, pool, "Old", testLeagueName, pastSeason)
	seedManagerGUID(t, pool, old, oldTeam, 1, newGUID(), email)
	byTeam, byTeamTeam := seedLeagueTeam(t, pool, "By Team")
	seedManagerGUID(t, pool, byTeam, byTeamTeam, 1, hiddenGUID, email)
	user := insertUser(t, pool, "owner")
	require.NoError(t, SyncEmailLinks(ctx, q, user, email))
	require.Equal(t, 1, countTeamLinksTo(t, pool, user, byTeam, byTeamTeam), "fixture: the hidden-guid team is a team link")

	picker := newTestPicker(pool, testSeason, byGUID, byTeam)
	choices, err := picker.SetTeams(ctx, userIDText(user), []TeamRef{{byGUID, byGUIDTeam}, {byTeam, byTeamTeam}})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{state(byGUID, byGUIDTeam, SourceSelf), state(byTeam, byTeamTeam, SourceSelf)}, choiceState(choices))
	var source string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT source FROM app_user_yahoo_managers WHERE user_id = $1 AND guid = $2`, user, guid).Scan(&source))
	assert.Equal(t, SourceSelf, source, "the email guid link became the pick")

	require.NoError(t, SyncEmailLinks(ctx, q, user, uniqueUsername("changed")+"@example.com"))
	choices, err = picker.TeamChoices(ctx, userIDText(user))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{state(byGUID, byGUIDTeam, SourceSelf), state(byTeam, byTeamTeam, SourceSelf)}, choiceState(choices),
		"both picks outlive the email match")
}
