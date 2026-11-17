package graph

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sperano/puckdb/internal/appuser"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/sperano/puckdb/internal/graph/generated"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/yahoolink"
)

// fakeTeamPicker records the user and teams each call got.
type fakeTeamPicker struct {
	choices yahoolink.TeamChoices
	err     error
	users   []string
	teams   []yahoolink.TeamRef
}

func (f *fakeTeamPicker) TeamChoices(_ context.Context, userID string) (yahoolink.TeamChoices, error) {
	f.users = append(f.users, userID)
	return f.choices, f.err
}

func (f *fakeTeamPicker) SetTeams(_ context.Context, userID string, teams []yahoolink.TeamRef) (yahoolink.TeamChoices, error) {
	f.users = append(f.users, userID)
	f.teams = teams
	return f.choices, f.err
}

func (f *fakeTeamPicker) DismissTeamOffer(_ context.Context, userID string) error {
	f.users = append(f.users, userID)
	return f.err
}

// Every team picker field needs the request's user, is scoped by it, and
// fails without a picker.
func TestMauriceTeamPicker_RequiresAndScopesByUser(t *testing.T) {
	picker := &fakeTeamPicker{}
	r := &Resolver{MauriceTeams: picker}
	anonymous := context.Background()

	_, err := r.mauriceTeamChoices(anonymous)
	require.ErrorIs(t, err, appuser.ErrUnauthenticated)
	_, err = r.mauriceSetTeams(anonymous, nil)
	require.ErrorIs(t, err, appuser.ErrUnauthenticated)
	_, err = r.mauriceDismissTeamOffer(anonymous)
	require.ErrorIs(t, err, appuser.ErrUnauthenticated)
	assert.Empty(t, picker.users, "an unauthenticated request never reaches the picker")

	ctx := appuser.WithUser(context.Background(), appuser.User{ID: "user-1"})
	_, err = r.mauriceTeamChoices(ctx)
	require.NoError(t, err)
	_, err = r.mauriceSetTeams(ctx, []*model.TeamRef{{LeagueID: 29249, TeamID: 8}})
	require.NoError(t, err)
	ok, err := r.mauriceDismissTeamOffer(ctx)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, []string{"user-1", "user-1", "user-1"}, picker.users)
	assert.Equal(t, []yahoolink.TeamRef{{LeagueID: 29249, TeamID: 8}}, picker.teams)

	_, err = (&Resolver{}).mauriceTeamChoices(ctx)
	require.ErrorIs(t, err, errTeamPickerNotConfigured)
	_, err = (&Resolver{}).mauriceSetTeams(ctx, nil)
	require.ErrorIs(t, err, errTeamPickerNotConfigured)
	_, err = (&Resolver{}).mauriceDismissTeamOffer(ctx)
	require.ErrorIs(t, err, errTeamPickerNotConfigured)
}

// A TeamRef ID that is not a positive 32-bit integer is refused before the
// picker, so it never wraps into another team's ID.
func TestMauriceSetTeams_RefusesOutOfRangeIDs(t *testing.T) {
	ctx := appuser.WithUser(context.Background(), appuser.User{ID: "user-1"})
	for _, ref := range []model.TeamRef{
		{LeagueID: math.MaxInt32 + 1, TeamID: 1},
		{LeagueID: 1 << 32, TeamID: 1},
		{LeagueID: 29249, TeamID: 0},
		{LeagueID: -29249, TeamID: 1},
	} {
		picker := &fakeTeamPicker{}
		_, err := (&Resolver{MauriceTeams: picker}).mauriceSetTeams(ctx, []*model.TeamRef{{LeagueID: 1, TeamID: 1}, &ref})
		require.ErrorIs(t, err, errInvalidTeamRef, "%+v", ref)
		assert.Empty(t, picker.users, "%+v", ref)
	}
	_, err := (&Resolver{MauriceTeams: &fakeTeamPicker{}}).mauriceSetTeams(ctx, []*model.TeamRef{{LeagueID: math.MaxInt32, TeamID: 0}})
	assert.EqualError(t, err, "invalid team reference: teamId 0 is not a positive 32-bit integer")
}

