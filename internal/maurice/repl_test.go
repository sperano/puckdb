package maurice

import (
	"bytes"
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sperano/puckdb/internal/llm"
)

// readLinesTestTimeout bounds how long a test waits for the readLines
// goroutine to deliver a line or close its channel.
const readLinesTestTimeout = 2 * time.Second

// goroutinePollInterval is how often a test rechecks the goroutine count.
const goroutinePollInterval = 10 * time.Millisecond

func TestReadLines_DeliversLinesThenClosesOnEOF(t *testing.T) {
	lines, errFn := readLines(context.Background(), strings.NewReader("first\nsecond\n"))

	require.Equal(t, "first", receiveLine(t, lines))
	require.Equal(t, "second", receiveLine(t, lines))

	select {
	case _, ok := <-lines:
		assert.False(t, ok, "channel should be closed after EOF")
	case <-time.After(readLinesTestTimeout):
		t.Fatal("channel was not closed after EOF")
	}
	assert.NoError(t, errFn())
}

func TestReadLines_ClosesAfterContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// A pipe that never reaches EOF: only cancellation can end the
	// goroutine. One line is written so the goroutine is past Scan and
	// blocked on delivery when the cancel lands.
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	go func() { _, _ = pw.Write([]byte("pending line\n")) }()

	lines, _ := readLines(ctx, pr)
	cancel()

	// The already-scanned line may or may not be delivered depending on
	// where the goroutine was when cancel landed; either way the channel
	// must close, proving the goroutine exits. If the line was delivered,
	// the goroutine is back inside Scan, which cancellation cannot
	// interrupt (documented on readLines) — close the writer so that Scan
	// ends and the channel closes.
	deadline := time.After(readLinesTestTimeout)
	for {
		select {
		case _, ok := <-lines:
			if !ok {
				return
			}
			_ = pw.Close()
		case <-deadline:
			t.Fatal("channel was not closed after context cancellation")
		}
	}
}

// receiveLine reads one line from lines, failing the test on close or
// timeout.
func receiveLine(t *testing.T, lines <-chan string) string {
	t.Helper()
	select {
	case line, ok := <-lines:
		require.True(t, ok, "channel closed before expected line")
		return line
	case <-time.After(readLinesTestTimeout):
		t.Fatal("timed out waiting for line")
		return ""
	}
}

