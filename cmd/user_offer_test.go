package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/sperano/puckdb/internal/graph"
	"github.com/sperano/puckdb/internal/yahoolink"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// teamOfferAdminToken is the admin token the test API server accepts.
const teamOfferAdminToken = "team-offer-admin-token"

func TestResetTeamOfferTarget(t *testing.T) {
	username, err := resetTeamOfferTarget([]string{"alice"}, false)
	require.NoError(t, err)
	assert.Equal(t, "alice", username)

	username, err = resetTeamOfferTarget(nil, true)
	require.NoError(t, err)
	assert.Empty(t, username)

	_, err = resetTeamOfferTarget(nil, false)
	require.ErrorIs(t, err, errResetTeamOfferTarget)
	assert.EqualError(t, err, "pass a username or --all-users, exactly one of the two")
	_, err = resetTeamOfferTarget([]string{"alice"}, true)
	require.ErrorIs(t, err, errResetTeamOfferTarget)
	assert.EqualError(t, err, "pass a username or --all-users, exactly one of the two")

	_, err = resetTeamOfferTarget([]string{""}, false)
	require.ErrorIs(t, err, errEmptyUsername, "an empty username never means every user")
}

// newTeamOfferAPI serves the real API router, with the real @admin
// directive accepting teamOfferAdminToken, over yahoolink's PostgreSQL
// store, and returns its address.
func newTeamOfferAPI(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	passThrough := func(next http.Handler) http.Handler { return next }
	router := setupAPIRouter(nil, &graph.Resolver{TeamOffers: yahoolink.NewPGStore(pool)},
		appSessions{filter: passThrough, resolve: passThrough},
		apiAuth{admin: graph.AdminAuth{Token: teamOfferAdminToken}})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server.URL
}

// newTeamOfferClient is a GraphQLClient of addr sending token as the admin
// token (none when empty).
func newTeamOfferClient(addr, token string) *GraphQLClient {
	client := NewGraphQLClient(addr)
	client.adminToken = token
	client.apiPassword = ""
	return client
}

// insertResetOfferUser inserts a user of instance whose team offer was
// seen when seen is set.
func insertResetOfferUser(t *testing.T, pool *pgxpool.Pool, instance, username string, seen bool) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO app_users (instance, auth_provider, auth_subject, username, team_offer_seen_at)
		 VALUES ($1, 'authentik', $2, $3, CASE WHEN $4::boolean THEN now() END) RETURNING id`,
		instance, fmt.Sprintf("sub-%d", newMauriceTeamsLeagueID()), username, seen).Scan(&id))
	return id
}

// teamOfferSeen reports whether user's team offer flag is set.
func teamOfferSeen(t *testing.T, pool *pgxpool.Pool, user pgtype.UUID) bool {
	t.Helper()
	var seen bool
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT team_offer_seen_at IS NOT NULL FROM app_users WHERE id = $1`, user).Scan(&seen))
	return seen
}

// newResetOfferInstance returns an instance name no earlier test run used:
// the test database is shared and never truncated.
func newResetOfferInstance() string { return fmt.Sprintf("reset-offer-%d", newMauriceTeamsLeagueID()) }

func TestResetTeamOffer_OneUser(t *testing.T) {
	pool := pgtest.Open(t)
	api := newTeamOfferClient(newTeamOfferAPI(t, pool), teamOfferAdminToken)
	ctx := context.Background()
	instance := newResetOfferInstance()
	alice := insertResetOfferUser(t, pool, instance, "alice", true)
	bob := insertResetOfferUser(t, pool, instance, "bob", true)

	var out bytes.Buffer
	require.NoError(t, resetTeamOffer(ctx, &out, api, instance, "alice"))
	assert.Equal(t, "Reset alice's team offer; it opens again at their next visit.\n", out.String())
	assert.False(t, teamOfferSeen(t, pool, alice))
	assert.True(t, teamOfferSeen(t, pool, bob), "another user's offer stays seen")

	out.Reset()
	require.NoError(t, resetTeamOffer(ctx, &out, api, instance, "alice"))
	assert.Equal(t, "alice has not seen the team offer yet; it opens at their next visit.\n", out.String())
}