// A team outside the choices reaches the client as is; a storage fault
// becomes errMauriceInternal.
func TestMauriceTeamPicker_ErrorMapping(t *testing.T) {
	ctx := appuser.WithUser(context.Background(), appuser.User{ID: "user-1"})
	notOffered := fmt.Errorf("%w: league 1 team 2", yahoolink.ErrTeamNotOffered)
	_, err := (&Resolver{MauriceTeams: &fakeTeamPicker{err: notOffered}}).mauriceSetTeams(ctx, nil)
	assert.EqualError(t, err, "team is not among the team choices: league 1 team 2")

	fault := &fakeTeamPicker{err: errors.New(`ERROR: relation "app_users" does not exist`)}
	_, err = (&Resolver{MauriceTeams: fault}).mauriceTeamChoices(ctx)
	require.ErrorIs(t, err, errMauriceInternal)
	_, err = (&Resolver{MauriceTeams: fault}).mauriceSetTeams(ctx, nil)
	require.ErrorIs(t, err, errMauriceInternal)
	_, err = (&Resolver{MauriceTeams: fault}).mauriceDismissTeamOffer(ctx)
	require.ErrorIs(t, err, errMauriceInternal)
}

func TestTeamChoicesToGQL(t *testing.T) {
	got := teamChoicesToGQL(yahoolink.TeamChoices{
		Season: 2026,
		Teams: []yahoolink.TeamChoice{
			{LeagueID: 29249, LeagueName: "Crapettes", TeamID: 8, TeamName: "Les Peter Stastny", ManagerNicknames: []string{"Eric"}, Source: yahoolink.SourceEmail},
			{LeagueID: 29249, LeagueName: "Crapettes", TeamID: 9, TeamName: "Nine", ManagerNicknames: []string{}, Source: yahoolink.SourceManual},
			{LeagueID: 29249, LeagueName: "Crapettes", TeamID: 10, TeamName: "Ten", ManagerNicknames: []string{}, Source: yahoolink.SourceSelf},
			{LeagueID: 29249, LeagueName: "Crapettes", TeamID: 11, TeamName: "Eleven", ManagerNicknames: []string{}},
		},
		Offered: true,
	})
	season := 2026
	email, manual, self := model.MauriceTeamLinkSourceEmail, model.MauriceTeamLinkSourceManual, model.MauriceTeamLinkSourceSelf
	assert.Equal(t, &model.MauriceTeamChoices{
		Season: &season,
		Teams: []*model.MauriceTeamChoice{
			{LeagueID: 29249, LeagueName: "Crapettes", TeamID: 8, TeamName: "Les Peter Stastny", ManagerNicknames: []string{"Eric"}, Linked: true, LinkSource: &email},
			{LeagueID: 29249, LeagueName: "Crapettes", TeamID: 9, TeamName: "Nine", ManagerNicknames: []string{}, Linked: true, LinkSource: &manual},
			{LeagueID: 29249, LeagueName: "Crapettes", TeamID: 10, TeamName: "Ten", ManagerNicknames: []string{}, Linked: true, LinkSource: &self},
			{LeagueID: 29249, LeagueName: "Crapettes", TeamID: 11, TeamName: "Eleven", ManagerNicknames: []string{}},
		},
		Offered: true,
	}, got)

	assert.Equal(t, &model.MauriceTeamChoices{Teams: []*model.MauriceTeamChoice{}}, teamChoicesToGQL(yahoolink.TeamChoices{}),
		"no season: null, no teams")
}

// teamPickerSeason is the Yahoo season the end-to-end test seeds and the
// picker treats as current.
const teamPickerSeason = 2026

