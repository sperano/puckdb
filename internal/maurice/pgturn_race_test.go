package maurice

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PostgreSQL-only lock behaviour of BeginTurn and FinishTurn between
// concurrent transactions.

const (
	// lockWaitTimeout bounds waiting for a backend to block on a lock.
	lockWaitTimeout = 10 * time.Second
	// lockWaitPoll is how often pg_stat_activity is checked meanwhile.
	lockWaitPoll = 5 * time.Millisecond
)

// waitForLockWait returns once a backend of the test database waits on a
// lock, so the test knows the concurrent call reached its blocking statement.
func waitForLockWait(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	require.Eventually(t, func() bool {
		var waiting int
		err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`,
		).Scan(&waiting)
		return err == nil && waiting > 0
	}, lockWaitTimeout, lockWaitPoll)
}

// A start racing its own retry: the loser's insert waits on the winner's
// uncommitted turn under the same key, then hits the key constraint. It looks
// again and replays the winner's running turn instead of failing, and its
// own new conversation rolls back.
func TestPg_SameKeyRaceReplaysTheWinner(t *testing.T) {
	pool := openPgTestPool(t)
	user := newPgUser(t, pool)
	userID, err := parseUUID(user)
	require.NoError(t, err)
	ctx := context.Background()
	params := turnParams(user, "", "raced-key", "q")
	params.ReplayRunning = true

	winner, err := pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = winner.Rollback(ctx) })
	q := sqlcdb.New(winner)
	conv, err := q.CreateConversation(ctx, userID)
	require.NoError(t, err)
	turn, err := q.InsertTurn(ctx, sqlcdb.InsertTurnParams{
		ConversationID: conv.ID, UserID: userID, IdempotencyKey: params.IdempotencyKey, RequestHash: params.RequestHash,
	})
	require.NoError(t, err)

	loser := NewPgDB(newPgPool(t))
	var start *TurnStart
	var beginErr error
	var wg sync.WaitGroup
	wg.Go(func() { start, beginErr = loser.BeginTurn(ctx, params) })
	waitForLockWait(t, pool)
	require.NoError(t, winner.Commit(ctx))
	wg.Wait()

	require.NoError(t, beginErr)
	require.NotNil(t, start.Replay)
	assert.Equal(t, uuidToString(turn.ID), start.TurnID)
	assert.Equal(t, uuidToString(conv.ID), start.ConversationID)
	assert.Equal(t, TurnRunning, start.Replay.Status)
	assert.Equal(t, 1, pgCount(t, pool, `SELECT count(*) FROM maurice_conversations WHERE user_id = $1`, user))
}

// FinishTurn takes the conversation lock before the turn row, as BeginTurn
// does: while a transaction holds the conversation, the commit waits without
// holding the turn, so that transaction can still reach the turn (a BeginTurn
// inserting the next running turn waits on it) instead of deadlocking.
func TestPg_FinishTurnLocksTheConversationFirst(t *testing.T) {
	pool := openPgTestPool(t)
	user := newPgUser(t, pool)
	ctx := context.Background()
	start := beginTurn(t, NewPgDB(pool), user, "", "q")

	holder, err := pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Rollback(ctx) })
	_, err = holder.Exec(ctx, `SELECT id FROM maurice_conversations WHERE id = $1 FOR UPDATE`, start.ConversationID)
	require.NoError(t, err)

	finisher := NewPgDB(newPgPool(t))
	var finishErr error
	var wg sync.WaitGroup
	wg.Go(func() {
		_, finishErr = finisher.FinishTurn(ctx, TurnRecord{
			TurnID: start.TurnID, ConversationID: start.ConversationID, Status: TurnSucceeded,
			Messages: []TurnMessage{{Role: "user", Content: "q"}, {Role: "assistant", Content: "a"}},
		})
	})
	waitForLockWait(t, pool)
	_, err = holder.Exec(ctx, `SELECT id FROM maurice_turns WHERE id = $1 FOR UPDATE NOWAIT`, start.TurnID)
	assert.NoError(t, err, "the waiting commit holds no lock on the turn")
	require.NoError(t, holder.Rollback(ctx))
	wg.Wait()

	require.NoError(t, finishErr)
	assert.Equal(t, 1, pgCount(t, pool, `SELECT count(*) FROM maurice_turns WHERE id = $1 AND status = 'succeeded'`, start.TurnID))
}
