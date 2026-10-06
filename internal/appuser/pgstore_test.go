package appuser

import (
	"context"
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
)

var pgMigrateOnce struct {
	sync.Once
	err error
}

func openStore(t *testing.T) (*PGStore, *pgxpool.Pool) {
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
	return NewPGStore(pool, SessionIdleTimeout), pool
}

// newSubject returns an Authentik subject no other test uses.
func newSubject() string { return "sub-" + uuid.NewString() }

func sessionRequest(subject string, presented []byte) SessionRequest {
	return SessionRequest{
		Identity:      Identity{Subject: subject, Username: "eric"},
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
