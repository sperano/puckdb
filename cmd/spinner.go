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
		i := 0
		for {
			select {
			case <-s.stop:
				// Exit cleanly, Stop() will render final state in place
				return
			default:
				s.mu.Lock()
				// Move cursor up and clear previous lines if multi-line
				s.clearLines()
				// Count lines in new message
				s.lineCount = strings.Count(s.message, "\n") + 1

				// Replace placeholder with spinner frame, or put spinner on last line
				if strings.Contains(s.message, SpinnerPlaceholder) {
					// Placeholder mode: replace all placeholders with spinner frame
					output := strings.ReplaceAll(s.message, SpinnerPlaceholder, s.frames[i])
					fmt.Fprint(s.writer, output)
				} else {
					// Legacy mode: spinner on last line
					lines := strings.Split(s.message, "\n")
					for j := 0; j < len(lines)-1; j++ {
						fmt.Fprintf(s.writer, "%s\n", lines[j])
					}
					fmt.Fprintf(s.writer, "%s %s", s.frames[i], lines[len(lines)-1])
				}
				s.mu.Unlock()
				i = (i + 1) % len(s.frames)
				time.Sleep(s.interval)
			}
		}
	}()
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
