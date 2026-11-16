package maurice

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// committedTestDB is a testDB that reports every commit and title record.
func committedTestDB(t *testing.T) *testDB {
	db := newTestDB(t)
	db.committed = make(chan TurnRecord, 1)
	db.titled = make(chan TitleRecord, 1)
	return db
}

func askWithKey(message, key string) ChatRequest {
	req := ask(message)
	req.IdempotencyKey = key
	return req
}

// A request cancelled while its turn runs no longer cancels the turn: Chat
// returns the request's error, the turn commits its answer and the
// conversation is free for the next prompt.
func TestChat_CancelledRequestStillCommitsTurn(t *testing.T) {
	db := committedTestDB(t)
	convID := seedConversation(t, db, TurnMessage{Role: "user", Content: "q0"})
	blocker := newBlockingLLM()
	svc := NewService(blocker, newMockMCP(), db, testConfig)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var chatErr error
	wg.Go(func() { _, chatErr = svc.Chat(ctx, askIn(convID, "outlives the request")) })
	<-blocker.entered
	cancel()
	wg.Wait()
	require.ErrorIs(t, chatErr, context.Canceled)

	close(blocker.release)
	record := <-db.committed
	assert.Equal(t, TurnSucceeded, record.Status)
	msgs, err := db.GetMessages(context.Background(), testUser, convID)
	require.NoError(t, err)
	require.Len(t, msgs, 3, "seed prompt, new prompt, answer")
	assert.Equal(t, "outlives the request", msgs[1].Content)
	assert.Equal(t, "done", msgs[2].Content)

	_, err = svc.Chat(context.Background(), askIn(convID, "next"))
	require.NoError(t, err)
	require.NoError(t, svc.Close())
}

// StartChat returns while the turn runs, even when its request is cancelled
// at once; GetTurn reports it running, then succeeded with the answer.
func TestStartChat_StatusGoesRunningThenSucceeded(t *testing.T) {
	db := committedTestDB(t)
	blocker := newBlockingLLM()
	svc := NewService(blocker, newMockMCP(), db, testConfig)

	ctx, cancel := context.WithCancel(context.Background())
	ref, err := svc.StartChat(ctx, ask("slow question"))
	require.NoError(t, err)
	cancel()
	<-blocker.entered

	running, err := svc.GetTurn(context.Background(), testUser, ref.TurnID)
	require.NoError(t, err)
	assert.Equal(t, &Turn{ID: ref.TurnID, ConversationID: ref.ConversationID,
		TurnReplay: TurnReplay{Status: TurnRunning, Warnings: []string{}}}, running)

	close(blocker.release)
	<-db.committed
	done, err := svc.GetTurn(context.Background(), testUser, ref.TurnID)
	require.NoError(t, err)
	assert.Equal(t, TurnSucceeded, done.Status)
	assert.Equal(t, "done", done.Content)
	assert.Equal(t, lastMessageID(t, db, ref.ConversationID), done.MessageID)
	assert.Empty(t, done.ErrorClass)
	require.NoError(t, svc.Close())
}

func TestStartChat_FailedTurnReportsItsClass(t *testing.T) {
	db := committedTestDB(t)
	llmMock := &mockLLMClient{errors: []error{errors.New("upstream 500")}}
	svc := NewService(llmMock, newMockMCP(), db, testConfig)

	ref, err := svc.StartChat(context.Background(), ask("doomed"))
	require.NoError(t, err)
	<-db.committed
	turn, err := svc.GetTurn(context.Background(), testUser, ref.TurnID)
	require.NoError(t, err)
	assert.Equal(t, TurnFailed, turn.Status)
	assert.Equal(t, ErrorClassProvider, turn.ErrorClass)
	assert.Empty(t, turn.Content)
	require.NoError(t, svc.Close())
}

func TestGetTurn_OtherUserCannotReadTheTurn(t *testing.T) {
	db := committedTestDB(t)
	svc := NewService(&mockLLMClient{}, newMockMCP(), db, testConfig)
	ref, err := svc.StartChat(context.Background(), ask("mine"))
	require.NoError(t, err)
	<-db.committed

	_, err = svc.GetTurn(context.Background(), "user-b", ref.TurnID)
	require.ErrorIs(t, err, ErrTurnNotFound)
	require.NoError(t, svc.Close())
}