// The server's "no such user" reaches the command as yahoolink's error,
// with its message unchanged.
func TestResetTeamOffer_UnknownUser(t *testing.T) {
	pool := pgtest.Open(t)
	api := newTeamOfferClient(newTeamOfferAPI(t, pool), teamOfferAdminToken)
	instance := newResetOfferInstance()
	elsewhere := insertResetOfferUser(t, pool, newResetOfferInstance(), "alice", true)

	var out bytes.Buffer
	err := resetTeamOffer(context.Background(), &out, api, instance, "alice")
	require.ErrorIs(t, err, yahoolink.ErrUserNotFound, "a user of another instance is not found")
	assert.EqualError(t, err, fmt.Sprintf(`no such user: "alice" on instance %q`, instance))
	assert.Empty(t, out.String())
	assert.True(t, teamOfferSeen(t, pool, elsewhere))
}

func TestResetTeamOffer_AmbiguousUser(t *testing.T) {
	pool := pgtest.Open(t)
	api := newTeamOfferClient(newTeamOfferAPI(t, pool), teamOfferAdminToken)
	instance := newResetOfferInstance()
	twins := []pgtype.UUID{
		insertResetOfferUser(t, pool, instance, "twin", true),
		insertResetOfferUser(t, pool, instance, "twin", true),
	}

	var out bytes.Buffer
	err := resetTeamOffer(context.Background(), &out, api, instance, "twin")
	require.ErrorIs(t, err, yahoolink.ErrAmbiguousUser)
	assert.EqualError(t, err, fmt.Sprintf(`username matches more than one user: "twin" on instance %q`, instance))
	assert.Empty(t, out.String())
	for _, u := range twins {
		assert.True(t, teamOfferSeen(t, pool, u), "an ambiguous name resets nobody")
	}
}

// The server refuses an empty username too, so a client that skipped
// resetTeamOfferTarget still resets nobody.
func TestResetTeamOffer_EmptyUsernameResetsNobody(t *testing.T) {
	pool := pgtest.Open(t)
	api := newTeamOfferClient(newTeamOfferAPI(t, pool), teamOfferAdminToken)
	instance := newResetOfferInstance()
	alice := insertResetOfferUser(t, pool, instance, "alice", true)

	err := resetTeamOffer(context.Background(), &bytes.Buffer{}, api, instance, "")
	assert.EqualError(t, err, "graphql error: username must not be empty")
	assert.True(t, teamOfferSeen(t, pool, alice), "an empty username resets no user")
}

func TestResetTeamOffer_AllUsers(t *testing.T) {
	pool := pgtest.Open(t)
	api := newTeamOfferClient(newTeamOfferAPI(t, pool), teamOfferAdminToken)
	instance := newResetOfferInstance()
	seen := []pgtype.UUID{
		insertResetOfferUser(t, pool, instance, "alice", true),
		insertResetOfferUser(t, pool, instance, "bob", true),
	}
	pending := insertResetOfferUser(t, pool, instance, "carol", false)
	elsewhere := insertResetOfferUser(t, pool, newResetOfferInstance(), "dave", true)

	var out bytes.Buffer
	require.NoError(t, resetInstanceTeamOffers(context.Background(), &out, api, instance))
	assert.Equal(t, fmt.Sprintf("Reset the team offer of 2 user(s) on instance %q.\n", instance), out.String())
	for _, u := range append(seen, pending) {
		assert.False(t, teamOfferSeen(t, pool, u))
	}
	assert.True(t, teamOfferSeen(t, pool, elsewhere), "another instance's user keeps the offer seen")
}

// Without the admin token the server refuses both mutations and resets
// nobody.
func TestResetTeamOffer_NonAdminRefused(t *testing.T) {
	pool := pgtest.Open(t)
	addr := newTeamOfferAPI(t, pool)
	instance := newResetOfferInstance()
	alice := insertResetOfferUser(t, pool, instance, "alice", true)
	ctx := context.Background()

	for name, token := range map[string]string{"no token": "", "wrong token": "not-the-token"} {
		api := newTeamOfferClient(addr, token)
		var out bytes.Buffer
		err := resetTeamOffer(ctx, &out, api, instance, "alice")
		assert.EqualError(t, err, "graphql error: access denied: admin privileges required", name)
		err = resetInstanceTeamOffers(ctx, &out, api, instance)
		assert.EqualError(t, err, "graphql error: access denied: admin privileges required", name)
		assert.Empty(t, out.String(), name)
	}
	assert.True(t, teamOfferSeen(t, pool, alice))
}

