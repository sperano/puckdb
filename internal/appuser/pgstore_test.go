package appuser

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envTestPGURL names the throwaway PostgreSQL these tests migrate and
// truncate (see CLAUDE.md, "Database-backed tests"); unset skips them.
const (
	envTestPGURL     = "PUCKDB_TEST_PG_URL"
	testDBNameMarker = "test"
	// concurrentResolves is how many cookie-less requests of one user race.
	concurrentResolves = 8
	// testInstance and otherInstance are two API instances sharing the
	// database.
	testInstance  = "puckdb"
	otherInstance = "maurice-test"
)

var pgMigrateOnce struct {
	sync.Once
	err error
}

func openStore(t *testing.T) (*PGStore, *pgxpool.Pool) {
	t.Helper()
	pool := openPool(t)
	return newStore(t, pool, testInstance), pool
}

func newStore(t *testing.T, pool *pgxpool.Pool, instance string) *PGStore {
	t.Helper()
	store, err := NewPGStore(pool, instance, SessionIdleTimeout)
	require.NoError(t, err)
	return store
}

func openPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv(envTestPGURL)
	if dbURL == "" {
		t.Skipf("set %s to run the PostgreSQL tests", envTestPGURL)
	}
	require.Contains(t, dbURL, testDBNameMarker, "%s must name a dedicated test database", envTestPGURL)
	pgMigrateOnce.Do(func() { pgMigrateOnce.err = database.MigrateUp(dbURL) })
	require.NoError(t, pgMigrateOnce.err)
	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	// No truncation: other packages' tests share the database concurrently.
	// Every test uses its own subjects (newSubject).
	return pool
}

// newSubject returns an Authentik subject no other test uses.
func newSubject() string { return "sub-" + uuid.NewString() }

func sessionRequest(subject string, presented []byte) SessionRequest {
	return SessionRequest{
		Identity:      Identity{Provider: ProviderAuthentik, Subject: subject, Username: "eric"},
		PresentedHash: presented,
		FreshHash:     []byte(uuid.NewString()),
	}
}

func countSessions(t *testing.T, pool *pgxpool.Pool, userID string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM app_sessions WHERE user_id = $1`, userID).Scan(&n))
	return n
}

func TestPGStore_SessionLifecycle(t *testing.T) {
	store, pool := openStore(t)
	ctx := context.Background()

	sub := newSubject()
	first := sessionRequest(sub, nil)
	res, err := store.Resolve(ctx, first)
	require.NoError(t, err)
	assert.True(t, res.Created, "the first request opens a session")
	user := res.User.ID

	again, err := store.Resolve(ctx, sessionRequest(sub, first.FreshHash))
	require.NoError(t, err)
	assert.False(t, again.Created, "the cookie continues the session")
	assert.Equal(t, res.User, again.User)

	cookieless, err := store.Resolve(ctx, sessionRequest(sub, nil))
	require.NoError(t, err)
	assert.False(t, cookieless.Created, "a client without cookies joins the active session")
	assert.Equal(t, res.User.SessionID, cookieless.User.SessionID)

	unknown, err := store.Resolve(ctx, sessionRequest(sub, []byte("forged")))
	require.NoError(t, err)
	assert.False(t, unknown.Created)
	assert.Equal(t, res.User.SessionID, unknown.User.SessionID)
	assert.Equal(t, 1, countSessions(t, pool, user))

	// Idle past the timeout: the session ends when its window closed and the
	// next request opens a new one.
	_, err = pool.Exec(ctx, `UPDATE app_sessions SET started_at = started_at - make_interval(secs => $2),
		last_activity_at = last_activity_at - make_interval(secs => $2) WHERE user_id = $1`,
		user, (SessionIdleTimeout + time.Minute).Seconds())
	require.NoError(t, err)
	expired, err := store.Resolve(ctx, sessionRequest(sub, first.FreshHash))
	require.NoError(t, err)
	assert.True(t, expired.Created)
	assert.NotEqual(t, res.User.SessionID, expired.User.SessionID)
	assert.Equal(t, 2, countSessions(t, pool, user), "puckdb_session_count counts application sessions")
	var ended int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM app_sessions WHERE id = $1 AND ended_at = last_activity_at + make_interval(secs => $2)`,
		res.User.SessionID, SessionIdleTimeout.Seconds()).Scan(&ended))
	assert.Equal(t, 1, ended)
}