// A replayed key returns the turn it started, while it runs and after it
// finished, without running the prompt again; Chat keeps rejecting the key
// of a running turn.
func TestStartChat_ReplayedKeyReturnsTheSameTurn(t *testing.T) {
	db := committedTestDB(t)
	blocker := newBlockingLLM()
	svc := NewService(blocker, newMockMCP(), db, testConfig)

	ref, err := svc.StartChat(context.Background(), askWithKey("q", "key-1"))
	require.NoError(t, err)
	<-blocker.entered

	again, err := svc.StartChat(context.Background(), askWithKey("q", "key-1"))
	require.NoError(t, err)
	assert.Equal(t, ref, again, "replay of the running turn")
	_, err = svc.Chat(context.Background(), askWithKey("q", "key-1"))
	require.ErrorIs(t, err, ErrTurnInProgress)
	_, err = svc.StartChat(context.Background(), askWithKey("another prompt", "key-1"))
	require.ErrorIs(t, err, ErrIdempotencyKeyReused)

	close(blocker.release)
	<-db.committed
	after, err := svc.StartChat(context.Background(), askWithKey("q", "key-1"))
	require.NoError(t, err)
	assert.Equal(t, ref, after, "replay of the finished turn")
	require.NoError(t, svc.Close())
	assert.Len(t, db.finishedTurns(), 1, "the prompt ran once")
}

func TestStartChat_SecondPromptWhileRunningIsRejected(t *testing.T) {
	db := committedTestDB(t)
	convID := seedConversation(t, db, TurnMessage{Role: "user", Content: "q0"})
	blocker := newBlockingLLM()
	svc := NewService(blocker, newMockMCP(), db, testConfig)

	_, err := svc.StartChat(context.Background(), askIn(convID, "slow"))
	require.NoError(t, err)
	<-blocker.entered
	_, err = svc.StartChat(context.Background(), askIn(convID, "impatient"))
	require.ErrorIs(t, err, ErrTurnInProgress)

	close(blocker.release)
	<-db.committed
	require.NoError(t, svc.Close())
}

func TestStartChat_GeneratesTitleForNewConversation(t *testing.T) {
	db := committedTestDB(t)
	llmMock := &mockLLMClient{responses: []*llm.Response{{Content: "answer"}, {Content: "Goal Leaders"}}}
	svc := NewService(llmMock, newMockMCP(), db, testConfig)

	ref, err := svc.StartChat(context.Background(), ask("who leads in goals?"))
	require.NoError(t, err)
	<-db.committed
	title := <-db.titled
	assert.Equal(t, ref.ConversationID, title.ConversationID)
	conv, err := db.GetConversation(context.Background(), testUser, ref.ConversationID)
	require.NoError(t, err)
	require.NotNil(t, conv.Title)
	assert.Equal(t, "Goal Leaders", *conv.Title)
	require.NoError(t, svc.Close())
}

// Close cancels a running turn, which commits as cancelled.
func TestClose_CancelsRunningTurn(t *testing.T) {
	db := committedTestDB(t)
	blocker := newBlockingLLM()
	svc := NewService(blocker, newMockMCP(), db, testConfig)

	ref, err := svc.StartChat(context.Background(), ask("interrupted"))
	require.NoError(t, err)
	<-blocker.entered
	require.NoError(t, svc.Close())

	var record TurnRecord
	select {
	case record = <-db.committed:
	default:
		t.Fatal("Close returned before the turn committed")
	}
	assert.Equal(t, TurnCancelled, record.Status)
	require.Len(t, record.Calls, 1)
	assert.Equal(t, ErrorClassCancelled, record.Calls[0].ErrorClass)
	turn, err := svc.GetTurn(context.Background(), testUser, ref.TurnID)
	require.NoError(t, err)
	assert.Equal(t, TurnCancelled, turn.Status)
	assert.Equal(t, ErrorClassCancelled, turn.ErrorClass)
}

func TestClose_RejectsNewTurns(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(&mockLLMClient{}, newMockMCP(), db, testConfig)
	require.NoError(t, svc.Close())

	_, err := svc.StartChat(context.Background(), ask("late"))
	require.ErrorIs(t, err, ErrServiceClosed)
	_, err = svc.Chat(context.Background(), ask("late"))
	require.ErrorIs(t, err, ErrServiceClosed)
	convs, err := db.ListConversations(context.Background(), testUser, 10)
	require.NoError(t, err)
	assert.Empty(t, convs, "no turn was begun")
}

// A turn begun just before Close cannot run any more: it commits as
// cancelled instead of blocking its conversation until it goes stale.
func TestLaunchTurn_AfterCloseCommitsTheTurnCancelled(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(&mockLLMClient{}, newMockMCP(), db, testConfig).(*service)
	req := ask("raced with shutdown")
	start, release, err := svc.beginTurn(context.Background(), req, false)
	require.NoError(t, err)
	require.NoError(t, svc.Close())

	_, err = svc.launchTurn(context.Background(), req, start, release)
	require.ErrorIs(t, err, ErrServiceClosed)
	assert.Zero(t, svc.runner.running, "the turn's slot is free")
	turn, err := svc.GetTurn(context.Background(), testUser, start.TurnID)
	require.NoError(t, err)
	assert.Equal(t, TurnCancelled, turn.Status)
	assert.Equal(t, ErrorClassCancelled, turn.ErrorClass)
}

