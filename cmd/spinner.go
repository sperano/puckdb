package cmd

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/sperano/puckdb/config"
)

// SpinnerPlaceholder is replaced with the current spinner frame when rendering.
// Use this in the message to position the spinner on a specific line.
const SpinnerPlaceholder = "\x00"

// spinner displays an animated spinner with a message (supports multi-line)
type spinner struct {
	frames    []string
	message   string
	header    string // in-progress header (e.g., "Downloading...")
	writer    io.Writer
	interval  time.Duration
	stop      chan struct{}
	done      chan struct{}
	mu        sync.Mutex
	once      sync.Once
	lineCount int // tracks number of lines in current message
}

func newSpinner(w io.Writer, header string) *spinner {
	return &spinner{
		frames:   []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		message:  header,
		header:   header,
		writer:   w,
		interval: config.DefaultSpinnerInterval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func (s *spinner) Start() {
	// Hide cursor
	fmt.Fprint(s.writer, "\033[?25l")

	go func() {
		defer close(s.done)
		frameIdx := 0
		for {
			select {
			case <-s.stop:
				// Exit cleanly, Stop() will render final state in place
				return
			default:
				s.render(frameIdx)
				frameIdx = (frameIdx + 1) % len(s.frames)
				time.Sleep(s.interval)
			}
		}
	}()
}

// render draws the current message with spinner frame, using in-place updates
// to avoid flicker. Uses clear-to-EOL (\033[K) instead of full screen clear.
func (s *spinner) render(frameIdx int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Move cursor back to start of output area (without clearing)
	if s.lineCount > 1 {
		fmt.Fprintf(s.writer, "\033[%dA", s.lineCount-1)
	}
	fmt.Fprint(s.writer, "\r")

	// Build lines with spinner frame substituted
	var lines []string
	if strings.Contains(s.message, SpinnerPlaceholder) {
		for _, line := range strings.Split(s.message, "\n") {
			lines = append(lines, strings.ReplaceAll(line, SpinnerPlaceholder, s.frames[frameIdx]))
		}
	} else {
		msgLines := strings.Split(s.message, "\n")
		for i, line := range msgLines {
			if i == len(msgLines)-1 {
				lines = append(lines, s.frames[frameIdx]+" "+line)
			} else {
				lines = append(lines, line)
			}
		}
	}

	newLineCount := len(lines)

	// Write lines, clearing to end of each line (handles varying line lengths)
	for i, line := range lines {
		fmt.Fprint(s.writer, line)
		fmt.Fprint(s.writer, "\033[K") // clear to end of line
		if i < len(lines)-1 {
			fmt.Fprint(s.writer, "\n")
		}
	}

	// Clear any extra lines from previous render
	if newLineCount < s.lineCount {
		for i := newLineCount; i < s.lineCount; i++ {
			fmt.Fprint(s.writer, "\n\033[K")
		}
		// Move cursor back up to end of content
		fmt.Fprintf(s.writer, "\033[%dA", s.lineCount-newLineCount)
	}

	s.lineCount = newLineCount
}

func (s *spinner) clearLines() {
	if s.lineCount > 1 {
		// Move cursor up to first line
		fmt.Fprintf(s.writer, "\033[%dA", s.lineCount-1)
	}
	// Clear from cursor to end of screen
	fmt.Fprintf(s.writer, "\r\033[J")
}

func (s *spinner) Stop() {
	s.once.Do(func() {
		close(s.stop)
		<-s.done

		s.mu.Lock()
		// Clear previous display
		s.clearLines()
		// Render final message (without spinner placeholders)
		msg := strings.ReplaceAll(s.message, SpinnerPlaceholder, " ")
		fmt.Fprintln(s.writer, msg)
		s.mu.Unlock()

		// Show cursor
		fmt.Fprint(s.writer, "\033[?25h")
	})
}

// Cancel stops the spinner without clearing or printing.
// Use this when the operation was interrupted rather than completed.
// Leaves the current display as-is.
func (s *spinner) Cancel() {
	s.once.Do(func() {
		close(s.stop)
		<-s.done

		// Show cursor
		fmt.Fprint(s.writer, "\033[?25h")
	})
}

// PrintAbove clears the spinner, runs the given function to print output,
// then continues rendering below that output. The printed content becomes permanent.
func (s *spinner) PrintAbove(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearLines()
	s.lineCount = 0
	fn()
}