func TestFindModel(t *testing.T) {
	t.Parallel()
	models := []ModelEntry{
		{ID: "model-a", Provider: llm.ProviderAnthropic},
		{ID: "model-b", Provider: llm.ProviderOllama},
	}

	tests := []struct {
		name    string
		models  []ModelEntry
		id      string
		want    ModelEntry
		wantErr error
	}{
		{name: "found", models: models, id: "model-b", want: models[1]},
		{name: "unknown id", models: models, id: "model-c", wantErr: ErrUnknownModel},
		{name: "empty id", models: models, id: "", wantErr: ErrUnknownModel},
		{name: "empty registry", models: nil, id: "model-a", wantErr: ErrUnknownModel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := FindModel(tt.models, tt.id)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRunREPL_DrivesCommandsAndChatFromInput(t *testing.T) {
	services := &serviceFactory{prototype: fakeService{convs: []*Conversation{
		{ID: "conv-1", Title: strPtr("Leafs goalies")},
	}}}
	var out, errOut bytes.Buffer
	cfg := REPLConfig{
		Models:       testREPLModels,
		InitialModel: "model-a",
		ListLimit:    testListLimit,
		In:           strings.NewReader("/history\n\n/load conv-1\n  hello  \n"),
		Out:          &out,
		Err:          &errOut,
	}

	require.NoError(t, runREPL(context.Background(), cfg, services.build), "EOF ends the REPL cleanly")

	require.Len(t, services.built, 1)
	svc := services.built[0]
	assert.Equal(t, "model-a", svc.model)
	assert.Equal(t, 1, svc.closed, "the service is closed on return")
	assert.Equal(t, []int{testListLimit}, svc.listLimits)
	require.Len(t, svc.chats, 1)
	assert.Equal(t, "hello", svc.chats[0].Message, "input lines are trimmed")
	require.NotNil(t, svc.chats[0].ConversationID)
	assert.Equal(t, "conv-1", *svc.chats[0].ConversationID)

	got := out.String()
	assert.Contains(t, got, "Maurice — Hockey AI Chat")
	assert.Contains(t, got, "(model-a)", "the prompt names the model")
	assert.Contains(t, got, "Leafs goalies")
	assert.Contains(t, got, "Resumed: Leafs goalies")
	assert.Contains(t, got, "answer")
	assert.Empty(t, errOut.String())
}

func TestRunREPL_QuitAfterModelSwitchClosesEveryService(t *testing.T) {
	services := &serviceFactory{}
	var out bytes.Buffer
	cfg := REPLConfig{
		Models:       testREPLModels,
		InitialModel: "model-a",
		In:           strings.NewReader("/model 2\n/quit\nnever sent\n"),
		Out:          &out,
		Err:          io.Discard,
	}

	require.NoError(t, runREPL(context.Background(), cfg, services.build))

	require.Len(t, services.built, 2)
	for _, svc := range services.built {
		assert.Equal(t, 1, svc.closed, "service %s", svc.model)
		assert.Empty(t, svc.chats, "nothing after /quit is read")
	}
	assert.Contains(t, out.String(), "(model-b)", "the prompt follows the switch")
}

func TestRunREPL_QuitReleasesInputGoroutine(t *testing.T) {
	baseline := runtime.NumGoroutine()
	cfg := REPLConfig{
		Models:       testREPLModels,
		InitialModel: "model-a",
		// After /quit the input goroutine has scanned "never sent" and
		// blocks delivering it until the REPL cancels its context.
		In:  strings.NewReader("/quit\nnever sent\n"),
		Out: io.Discard,
		Err: io.Discard,
	}

	require.NoError(t, runREPL(context.Background(), cfg, (&serviceFactory{}).build))

	// A manual poll: assert.Eventually runs its condition on a goroutine of
	// its own, which the count would include.
	for deadline := time.Now().Add(readLinesTestTimeout); runtime.NumGoroutine() > baseline; {
		require.True(t, time.Now().Before(deadline), "the input goroutine is still running")
		time.Sleep(goroutinePollInterval)
	}
}

func TestRunREPL_UnknownInitialModel(t *testing.T) {
	services := &serviceFactory{}
	cfg := REPLConfig{Models: testREPLModels, InitialModel: "model-z", In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

	err := runREPL(context.Background(), cfg, services.build)

	require.ErrorIs(t, err, ErrUnknownModel)
	assert.Empty(t, services.built)
}

func TestRunREPL_ReturnsInputError(t *testing.T) {
	readErr := errors.New("tty gone")
	services := &serviceFactory{}
	cfg := REPLConfig{Models: testREPLModels, InitialModel: "model-a", In: iotest.ErrReader(readErr), Out: io.Discard, Err: io.Discard}

	err := runREPL(context.Background(), cfg, services.build)

	require.ErrorIs(t, err, readErr)
	assert.Equal(t, 1, services.last(t).closed)
}

func TestRunREPL_ReturnsOnContextCancel(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	services := &serviceFactory{}
	cfg := REPLConfig{Models: testREPLModels, InitialModel: "model-a", In: pr, Out: io.Discard, Err: io.Discard}

	done := make(chan error, 1)
	go func() { done <- runREPL(ctx, cfg, services.build) }()
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(readLinesTestTimeout):
		t.Fatal("RunREPL did not return after cancellation")
	}
	assert.Equal(t, 1, services.last(t).closed)
}

func TestREPLConfigModelService_Limits(t *testing.T) {
	const (
		explicitHistory    = 12
		explicitTokens     = 2048
		explicitToolRounds = 3
	)
	tests := []struct {
		name                          string
		cfg                           REPLConfig
		wantHistory, wantTokens, want int
	}{
		{
			name:        "explicit",
			cfg:         REPLConfig{MaxHistory: explicitHistory, MaxTokens: explicitTokens, MaxToolRounds: explicitToolRounds},
			wantHistory: explicitHistory, wantTokens: explicitTokens, want: explicitToolRounds,
		},
		{
			name:        "zero falls back to defaults",
			cfg:         REPLConfig{},
			wantHistory: DefaultMaxHistory, wantTokens: DefaultMaxTokens, want: DefaultMaxToolRounds,
		},
		{
			name:        "negative falls back to defaults",
			cfg:         REPLConfig{MaxHistory: -1, MaxTokens: -1, MaxToolRounds: -1},
			wantHistory: DefaultMaxHistory, wantTokens: DefaultMaxTokens, want: DefaultMaxToolRounds,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cfg.DB = newTestDB(t)
			tt.cfg.MCPClient = newMockMCP()

			svc, ok := tt.cfg.modelService(testREPLModels[1]).(*service)
			require.True(t, ok)
			t.Cleanup(func() { require.NoError(t, svc.Close()) })

			assert.Equal(t, tt.wantHistory, svc.maxHistory)
			assert.Equal(t, tt.wantTokens, svc.maxTokens)
			assert.Equal(t, tt.want, svc.maxToolRounds)
			assert.Equal(t, "model-b", svc.model)
			assert.Equal(t, llm.ProviderOllama.String(), svc.provider)
		})
	}
}

// replTestModels are the models the REPL tests can switch between; Ollama
// needs no API key, so switching prints no warning.
var replTestModels = []ModelEntry{
	{ID: "first-model", Description: "first", Provider: llm.ProviderOllama},
	{ID: "second-model", Description: "second", Provider: llm.ProviderOllama},
}

// runTestREPL runs the REPL on input with an in-memory SQLite store and
// returns what it wrote to Out and Err. No prompt reaches the model.
func runTestREPL(t *testing.T, input string) (string, string) {
	t.Helper()
	out, errOut, err := runTestREPLOn(context.Background(), openTestDB(t), strings.NewReader(input))
	require.NoError(t, err)
	return out, errOut
}

// runTestREPLOn runs the REPL on in with db and returns what it wrote to
// Out and Err and its error.
func runTestREPLOn(ctx context.Context, db DB, in io.Reader) (string, string, error) {
	var out, errOut bytes.Buffer
	err := RunREPL(ctx, REPLConfig{
		DB:              db,
		Models:          replTestModels,
		InitialModel:    replTestModels[0].ID,
		ProviderConfigs: map[llm.Provider]llm.ProviderConfig{llm.ProviderOllama: {BaseURL: "http://127.0.0.1:1/v1"}},
		ListLimit:       10,
		In:              in,
		Out:             &out,
		Err:             &errOut,
	})
	return out.String(), errOut.String(), err
}

// seedREPLConversation stores a conversation of LocalUserID with one answered
// turn, titled title unless it is empty, and returns its ID.
func seedREPLConversation(t *testing.T, db DB, title string) string {
	t.Helper()
	start := beginTurn(t, db, LocalUserID, "", "hello")
	ids := finishTurn(t, db, start, "hello", TurnMessage{Role: "assistant", Content: "hi"})
	if title != "" {
		require.NoError(t, db.RecordTitle(context.Background(), TitleRecord{
			UserID: LocalUserID, ConversationID: start.ConversationID, Title: title, Call: titleCallRecord(ids),
		}))
	}
	return start.ConversationID
}

func TestRunREPL_WritesCommandsToConfiguredStreams(t *testing.T) {
	out, errOut := runTestREPL(t, "/new\n/history\n/model 9\n/model 2\n/load missing-id\n/quit\n")

	assert.Contains(t, out, "Maurice — Hockey AI Chat")
	assert.Contains(t, out, "Started new conversation.")
	assert.Contains(t, out, "No conversations yet.")
	assert.Contains(t, out, "Invalid selection: 9")
	assert.Contains(t, out, "Switched to")
	assert.Contains(t, out, "(second-model)", "the prompt after the switch names the new model")
	assert.NotContains(t, out, "Error:", "errors go to Err, not Out")
	assert.Contains(t, errOut, "Error:", "the failed /load is reported on Err")
}

func TestRunREPL_EndsAtEndOfInput(t *testing.T) {
	out, errOut := runTestREPL(t, "/new\n")

	assert.Contains(t, out, "Started new conversation.")
	assert.Empty(t, errOut)
}

func TestRunREPL_RequiresStreams(t *testing.T) {
	tests := []struct {
		name string
		nil  func(cfg *REPLConfig)
	}{
		{name: "in", nil: func(cfg *REPLConfig) { cfg.In = nil }},
		{name: "out", nil: func(cfg *REPLConfig) { cfg.Out = nil }},
		{name: "err", nil: func(cfg *REPLConfig) { cfg.Err = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := REPLConfig{
				Models:       replTestModels,
				InitialModel: replTestModels[0].ID,
				In:           strings.NewReader("/quit\n"),
				Out:          &bytes.Buffer{},
				Err:          &bytes.Buffer{},
			}
			tt.nil(&cfg)
			built := false

			err := runREPL(context.Background(), cfg, func(ModelEntry) Service {
				built = true
				return nil
			})

			assert.ErrorIs(t, err, errREPLStreams)
			assert.False(t, built, "no service is built without every stream")
		})
	}
}

func TestRunREPL_HistoryListsConversations(t *testing.T) {
	db := openTestDB(t)
	untitled := seedREPLConversation(t, db, "")
	titled := seedREPLConversation(t, db, "Hockey Chat")

	out, errOut, err := runTestREPLOn(context.Background(), db, strings.NewReader("/history\n/quit\n"))

	require.NoError(t, err)
	assert.Empty(t, errOut)
	assert.Contains(t, out, untitled)
	assert.Contains(t, out, "(untitled)")
	assert.Contains(t, out, titled)
	assert.Contains(t, out, "Hockey Chat")
	assert.NotContains(t, out, "No conversations yet.")
}

func TestRunREPL_LoadResumesConversation(t *testing.T) {
	db := openTestDB(t)
	id := seedREPLConversation(t, db, "Hockey Chat")

	out, errOut, err := runTestREPLOn(context.Background(), db, strings.NewReader("/load "+id+"\n/quit\n"))

	require.NoError(t, err)
	assert.Empty(t, errOut)
	assert.Contains(t, out, "Resumed: Hockey Chat")
}

func TestRunREPL_LoadWithoutIDPrintsUsage(t *testing.T) {
	for _, input := range []string{"/load\n/quit\n", "/load   \n/quit\n"} {
		out, errOut := runTestREPL(t, input)

		assert.Contains(t, out, "Usage: /load <conversation-id>", "input %q", input)
		assert.Empty(t, errOut, "input %q: nothing is sent to the model", input)
	}
}

func TestRunREPL_ReturnsOnCancellation(t *testing.T) {
	in, w := io.Pipe()
	t.Cleanup(func() { w.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out, errOut, err := runTestREPLOn(ctx, openTestDB(t), in)

	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(out, formatPrompt(replTestModels[0].ID)+"\n"), "got %q", out)
	assert.Empty(t, errOut)
}