// The turn timeout is the only bound on a detached turn: a turn that
// outlasts it commits as failed with the timeout class.
func TestStartChat_TurnTimeoutFailsTheTurn(t *testing.T) {
	db := committedTestDB(t)
	blocker := newBlockingLLM()
	svc := NewService(blocker, newMockMCP(), db, testConfig).(*service)
	svc.turnTimeout = time.Millisecond

	ref, err := svc.StartChat(context.Background(), ask("never answered"))
	require.NoError(t, err)
	record := <-db.committed
	assert.Equal(t, TurnFailed, record.Status)
	assert.Equal(t, ErrorClassTimeout, record.ErrorClass)
	turn, err := svc.GetTurn(context.Background(), testUser, ref.TurnID)
	require.NoError(t, err)
	assert.Equal(t, TurnFailed, turn.Status)
	assert.Equal(t, ErrorClassTimeout, turn.ErrorClass)
	require.NoError(t, svc.Close())
}

// A user at the per-user cap cannot start another turn, yet a resent key
// still returns its running turn; other users keep their own slots.
func TestStartChat_PerUserTurnCap(t *testing.T) {
	db := committedTestDB(t)
	db.titled = make(chan TitleRecord, 2) // two new conversations
	blocker := newBlockingLLM()
	svc := NewService(blocker, newMockMCP(), db, testConfig).(*service)
	svc.runner.maxUserTurns = 1

	ref, err := svc.StartChat(context.Background(), askWithKey("q", "key-1"))
	require.NoError(t, err)
	<-blocker.entered

	again, err := svc.StartChat(context.Background(), askWithKey("q", "key-1"))
	require.NoError(t, err)
	assert.Equal(t, ref, again, "a resent key finds its turn at the cap")
	_, err = svc.StartChat(context.Background(), askWithKey("q", "key-2"))
	require.ErrorIs(t, err, ErrTooManyTurns)
	_, err = svc.Chat(context.Background(), ask("blocking prompt"))
	require.ErrorIs(t, err, ErrTooManyTurns)
	convs, err := db.ListConversations(context.Background(), testUser, 10)
	require.NoError(t, err)
	assert.Len(t, convs, 1, "a refused turn opens no conversation")

	other := ask("someone else")
	other.UserID = "user-b"
	_, err = svc.StartChat(context.Background(), other)
	require.NoError(t, err, "the cap is per user")
	<-blocker.entered

	close(blocker.release)
	<-db.committed
	<-db.committed
	require.NoError(t, svc.Close())
	assert.Zero(t, svc.runner.running, "finished turns free their slots")
	assert.Empty(t, svc.runner.userRunning)
}

func TestStartChat_ProcessTurnCap(t *testing.T) {
	db := committedTestDB(t)
	db.titled = make(chan TitleRecord, 2) // two new conversations
	blocker := newBlockingLLM()
	svc := NewService(blocker, newMockMCP(), db, testConfig).(*service)
	svc.runner.maxTurns = 1

	var wg sync.WaitGroup
	var firstErr error
	wg.Go(func() { _, firstErr = svc.Chat(context.Background(), ask("first")) })
	<-blocker.entered
	other := ask("second")
	other.UserID = "user-b"
	_, err := svc.StartChat(context.Background(), other)
	require.ErrorIs(t, err, ErrTooManyTurns)

	close(blocker.release)
	wg.Wait()
	require.NoError(t, firstErr)
	<-db.committed
	_, err = svc.StartChat(context.Background(), other)
	require.NoError(t, err, "the finished turn freed its slot")
	<-db.committed
	require.NoError(t, svc.Close())
}

// A turn that never runs does not keep its slot: a failed begin and a replay
// free it at once.
func TestBeginTurn_FreesTheSlotOfATurnThatDoesNotRun(t *testing.T) {
	db := committedTestDB(t)
	svc := NewService(&mockLLMClient{}, newMockMCP(), db, testConfig).(*service)
	_, err := svc.Chat(context.Background(), askWithKey("q", "key-1"))
	require.NoError(t, err)
	<-db.committed

	_, err = svc.StartChat(context.Background(), askWithKey("q", "key-1"))
	require.NoError(t, err)
	assert.Zero(t, svc.runner.running, "a replay holds no slot")

	db.beginErr = errors.New("database down")
	_, err = svc.StartChat(context.Background(), ask("q"))
	require.ErrorIs(t, err, db.beginErr)
	assert.Zero(t, svc.runner.running, "a failed begin holds no slot")
	require.NoError(t, svc.Close())
}
