package maurice

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// spinnerInterval is the delay between two frames of the thinking indicator.
const spinnerInterval = 80 * time.Millisecond

// spinner frames for the thinking indicator.
var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinner displays a thinking animation on out while the LLM is working.
type spinner struct {
	out     io.Writer
	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
}

func newSpinner(out io.Writer) *spinner {
	return &spinner{out: out}
}

func (s *spinner) Start(label string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	s.running = true
	s.stop = make(chan struct{})
	s.done = make(chan struct{})

	style := lipgloss.NewStyle().Foreground(lipgloss.Color("12"))

	go func(stop <-chan struct{}, done chan<- struct{}) {
		defer close(done)
		i := 0
		ticker := time.NewTicker(spinnerInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				fmt.Fprint(s.out, "\r\033[K") // clear spinner line
				return
			case <-ticker.C:
				frame := style.Render(spinnerFrames[i%len(spinnerFrames)])
				fmt.Fprintf(s.out, "\r  %s %s", frame, label)
				i++
			}
		}
	}(s.stop, s.done)
}

// Stop ends the animation and waits until the spinner line is cleared, so
// nothing the spinner writes can interleave with the caller's next output.
func (s *spinner) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	close(s.stop)
	<-s.done
}
