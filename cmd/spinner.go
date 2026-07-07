package cmd

import (
	"fmt"
	"io"
	"math/rand"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sperano/puckdb/config"
)

// colorThemeNames is the ordered list of available theme names for random selection.
var colorThemeNames []string

func init() {
	colorThemeNames = make([]string, 0, len(colorThemes))
	for name := range colorThemes {
		colorThemeNames = append(colorThemeNames, name)
	}
}

// colorThemePaletteSize is the number of shades in each color theme (dark to light).
const colorThemePaletteSize = 8

// Theme name constants.
const (
	ThemeBlue       = "blue"
	ThemeRed        = "red"
	ThemeGreen      = "green"
	ThemeTeal       = "teal"
	ThemePurple     = "purple"
	ThemeRandom     = "random"
	ThemeRandomEach = "random-each"
)

// colorThemes maps theme names to 8-shade ANSI 256-color palettes (dark to light).
var colorThemes = map[string][colorThemePaletteSize]int{
	ThemeBlue:   {17, 19, 21, 27, 33, 39, 45, 51},        // navy → cyan
	ThemeRed:    {88, 124, 160, 196, 202, 208, 214, 220}, // crimson → gold
	ThemeGreen:  {22, 28, 34, 40, 46, 82, 118, 154},      // dark green → chartreuse
	ThemeTeal:   {23, 30, 37, 44, 51, 87, 123, 159},      // dark teal → pale aqua
	ThemePurple: {53, 90, 127, 164, 201, 207, 213, 219},  // purple → pink
}

// SpinnerPlaceholder is replaced with the current spinner frame when rendering.
// Use this in the message to position the spinner on a specific line.
const SpinnerPlaceholder = "\x00"

// ProgressShade sentinels mark the start of each gradient segment in a progress bar.
// ProgressShadeEnd resets back to default. The render loop replaces these with ANSI colors.
const (
	ProgressShade0   = "\x01" // darkest
	ProgressShade1   = "\x02"
	ProgressShade2   = "\x03"
	ProgressShade3   = "\x04"
	ProgressShade4   = "\x05"
	ProgressShade5   = "\x06"
	ProgressShade6   = "\x07"
	ProgressShade7   = "\x08" // brightest
	ProgressShadeEnd = "\x0e"
)

var progressShades = [colorThemePaletteSize]string{
	ProgressShade0, ProgressShade1, ProgressShade2, ProgressShade3,
	ProgressShade4, ProgressShade5, ProgressShade6, ProgressShade7,
}