// Another user's cookie does not resolve to that user's session.
func TestPGStore_CookieIsBoundToItsUser(t *testing.T) {
	store, _ := openStore(t)
	ctx := context.Background()
	alice := sessionRequest(newSubject(), nil)
	a, err := store.Resolve(ctx, alice)
	require.NoError(t, err)

	b, err := store.Resolve(ctx, sessionRequest(newSubject(), alice.FreshHash))
	require.NoError(t, err)
	assert.NotEqual(t, a.User.ID, b.User.ID)
	assert.NotEqual(t, a.User.SessionID, b.User.SessionID)
	assert.True(t, b.Created)
}

func TestNewPGStore_RequiresAnInstance(t *testing.T) {
	_, err := NewPGStore(nil, "", SessionIdleTimeout)
	require.ErrorIs(t, err, ErrNoInstance)
}

// The same identity on two instances is two users: one's session cookie does
// not carry over to the other instance. Conversations are scoped by user, so
// they follow (internal/maurice's PostgreSQL contract tests that scoping).
func TestPGStore_UsersArePerInstance(t *testing.T) {
	store, pool := openStore(t)
	other := newStore(t, pool, otherInstance)
	ctx := context.Background()
	sub := newSubject()
	first := sessionRequest(sub, nil)
	main, err := store.Resolve(ctx, first)
	require.NoError(t, err)

	elsewhere, err := other.Resolve(ctx, sessionRequest(sub, first.FreshHash))
	require.NoError(t, err)
	assert.NotEqual(t, main.User.ID, elsewhere.User.ID)
	assert.NotEqual(t, main.User.SessionID, elsewhere.User.SessionID)
	assert.True(t, elsewhere.Created, "the other instance's cookie opens no session here")

	again, err := store.Resolve(ctx, sessionRequest(sub, first.FreshHash))
	require.NoError(t, err)
	assert.Equal(t, main.User, again.User, "each instance keeps its own user and session")

	for id, instance := range map[string]string{main.User.ID: testInstance, elsewhere.User.ID: otherInstance} {
		var stored string
		require.NoError(t, pool.QueryRow(ctx, `SELECT instance FROM app_users WHERE id = $1`, id).Scan(&stored))
		assert.Equal(t, instance, stored)
	}
}

func TestPGStore_ProfileIsASnapshot(t *testing.T) {
	store, pool := openStore(t)
	ctx := context.Background()
	sub := newSubject()
	res, err := store.Resolve(ctx, sessionRequest(sub, nil))
	require.NoError(t, err)

	renamed := sessionRequest(sub, nil)
	renamed.Identity.Username, renamed.Identity.DisplayName = "eric2", "Eric Two"
	again, err := store.Resolve(ctx, renamed)
	require.NoError(t, err)
	assert.Equal(t, res.User.ID, again.User.ID, "the subject, not the username, is the identity")

	var username, display string
	require.NoError(t, pool.QueryRow(ctx, `SELECT username, display_name FROM app_users WHERE id = $1`, res.User.ID).Scan(&username, &display))
	assert.Equal(t, "eric2", username)
	assert.Equal(t, "Eric Two", display)
}

