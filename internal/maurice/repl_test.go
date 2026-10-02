package maurice

import (
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
