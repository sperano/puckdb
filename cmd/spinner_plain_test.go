package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const plainTestEvery = time.Minute

// fakeClock is a settable time source for plainProgress.now.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// plainSpinner is a plain spinner writing to buf, on clock.
func plainSpinner(buf *bytes.Buffer, clock *fakeClock) *spinner {
	sp := newSpinner(buf, "Starting...")
	sp.SetPlain(plainTestEvery)
	sp.plain.now = clock.now
	return sp
}

func TestIsTerminal(t *testing.T) {
	assert.False(t, isTerminal(&bytes.Buffer{}))
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	assert.False(t, isTerminal(f), "a regular file is not a terminal")
}

func TestPlainText(t *testing.T) {
	msg := "Fetching 2026-2027...\n" + SpinnerPlaceholder + " [" + ProgressShade0 + "██" + ProgressShadeEnd + "░░] 2/4 50%  "
	assert.Equal(t, "Fetching 2026-2027...\n [██░░] 2/4 50%", plainText(msg))
}

func TestRenderPlainWritesChangesAtMostOncePerInterval(t *testing.T) {
	var buf bytes.Buffer
	clock := &fakeClock{t: time.Date(2026, time.November, 21, 6, 0, 0, 0, time.UTC)}
	sp := plainSpinner(&buf, clock)

	sp.SetMessage("A")
	sp.renderPlain()
	assert.Equal(t, "A\n", buf.String(), "the first message is written at once")

	sp.SetMessage("B")
	clock.advance(plainTestEvery - time.Second)
	sp.renderPlain()
	assert.Equal(t, "A\n", buf.String(), "a change within the interval waits")

	clock.advance(time.Second)
	sp.renderPlain()
	assert.Equal(t, "A\nB\n", buf.String())

	clock.advance(2 * plainTestEvery)
	sp.renderPlain()
	assert.Equal(t, "A\nB\n", buf.String(), "an unchanged message is not repeated")
}

func TestFlushPlainWritesChangedMessageWithinInterval(t *testing.T) {
	var buf bytes.Buffer
	clock := &fakeClock{t: time.Date(2026, time.November, 21, 6, 0, 0, 0, time.UTC)}
	sp := plainSpinner(&buf, clock)
	sp.SetMessage("A")
	sp.renderPlain()

	sp.SetMessage("Done")
	sp.flushPlain()
	sp.flushPlain()

	assert.Equal(t, "A\nDone\n", buf.String())
}

func TestPlainSpinnerStopAndCancelWriteOnlyTheMessage(t *testing.T) {
	for name, end := range map[string]func(*spinner){
		"stop":   (*spinner).Stop,
		"cancel": (*spinner).Cancel,
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			sp := newSpinner(&buf, "Starting...")
			sp.SetPlain(plainTestEvery)
			sp.SetMessage(SpinnerPlaceholder + " Done")
			sp.Start()
			end(sp)

			// Whether or not the display wrote it before ending, the
			// message appears once, with no escape sequence.
			assert.Equal(t, " Done\n", buf.String())
		})
	}
}

func TestPlainSpinnerPrintAboveWritesNoEscapes(t *testing.T) {
	var buf bytes.Buffer
	sp := newSpinner(&buf, "Starting...")
	sp.SetPlain(plainTestEvery)

	sp.PrintAbove(func() { buf.WriteString("warning\n") })

	assert.Equal(t, "warning\n", buf.String())
}

func TestNewThemedSpinnerIsPlainOffTerminal(t *testing.T) {
	var buf bytes.Buffer
	sp := newThemedSpinner(&buf)
	sp.Stop()

	require.NotNil(t, sp.plain)
	assert.Equal(t, config.DefaultPlainProgressInterval, sp.plain.every)
	assert.Equal(t, "Starting...\n", buf.String())
}
