package maurice

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
)

func TestMarkdownStyleNoTTY(t *testing.T) {
	t.Parallel()
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pipeR.Close(); _ = pipeW.Close() })

	cases := map[string]struct {
		in  io.Reader
		out io.Writer
	}{
		"buffers":    {in: strings.NewReader(""), out: &bytes.Buffer{}},
		"pipe files": {in: pipeR, out: pipeW},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := markdownStyle(tc.in, tc.out); got != styles.NoTTYStyle {
				t.Errorf("markdownStyle = %q, want %q", got, styles.NoTTYStyle)
			}
		})
	}
}

func TestStyledWriterStripsColorOffTerminal(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	styled := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")).Render("hi")
	if _, err := io.WriteString(styledWriter(&buf), styled); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "hi" {
		t.Errorf("styledWriter wrote %q, want %q", got, "hi")
	}
}
