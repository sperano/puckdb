package maurice

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Detached, bounded, awaited title generation ---

// gatedLLM lets a test hold the title-generation Complete call until the test
// releases it, and records the context error observed at that moment. Requests
// with MaxTokens == titleMaxTokens are treated as the title call.
type gatedLLM struct {
	proceed     chan struct{}
	observedErr chan error
}

func (g *gatedLLM) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if req.MaxTokens != titleMaxTokens {
		return &llm.Response{Content: "answer"}, nil
	}
	<-g.proceed
	err := ctx.Err()
	g.observedErr <- err
	if err != nil {
		return nil, err
	}
	return &llm.Response{Content: "My Title"}, nil
}

// Title generation must survive cancellation of the request context (it derives
// from context.WithoutCancel), and Close must await it.
func TestChat_TitleGeneration_DetachedFromRequestContext(t *testing.T) {
	db := newTestDB(t)
	g := &gatedLLM{proceed: make(chan struct{}), observedErr: make(chan error, 1)}
	svc := NewService(g, newMockMCP(), db, testConfig)

	ctx, cancel := context.WithCancel(context.Background())
	resp, err := svc.Chat(ctx, ask("hi")) // returns immediately; title goroutine now blocked on proceed
	require.NoError(t, err)

	// Cancel the request context, then let the title call observe its own
	// context. WithoutCancel means the title context is still live.
	cancel()
	close(g.proceed)
	gotErr := <-g.observedErr // read before Close so shutdown can't be the cause
	assert.NoError(t, gotErr, "title context must survive request-context cancellation")

	require.NoError(t, svc.Close()) // awaits the in-flight title goroutine

	convDetail, err := db.GetConversation(context.Background(), testUser, resp.ConversationID)
	require.NoError(t, err)
	require.NotNil(t, convDetail.Title)
	assert.Equal(t, "My Title", *convDetail.Title)
}

// The title call is recorded (its tokens count toward the user) with the
// stored prompt and answer as its inputs; a failed title call is recorded too.
func TestChat_TitleCallIsRecorded(t *testing.T) {
	for _, tc := range []struct {
		name      string
		titleErr  error
		wantTitle string
		status    CallStatus
	}{
		{"succeeded", nil, "Goal Leaders", CallSucceeded},
		{"failed", errors.New("title model down"), "", CallFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			titleResp := &llm.Response{Content: tc.wantTitle, Usage: &llm.Usage{PromptTokens: 30, CompletionTokens: 3}}
			llmMock := &mockLLMClient{
				responses: []*llm.Response{{Content: "answer"}, titleResp},
				errors:    []error{nil, tc.titleErr},
			}
			svc := NewService(llmMock, newMockMCP(), db, testConfig)
			resp, err := svc.Chat(context.Background(), ask("who leads?"))
			require.NoError(t, err)
			require.NoError(t, svc.Close())

			titles := db.titleRecords()
			require.Len(t, titles, 1)
			title := titles[0]
			assert.Equal(t, testUser, title.UserID)
			assert.Equal(t, resp.ConversationID, title.ConversationID)
			assert.Equal(t, tc.wantTitle, title.Title)
			assert.Equal(t, CallTitleGeneration, title.Call.Kind)
			assert.Nil(t, title.Call.Round)
			assert.Equal(t, tc.status, title.Call.Status)
			require.NotNil(t, title.Call.Instruction)
			assert.Equal(t, titleGenerationHint, *title.Call.Instruction)
			msgs, err := db.GetMessages(context.Background(), testUser, resp.ConversationID)
			require.NoError(t, err)
			assert.Equal(t, []MessageRef{{MessageID: msgs[0].ID}, {MessageID: msgs[1].ID}}, title.Call.Inputs)
		})
	}
}

// A title is recorded even when generation is cut off by shutdown.
func TestGenerateTitle_RecordsAfterCancellation(t *testing.T) {
	db := newTestDB(t)
	start := beginTurn(t, db, testUser, "", "q")
	ids := finishTurn(t, db, start, "q", TurnMessage{Role: "assistant", Content: "a"})
	svc := NewService(newBlockingLLM(), newMockMCP(), db, testConfig).(*service)
	svc.llmClient = &mockLLMClient{errors: []error{context.Canceled}}

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	<-ctx.Done()
	svc.generateTitle(ctx, titleJob{userID: testUser, conversationID: start.ConversationID,
		userMessageID: ids[0], answerID: ids[1], userMessage: "q", answer: "a"})

	titles := db.titleRecords()
	require.Len(t, titles, 1)
	assert.Equal(t, CallFailed, titles[0].Call.Status)
	assert.Equal(t, ErrorClassCancelled, titles[0].Call.ErrorClass)
}
