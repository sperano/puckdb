package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// plainProgress is the spinner's output for a writer that is not a
// terminal, such as a CronJob's log: no cursor moves, colors or animation,
// only the message as plain lines, written when it changed and at most once
// per every (the final message always).
type plainProgress struct {
	every   time.Duration
	now     func() time.Time
	last    string
	written time.Time
}

// isTerminal reports whether w is a terminal; anything but an *os.File on a
// terminal (a pipe, a file, a buffer) is not.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// SetPlain switches the spinner to plain output, writing a changed message
// at most once per every. Call it before Start.
func (s *spinner) SetPlain(every time.Duration) {
	s.plain = &plainProgress{every: every, now: time.Now}
}

// plainText is msg without the spinner placeholders and progress shade
// sentinels, and without trailing spaces on its lines.
func plainText(msg string) string {
	msg = strings.ReplaceAll(msg, SpinnerPlaceholder, "")
	msg = colorizeProgressFill(msg, nil)
	lines := strings.Split(msg, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

// renderPlain writes the message when it changed since the last write and
// the last write is at least every old.
func (s *spinner) renderPlain() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.plain.now()
	if !s.plain.written.IsZero() && now.Sub(s.plain.written) < s.plain.every {
		return
	}
	s.writePlainLocked(now)
}

// flushPlain writes the message when it differs from the last one written,
// however recent that write: the final state of a stopped or cancelled
// spinner.
func (s *spinner) flushPlain() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writePlainLocked(s.plain.now())
}

func (s *spinner) writePlainLocked(now time.Time) {
	text := plainText(s.message)
	if text == s.plain.last {
		return
	}
	_, _ = fmt.Fprintln(s.writer, text)
	s.plain.last = text
	s.plain.written = now
}

// startPlain runs the plain display: it checks the message every spinner
// interval and writes it per renderPlain, until Stop or Cancel.
func (s *spinner) startPlain() {
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			s.renderPlain()
			select {
			case <-s.stop:
				return
			case <-ticker.C:
			}
		}
	}()
}