// Through the real schema and yahoolink.Picker on PostgreSQL: the choices
// carry nicknames and never the managers' emails or guids, a selection is a
// SELF link and marks the offer, and every field refuses an anonymous
// request.
func TestMauriceTeamPicker_EndToEnd(t *testing.T) {
	pool := pgtest.Open(t)
	league := 10_000_000 + rand.Int32N(1_000_000_000-10_000_000)
	const email, guid = "hidden-manager@example.com", "SECRETMANAGERGUID"
	seedPickerTeam(t, pool, league, email, guid)
	user := seedPickerUser(t, pool)
	picker := yahoolink.NewPicker(pool, []int32{league}, func() int32 { return teamPickerSeason })
	server := newTeamPickerServer(t, &Resolver{MauriceTeams: picker})

	const fields = `season teams { leagueId leagueName teamId teamName managerNicknames linked linkSource } offered`
	resp := postGraphQL(t, server, http.Header{"X-Test-User": {user}}, `{ mauriceTeamChoices { `+fields+` } }`, nil)
	require.Empty(t, resp.Errors)
	assert.NotContains(t, string(resp.Data), email)
	assert.NotContains(t, string(resp.Data), guid)
	assert.JSONEq(t, fmt.Sprintf(`{"mauriceTeamChoices": {"season": 2026, "offered": false, "teams": [
		{"leagueId": %d, "leagueName": "Picker League", "teamId": 8, "teamName": "Les Peter Stastny",
		 "managerNicknames": ["Eric"], "linked": false, "linkSource": null}]}}`, league), string(resp.Data))

	resp = postGraphQL(t, server, http.Header{"X-Test-User": {user}},
		`mutation($teams: [TeamRef!]!) { mauriceSetTeams(teams: $teams) { `+fields+` } }`,
		map[string]any{"teams": []map[string]any{{"leagueId": league, "teamId": 8}}})
	require.Empty(t, resp.Errors)
	assert.JSONEq(t, fmt.Sprintf(`{"mauriceSetTeams": {"season": 2026, "offered": true, "teams": [
		{"leagueId": %d, "leagueName": "Picker League", "teamId": 8, "teamName": "Les Peter Stastny",
		 "managerNicknames": ["Eric"], "linked": true, "linkSource": "SELF"}]}}`, league), string(resp.Data))

	resp = postGraphQL(t, server, http.Header{"X-Test-User": {user}}, `mutation { mauriceDismissTeamOffer }`, nil)
	require.Empty(t, resp.Errors)
	assert.JSONEq(t, `{"mauriceDismissTeamOffer": true}`, string(resp.Data))

	for _, query := range []string{
		`{ mauriceTeamChoices { offered } }`,
		`mutation { mauriceSetTeams(teams: []) { offered } }`,
		`mutation { mauriceDismissTeamOffer }`,
	} {
		resp = postGraphQL(t, server, nil, query, nil)
		require.Len(t, resp.Errors, 1, query)
		assert.Equal(t, appuser.ErrUnauthenticated.Error(), resp.Errors[0].Message, query)
	}
}

// newTeamPickerServer serves the schema over r, resolving the user named
// by the X-Test-User header (none without it).
func newTeamPickerServer(t *testing.T, r *Resolver) *httptest.Server {
	t.Helper()
	schema := handler.New(generated.NewExecutableSchema(generated.Config{Resolvers: r}))
	schema.AddTransport(transport.POST{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if id := req.Header.Get("X-Test-User"); id != "" {
			req = req.WithContext(appuser.WithUser(req.Context(), appuser.User{ID: id}))
		}
		schema.ServeHTTP(w, req)
	}))
	t.Cleanup(server.Close)
	return server
}

// seedPickerTeam inserts league (season teamPickerSeason) with team 8,
// managed by "Eric" under email and guid.
func seedPickerTeam(t *testing.T, pool *pgxpool.Pool, league int32, email, guid string) {
	t.Helper()
	ctx := context.Background()
	key := fmt.Sprintf("465.l.%d", league)
	_, err := pool.Exec(ctx, `INSERT INTO yahoo_leagues (id, league_key, name, season) VALUES ($1, $2, 'Picker League', $3)`,
		league, key, teamPickerSeason)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO yahoo_teams (league_id, id, team_key, name) VALUES ($1, 8, $2, 'Les Peter Stastny')`,
		league, key+".t.8")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO yahoo_team_managers (league_id, team_id, id, nickname, guid, email) VALUES ($1, 8, 1, 'Eric', $2, $3)`,
		league, guid, email)
	require.NoError(t, err)
}

// seedPickerUser inserts an app_users row and returns its ID.
func seedPickerUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id pgtype.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO app_users (instance, auth_provider, auth_subject, username) VALUES ('puckdb-test', 'authentik', $1, 'picker') RETURNING id`,
		fmt.Sprintf("picker-%d", rand.Int64())).Scan(&id))
	return id.String()
}
