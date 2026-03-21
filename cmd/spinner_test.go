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

func TestSpinner_ColorTheme(t *testing.T) {
	var buf bytes.Buffer
	sp := newSpinner(&buf, "Test")
	ok := sp.SetColorTheme("coral")
	if !ok {
		t.Fatal("expected SetColorTheme to return true for 'coral'")
	}

	sp.Start()
	time.Sleep(100 * time.Millisecond)
	sp.Stop()

	output := buf.String()

	if !strings.Contains(output, "\033[38;5;") {
		t.Error("expected 256-color ANSI escape sequence with color theme")
	}

	if !strings.Contains(output, "\033[0m") {
		t.Error("expected ANSI reset sequence with color theme")
	}
}

func TestSpinner_ColorThemeInvalid(t *testing.T) {
	sp := newSpinner(&bytes.Buffer{}, "Test")
	ok := sp.SetColorTheme("nonexistent")
	if ok {
		t.Error("expected SetColorTheme to return false for unknown theme")
	}
	if sp.colorTheme != nil {
		t.Error("colorTheme should be nil for unknown theme")
	}
}

func TestSpinner_ColorThemePaletteCycles(t *testing.T) {
	sp := newSpinner(&bytes.Buffer{}, "Test")
	sp.SetColorTheme("coral")

	// Verify each frame index maps to the expected palette shade
	expected := [colorThemePaletteSize]string{
		"\033[38;5;167m⠋\033[0m", // dark
		"\033[38;5;173m⠙\033[0m",
		"\033[38;5;174m⠹\033[0m",
		"\033[38;5;203m⠸\033[0m", // light
	}
	palette := sp.colorTheme
	for i, want := range expected {
		got := colorize(sp.frames[i], i, palette)
		if got != want {
			t.Errorf("frame %d: got %q, want %q", i, got, want)
		}
	}

	// Verify it wraps around
	got := colorize(sp.frames[4], 4, palette)
	if got != "\033[38;5;167m⠼\033[0m" {
		t.Errorf("frame 4 (wrap): got %q, want dark shade", got)
	}
}

func TestSpinner_RandomLineThemes(t *testing.T) {
	sp := newSpinner(&bytes.Buffer{}, "Test")
	sp.SetRandomLineThemes()

	if !sp.randomLineThemes {
		t.Fatal("expected randomLineThemes to be true")
	}

	// Different season lines get palettes
	seasons := []string{
		SpinnerPlaceholder + " 2001-2002 [████░░░░] 50%",
		SpinnerPlaceholder + " 2002-2003 [██░░░░░░] 25%",
		SpinnerPlaceholder + " 2003-2004 [██████░░] 75%",
	}
	for _, line := range seasons {
		p := sp.paletteForLine(line)
		if p == nil {
			t.Fatalf("expected non-nil palette for %q", line)
		}
		// Verify palette is a known theme
		found := false
		for _, palette := range colorThemes {
			if *p == palette {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("palette for %q does not match any known theme", line)
		}
	}

	// Same season with different progress still gets the same palette
	p1 := sp.paletteForLine(SpinnerPlaceholder + " 2001-2002 [████░░░░] 50%")
	p2 := sp.paletteForLine(SpinnerPlaceholder + " 2001-2002 [██████░░] 75%")
	if *p1 != *p2 {
		t.Error("expected same palette for same season with different progress")
	}
}

func TestSpinner_LineKey(t *testing.T) {
	// Same season, different progress → same key
	k1 := lineKey(SpinnerPlaceholder + " 2001-2002 [████░░░░] 50%")
	k2 := lineKey(SpinnerPlaceholder + " 2001-2002 [██████░░] 75%")
	if k1 != k2 {
		t.Errorf("expected same key, got %q and %q", k1, k2)
	}

	// Different seasons → different keys
	k3 := lineKey(SpinnerPlaceholder + " 2002-2003 [████░░░░] 50%")
	if k1 == k3 {
		t.Error("expected different keys for different seasons")
	}
}

func TestSpinner_NoTheme(t *testing.T) {
	var buf bytes.Buffer
	sp := newSpinner(&buf, "Test")

	sp.Start()
	time.Sleep(100 * time.Millisecond)
	sp.Stop()

	output := buf.String()

	// Should not contain 256-color escape sequence when no theme is set
	if strings.Contains(output, "\033[38;5;") {
		t.Error("should not contain 256-color sequence without a theme")
	}
}
