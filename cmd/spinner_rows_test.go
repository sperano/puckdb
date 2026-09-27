package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const testTerminalColumns = 10

func TestScreenRows(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		line    string
		columns int
		want    int
	}{
		{"empty line", "", testTerminalColumns, 1},
		{"fits", "0123456789", testTerminalColumns, 1},
		{"wraps once", "0123456789a", testTerminalColumns, 2},
		{"wraps twice", strings.Repeat("x", 25), testTerminalColumns, 3},
		{"escapes take no columns", "\033[38;5;196m0123456789\033[0m\033[K", testTerminalColumns, 1},
		{"wide runes take two columns", strings.Repeat("界", 6), testTerminalColumns, 2},
		{"unknown width is one row", strings.Repeat("x", 500), 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, screenRows(tc.line, tc.columns))
		})
	}
}

// A wrapped line must move the cursor back up by every row it occupies, or
// the next frame is drawn below the remains of this one.
func TestSpinnerRenderMovesUpByScreenRows(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	sp := newSpinner(&out, "")
	sp.columns = func() int { return testTerminalColumns }
	sp.SetMessage("✓ " + strings.Repeat("x", 3*testTerminalColumns) + "\nshort")

	sp.render(0)
	// "✓ " + 30 columns = 32 columns → 4 rows, plus "short" with its frame → 5.
	assert.Equal(t, 5, sp.lineCount)

	out.Reset()
	sp.render(1)
	assert.True(t, strings.HasPrefix(out.String(), "\033[4A"),
		"redraw must move up 4 rows, got %q", out.String()[:min(len(out.String()), 8)])
}