// spinner displays an animated spinner with a message (supports multi-line)
type spinner struct {
	frames           []string
	message          string
	header           string // in-progress header (e.g., "Downloading...")
	writer           io.Writer
	interval         time.Duration
	stop             chan struct{}
	done             chan struct{}
	mu               sync.Mutex
	once             sync.Once
	lineCount        int                                   // tracks number of lines in current message
	colorTheme       *[colorThemePaletteSize]int           // when set, all lines use this theme
	randomLineThemes bool                                  // when true, each line gets a shuffled theme
	keyThemes        map[string][colorThemePaletteSize]int // palette keyed by stable line content
	shuffledThemes   []string                              // shuffled theme names for round-robin assignment
	shuffledIdx      int                                   // next index into shuffledThemes
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

// SetColorTheme sets a named color theme for the spinner pulse effect.
// The spinner cycles through 4 shades (dark to light) on each frame.
// Returns false if the theme name is not recognized.
func (s *spinner) SetColorTheme(name string) bool {
	palette, ok := colorThemes[name]
	if !ok {
		return false
	}
	s.colorTheme = &palette
	return true
}

// SetRandomTheme picks one random theme and applies it to all lines.
func (s *spinner) SetRandomTheme() {
	name := colorThemeNames[rand.Intn(len(colorThemeNames))]
	s.SetColorTheme(name)
}

// SetRandomLineThemes enables per-line random theme assignment.
// Themes are shuffled so every theme appears before any repeats.
func (s *spinner) SetRandomLineThemes() {
	s.randomLineThemes = true
	s.keyThemes = make(map[string][colorThemePaletteSize]int)
	s.shuffledThemes = make([]string, len(colorThemeNames))
	copy(s.shuffledThemes, colorThemeNames)
	rand.Shuffle(len(s.shuffledThemes), func(i, j int) {
		s.shuffledThemes[i], s.shuffledThemes[j] = s.shuffledThemes[j], s.shuffledThemes[i]
	})
}

// lineKey extracts a stable identity from a line by stripping volatile parts
// (spinner placeholder, shade sentinels, progress bar, progress numbers).
// What remains is the label text (e.g., season name) that identifies the line.
var lineKeySentinelReplacer = strings.NewReplacer(
	SpinnerPlaceholder, "",
	ProgressShade0, "", ProgressShade1, "", ProgressShade2, "", ProgressShade3, "",
	ProgressShade4, "", ProgressShade5, "", ProgressShade6, "", ProgressShade7, "",
	ProgressShadeEnd, "",
)

// lineKeyProgressRe matches progress bar and numeric progress: [███░░░] 42/100 85%
var lineKeyProgressRe = regexp.MustCompile(`[█░\[\]]+|\d+/\d+|\d+%`)

func lineKey(line string) string {
	line = lineKeySentinelReplacer.Replace(line)
	line = lineKeyProgressRe.ReplaceAllString(line, "")
	return strings.TrimSpace(line)
}

// nextShuffledPalette returns the next palette from the shuffled bag,
// re-shuffling when all themes have been used.
func (s *spinner) nextShuffledPalette() [colorThemePaletteSize]int {
	if s.shuffledIdx >= len(s.shuffledThemes) {
		rand.Shuffle(len(s.shuffledThemes), func(i, j int) {
			s.shuffledThemes[i], s.shuffledThemes[j] = s.shuffledThemes[j], s.shuffledThemes[i]
		})
		s.shuffledIdx = 0
	}
	palette := colorThemes[s.shuffledThemes[s.shuffledIdx]]
	s.shuffledIdx++
	return palette
}

// paletteForLine returns the color palette for a line based on its content.
// With randomLineThemes, each unique line key gets a stable palette from the shuffle bag.
// With a single colorTheme, all lines share it.
// Returns nil if no theming is active.
func (s *spinner) paletteForLine(line string) *[colorThemePaletteSize]int {
	if !s.randomLineThemes {
		return s.colorTheme
	}
	key := lineKey(line)
	if _, ok := s.keyThemes[key]; !ok {
		s.keyThemes[key] = s.nextShuffledPalette()
	}
	palette := s.keyThemes[key]
	return &palette
}

// colorize wraps text in an ANSI 256-color escape sequence using the palette shade
// for the given frame index. Returns the text unchanged if palette is nil.
func colorize(text string, frameIdx int, palette *[colorThemePaletteSize]int) string {
	if palette == nil {
		return text
	}
	color := palette[frameIdx%colorThemePaletteSize]
	return fmt.Sprintf("\033[38;5;%dm%s\033[0m", color, text)
}

// colorizeProgressFill replaces ProgressShade sentinels with ANSI color codes
// from the palette. Each shade marker maps to the corresponding palette index,
// creating a dark-to-light gradient across the filled portion of the bar.
func colorizeProgressFill(line string, palette *[colorThemePaletteSize]int) string {
	if palette == nil {
		for _, s := range progressShades {
			line = strings.ReplaceAll(line, s, "")
		}
		return strings.ReplaceAll(line, ProgressShadeEnd, "")
	}
	for i, s := range progressShades {
		line = strings.ReplaceAll(line, s, fmt.Sprintf("\033[38;5;%dm", palette[i]))
	}
	return strings.ReplaceAll(line, ProgressShadeEnd, "\033[0m")
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

	// Build lines with spinner frame and progress fill colorized.
	// Lines with a SpinnerPlaceholder get spinner + progress bar themed.
	// Lines without a placeholder but with shade sentinels (e.g., Total) get progress bar themed.
	// Lines with neither (e.g., headers) pass through unchanged.
	var lines []string
	if strings.Contains(s.message, SpinnerPlaceholder) {
		for _, line := range strings.Split(s.message, "\n") {
			if strings.Contains(line, SpinnerPlaceholder) {
				palette := s.paletteForLine(line)
				colorizedFrame := colorize(s.frames[frameIdx], frameIdx, palette)
				line = strings.ReplaceAll(line, SpinnerPlaceholder, colorizedFrame)
				line = colorizeProgressFill(line, palette)
			} else if strings.Contains(line, ProgressShade0) {
				palette := s.paletteForLine(line)
				line = colorizeProgressFill(line, palette)
			}
			lines = append(lines, line)
		}
	} else {
		palette := s.paletteForLine(s.message)
		colorizedFrame := colorize(s.frames[frameIdx], frameIdx, palette)
		msgLines := strings.Split(s.message, "\n")
		for i, line := range msgLines {
			if i == len(msgLines)-1 {
				line = colorizeProgressFill(line, palette)
				lines = append(lines, colorizedFrame+" "+line)
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
		// Render final message (without spinner/progress placeholders)
		msg := strings.ReplaceAll(s.message, SpinnerPlaceholder, " ")
		msg = colorizeProgressFill(msg, nil)
		fmt.Fprintln(s.writer, msg)
		s.mu.Unlock()

		// Show cursor
		fmt.Fprint(s.writer, "\033[?25h")
	})
}

// Cancel stops the spinner, leaving the last rendered output visible.
// Use this when the operation was interrupted rather than completed.
func (s *spinner) Cancel() {
	s.once.Do(func() {
		close(s.stop)
		<-s.done

		// Move cursor below the last rendered output so subsequent
		// prints don't overwrite it.
		s.mu.Lock()
		if s.lineCount > 0 {
			fmt.Fprint(s.writer, "\n")
		}
		s.mu.Unlock()

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

// SetMessage updates the spinner's message text.
func (s *spinner) SetMessage(msg string) {
	s.mu.Lock()
	s.message = msg
	s.mu.Unlock()
}
