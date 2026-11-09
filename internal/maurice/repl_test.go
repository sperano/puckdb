package maurice

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sperano/puckdb/internal/llm"
)

// readLinesTestTimeout bounds how long a test waits for the readLines
// goroutine to deliver a line or close its channel.
const readLinesTestTimeout = 2 * time.Second

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
	err := RunREPL(context.Background(), REPLConfig{Models: replTestModels, InitialModel: replTestModels[0].ID})

	assert.ErrorIs(t, err, errREPLStreams)
}

func TestSpinnerStop_ClearsLineBeforeReturning(t *testing.T) {
	var out bytes.Buffer
	spin := &spinner{out: &out}

	spin.Start("thinking...")
	spin.Stop()

	// Stop waits for the animation goroutine, so its last write is
	// visible here without synchronisation.
	assert.True(t, strings.HasSuffix(out.String(), "\r\033[K"), "got %q", out.String())
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
