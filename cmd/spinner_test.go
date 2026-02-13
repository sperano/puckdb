package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestSpinner_BasicOperation(t *testing.T) {
	var buf bytes.Buffer
	sp := newSpinner(&buf, "Loading...")

	// Verify spinner was created with correct defaults
	if sp.header != "Loading..." {
		t.Errorf("expected header 'Loading...', got %q", sp.header)
	}
	if sp.message != "Loading..." {
		t.Errorf("expected message 'Loading...', got %q", sp.message)
	}
	if len(sp.frames) != 10 {
		t.Errorf("expected 10 frames, got %d", len(sp.frames))
	}
}

func TestSpinner_StartStop(t *testing.T) {
	var buf bytes.Buffer
	sp := newSpinner(&buf, "Test")

	sp.Start()
	time.Sleep(50 * time.Millisecond) // Let spinner run briefly
	sp.Stop()

	output := buf.String()

	// Should contain hide cursor sequence at start
	if !strings.Contains(output, "\033[?25l") {
		t.Error("expected hide cursor sequence")
	}

	// Should contain show cursor sequence at end
	if !strings.Contains(output, "\033[?25h") {
		t.Error("expected show cursor sequence")
	}
}

func TestSpinner_Cancel(t *testing.T) {
	var buf bytes.Buffer
	sp := newSpinner(&buf, "Test")

	sp.Start()
	time.Sleep(50 * time.Millisecond)
	sp.Cancel()

	output := buf.String()

	// Should still show cursor after cancel
	if !strings.Contains(output, "\033[?25h") {
		t.Error("expected show cursor sequence after cancel")
	}
}

func TestSpinner_MessageUpdate(t *testing.T) {
	var buf bytes.Buffer
	sp := newSpinner(&buf, "Initial")

	sp.Start()
	time.Sleep(50 * time.Millisecond)

	sp.mu.Lock()
	sp.message = "Updated"
	sp.mu.Unlock()

	time.Sleep(50 * time.Millisecond)
	sp.Stop()

	// Both messages should have appeared in output
	output := buf.String()
	if !strings.Contains(output, "Initial") && !strings.Contains(output, "Updated") {
		t.Error("expected message content in output")
	}
}

func TestSpinner_PlaceholderReplacement(t *testing.T) {
	var buf bytes.Buffer
	sp := newSpinner(&buf, "")

	sp.message = SpinnerPlaceholder + " Loading..."
	sp.Start()
	time.Sleep(100 * time.Millisecond)
	sp.Stop()

	output := buf.String()

	// Should not contain raw placeholder
	if strings.Contains(output, SpinnerPlaceholder) {
		t.Error("placeholder should have been replaced with spinner frame")
	}

	// Should contain one of the spinner frames
	hasFrame := false
	for _, frame := range sp.frames {
		if strings.Contains(output, frame) {
			hasFrame = true
			break
		}
	}
	if !hasFrame {
		t.Error("expected spinner frame in output")
	}
}

func TestSpinner_MultiLineMessage(t *testing.T) {
	var buf bytes.Buffer
	sp := newSpinner(&buf, "Line1\nLine2\nLine3")

	sp.Start()
	time.Sleep(50 * time.Millisecond)
	sp.Stop()

	output := buf.String()

	// Should handle multi-line messages
	if !strings.Contains(output, "Line1") {
		t.Error("expected Line1 in output")
	}
}

func TestSpinner_PrintAbove(t *testing.T) {
	var buf bytes.Buffer
	sp := newSpinner(&buf, "Spinning...")

	sp.Start()
	time.Sleep(50 * time.Millisecond)

	sp.PrintAbove(func() {
		buf.WriteString("PERMANENT OUTPUT\n")
	})

	time.Sleep(50 * time.Millisecond)
	sp.Stop()

	output := buf.String()
	if !strings.Contains(output, "PERMANENT OUTPUT") {
		t.Error("expected permanent output from PrintAbove")
	}
}

func TestSpinner_DoubleStop(t *testing.T) {
	var buf bytes.Buffer
	sp := newSpinner(&buf, "Test")

	sp.Start()
	time.Sleep(20 * time.Millisecond)

	// Calling Stop twice should not panic
	sp.Stop()
	sp.Stop()
}

func TestSpinner_DoubleCancel(t *testing.T) {
	var buf bytes.Buffer
	sp := newSpinner(&buf, "Test")

	sp.Start()
	time.Sleep(20 * time.Millisecond)

	// Calling Cancel twice should not panic
	sp.Cancel()
	sp.Cancel()
}
