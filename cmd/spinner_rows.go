package cmd

import (
	"os"
	"regexp"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

// ansiEscapeRe matches the CSI escape sequences the spinner writes (colors,
// cursor moves, clears); they take no columns on screen.
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// terminalColumns returns stdout's width in columns, or 0 when it is not a
// terminal or its size is unknown.
func terminalColumns() int {
	columns, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || columns <= 0 {
		return 0
	}
	return columns
}

// screenRows returns how many terminal rows line occupies once the terminal
// wraps it at columns. With an unknown width (columns <= 0) every line
// counts as one row.
func screenRows(line string, columns int) int {
	if columns <= 0 {
		return 1
	}
	width := runewidth.StringWidth(ansiEscapeRe.ReplaceAllString(line, ""))
	if width <= columns {
		return 1
	}
	return (width + columns - 1) / columns
}

// totalScreenRows returns the rows lines occupy when written one per line.
// The spinner moves the cursor by rows, not lines: counting a wrapped line
// as one row leaves the cursor short of the top on the next redraw, so each
// frame is drawn below the remains of the previous one.
func totalScreenRows(lines []string, columns int) int {
	rows := 0
	for _, line := range lines {
		rows += screenRows(line, columns)
	}
	return rows
}