func TestPGStore_DisabledUserIsRefused(t *testing.T) {
	store, pool := openStore(t)
	ctx := context.Background()
	sub := newSubject()
	res, err := store.Resolve(ctx, sessionRequest(sub, nil))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE app_users SET disabled_at = now() WHERE id = $1`, res.User.ID)
	require.NoError(t, err)

	_, err = store.Resolve(ctx, sessionRequest(sub, nil))
	require.ErrorIs(t, err, ErrUserDisabled)
}

// Concurrent cookie-less requests of one new user agree on one session.
func TestPGStore_ConcurrentRequestsShareOneSession(t *testing.T) {
	store, pool := openStore(t)
	sub := newSubject()
	results := make([]Resolution, concurrentResolves)
	errs := make([]error, concurrentResolves)
	barrier := make(chan struct{})
	var wg sync.WaitGroup
	for i := range concurrentResolves {
		wg.Go(func() {
			<-barrier
			results[i], errs[i] = store.Resolve(context.Background(), sessionRequest(sub, nil))
		})
	}
	close(barrier)
	wg.Wait()

	created := 0
	for i := range concurrentResolves {
		require.NoError(t, errs[i])
		assert.Equal(t, results[0].User, results[i].User)
		if results[i].Created {
			created++
		}
	}
	assert.Equal(t, 1, created)
	assert.Equal(t, 1, countSessions(t, pool, results[0].User.ID))
}

// A Cloudflare identity keeps its email; the provider is part of the key, so
// the same subject under Authentik is another user.
func TestPGStore_CloudflareUserStoresEmail(t *testing.T) {
	store, pool := openStore(t)
	ctx := context.Background()
	sub := newSubject()
	req := sessionRequest(sub, nil)
	req.Identity = Identity{Provider: ProviderCloudflare, Subject: sub, Username: "eric@example.com", Email: "eric@example.com"}
	cf, err := store.Resolve(ctx, req)
	require.NoError(t, err)

	authentik, err := store.Resolve(ctx, sessionRequest(sub, nil))
	require.NoError(t, err)
	assert.NotEqual(t, cf.User.ID, authentik.User.ID)

	changed := sessionRequest(sub, nil)
	changed.Identity = req.Identity
	changed.Identity.Email = "eric@example.org"
	again, err := store.Resolve(ctx, changed)
	require.NoError(t, err)
	assert.Equal(t, cf.User.ID, again.User.ID)

	var provider string
	var email, authentikEmail *string
	var profileMoved bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT auth_provider, email, profile_updated_at > first_seen_at FROM app_users WHERE id = $1`, cf.User.ID).
		Scan(&provider, &email, &profileMoved))
	assert.Equal(t, ProviderCloudflare, provider)
	require.NotNil(t, email)
	assert.Equal(t, "eric@example.org", *email)
	assert.True(t, profileMoved, "an email change is a profile change")
	require.NoError(t, pool.QueryRow(ctx, `SELECT email FROM app_users WHERE id = $1`, authentik.User.ID).Scan(&authentikEmail))
	assert.Nil(t, authentikEmail, "no email is stored as NULL")
}

// newYahooLeagueID returns a Yahoo league ID no fixture elsewhere in the
// test suite uses: tests share the database and never truncate it.
func newYahooLeagueID() int32 { return 10_000_000 + rand.Int32N(1_000_000_000-10_000_000) }

// seedYahooTeamManager seeds a minimal league, team and manager email, for
// a test of Resolve's email link sync.
func seedYahooTeamManager(t *testing.T, pool *pgxpool.Pool, email string) (leagueID, teamID int32) {
	t.Helper()
	ctx := context.Background()
	leagueID, teamID = newYahooLeagueID(), 1
	key := fmt.Sprintf("465.l.%d", leagueID)
	_, err := pool.Exec(ctx, `INSERT INTO yahoo_leagues (id, league_key, name, season) VALUES ($1, $2, 'appuser test', 20262027)`, leagueID, key)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO yahoo_teams (league_id, id, team_key, name) VALUES ($1, $2, $3, 'Resolve Sync')`, leagueID, teamID, key+".t.1")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO yahoo_team_managers (league_id, team_id, id, nickname, email) VALUES ($1, $2, 1, 'Manager', $3)`, leagueID, teamID, email)
	require.NoError(t, err)
	return leagueID, teamID
}

// Resolve links the user's Yahoo team in the same transaction as the login
// upsert, and drops the link once the login email no longer matches.
func TestPGStore_ResolveSyncsYahooTeamLinks(t *testing.T) {
	store, pool := openStore(t)
	ctx := context.Background()
	sub := newSubject()
	email := fmt.Sprintf("resolve-sync-%s@example.com", uuid.NewString())
	seedYahooTeamManager(t, pool, email)

	first := sessionRequest(sub, nil)
	first.Identity.Email = email
	res, err := store.Resolve(ctx, first)
	require.NoError(t, err)
	assert.Equal(t, 1, countRows(t, pool,
		`SELECT count(*) FROM app_user_yahoo_teams WHERE user_id = $1 AND source = 'email'`, res.User.ID))

	changed := sessionRequest(sub, first.FreshHash)
	changed.Identity.Email = fmt.Sprintf("someoneelse-%s@example.com", uuid.NewString())
	_, err = store.Resolve(ctx, changed)
	require.NoError(t, err)
	assert.Equal(t, 0, countRows(t, pool, `SELECT count(*) FROM app_user_yahoo_teams WHERE user_id = $1`, res.User.ID))
}

func countRows(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), sql, args...).Scan(&n))
	return n
}

func TestPGStore_RequiresAProvider(t *testing.T) {
	store, err := NewPGStore(nil, testInstance, SessionIdleTimeout)
	require.NoError(t, err)
	req := sessionRequest(newSubject(), nil)
	req.Identity.Provider = ""
	_, err = store.Resolve(context.Background(), req)
	require.ErrorIs(t, err, errNoProvider)
}
