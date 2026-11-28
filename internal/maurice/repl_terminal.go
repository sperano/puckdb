package maurice

import (
	"io"
	"os"

	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
)

// markdownStyle picks the glamour style glamour v1's WithAutoStyle chose:
// plain "notty" output unless both streams are terminals, then the dark or
// light theme to match the terminal's background.
func markdownStyle(in io.Reader, out io.Writer) string {
	inFile, inOK := in.(*os.File)
	outFile, outOK := out.(*os.File)
	if !inOK || !outOK || !term.IsTerminal(inFile.Fd()) || !term.IsTerminal(outFile.Fd()) {
		return styles.NoTTYStyle
	}
	if lipgloss.HasDarkBackground(inFile, outFile) {
		return styles.DarkStyle
	}
	return styles.LightStyle
}

// styledWriter wraps w so lipgloss v2 styles, which always render full-color
// ANSI, are reduced to what w supports: no escape codes when it isn't a
// terminal (lipgloss v1 did this detection itself).
func styledWriter(w io.Writer) io.Writer {
	return colorprofile.NewWriter(w, os.Environ())
}