func TestResetTeamOffers_Dispatch(t *testing.T) {
	pool := pgtest.Open(t)
	api := newTeamOfferClient(newTeamOfferAPI(t, pool), teamOfferAdminToken)
	ctx := context.Background()
	instance := newResetOfferInstance()
	alice := insertResetOfferUser(t, pool, instance, "alice", true)
	bob := insertResetOfferUser(t, pool, instance, "bob", true)

	var out bytes.Buffer
	require.NoError(t, resetTeamOffers(ctx, &out, api, instance, "alice", false))
	assert.Equal(t, "Reset alice's team offer; it opens again at their next visit.\n", out.String())
	assert.False(t, teamOfferSeen(t, pool, alice))
	assert.True(t, teamOfferSeen(t, pool, bob), "without --all-users only the named user is reset")

	out.Reset()
	require.NoError(t, resetTeamOffers(ctx, &out, api, instance, "", true))
	assert.Equal(t, fmt.Sprintf("Reset the team offer of 1 user(s) on instance %q.\n", instance), out.String())
	assert.False(t, teamOfferSeen(t, pool, bob), "--all-users resets every user of the instance")
}

// runResetTeamOfferCommand runs user reset-team-offer with args against
// the API at addr and returns its output. Not parallel: it resets viper.
func runResetTeamOfferCommand(t *testing.T, addr string, args ...string) (string, error) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set(config.FlagAPIServerAddr, addr)
	cmd := cmdUserResetTeamOffer()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// The command resets through the API with its flags, and never needs
// database flags.
func TestUserResetTeamOfferCommand_ThroughAPI(t *testing.T) {
	pool := pgtest.Open(t)
	addr := newTeamOfferAPI(t, pool)
	instance := newResetOfferInstance()
	alice := insertResetOfferUser(t, pool, instance, "alice", true)
	bob := insertResetOfferUser(t, pool, instance, "bob", true)

	out, err := runResetTeamOfferCommand(t, addr, "--instance", instance, "--admin-token", teamOfferAdminToken, "alice")
	require.NoError(t, err)
	assert.Equal(t, "Reset alice's team offer; it opens again at their next visit.\n", out)
	assert.False(t, teamOfferSeen(t, pool, alice))
	assert.True(t, teamOfferSeen(t, pool, bob))

	out, err = runResetTeamOfferCommand(t, addr, "--instance", instance, "--admin-token", teamOfferAdminToken, "--all-users")
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("Reset the team offer of 1 user(s) on instance %q.\n", instance), out)
	assert.False(t, teamOfferSeen(t, pool, bob))
}

// The command checks its target and --instance before calling the API: an
// address nothing listens on is never reached.
func TestUserResetTeamOfferCommand_ValidatesBeforeCalling(t *testing.T) {
	const unreachable = "http://127.0.0.1:1"
	_, err := runResetTeamOfferCommand(t, unreachable, "alice")
	require.ErrorIs(t, err, errInstanceRequired)
	assert.EqualError(t, err, "--instance is required")

	_, err = runResetTeamOfferCommand(t, unreachable, "--instance", "puckdb")
	require.ErrorIs(t, err, errResetTeamOfferTarget)
	_, err = runResetTeamOfferCommand(t, unreachable, "--instance", "puckdb", "--all-users", "alice")
	require.ErrorIs(t, err, errResetTeamOfferTarget)
	_, err = runResetTeamOfferCommand(t, unreachable, "--instance", "puckdb", "")
	require.ErrorIs(t, err, errEmptyUsername)

	_, err = runResetTeamOfferCommand(t, "", "--instance", "puckdb", "alice")
	assert.EqualError(t, err, "api-server-addr is required")
}

// --admin-group is read only by the API server, so the client command
// refuses it instead of accepting a value that grants nothing.
func TestUserResetTeamOfferCommand_RefusesAdminGroup(t *testing.T) {
	_, err := runResetTeamOfferCommand(t, "http://127.0.0.1:1", "--instance", "puckdb", "--admin-group", "ops", "alice")
	assert.EqualError(t, err, "unknown flag: --admin-group")
}
