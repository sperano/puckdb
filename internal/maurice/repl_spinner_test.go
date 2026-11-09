package maurice

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSpinner_StopClearsLineAndEndsOutput(t *testing.T) {
	var out bytes.Buffer
	s := newSpinner(&out)

	s.Start("thinking...")
	time.Sleep(3 * spinnerInterval)
	s.Stop()

	// Stop waits for the goroutine, so reading out here is race-free and
	// nothing may be written afterwards.
	got := out.String()
	assert.Contains(t, got, "thinking...")
	assert.True(t, strings.HasSuffix(got, "\r\033[K"), "the spinner line is cleared last: %q", got)
	time.Sleep(2 * spinnerInterval)
	assert.Equal(t, got, out.String(), "no output after Stop")
}

func TestSpinner_StopWithoutStartAndRestart(t *testing.T) {
	var out bytes.Buffer
	s := newSpinner(&out)

	s.Stop()
	assert.Empty(t, out.String())

	s.Start("first")
	s.Stop()
	s.Start("second")
	s.Stop()
	assert.Equal(t, 2, strings.Count(out.String(), "\r\033[K"))
}
