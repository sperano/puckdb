package store

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
)

// ErrPlayerPageEmpty indicates the Yahoo player page exists but has no player data.
var ErrPlayerPageEmpty = errors.New("player page is empty or deleted")

// YahooPlayer holds player data parsed from Yahoo Sports HTML files.
type YahooPlayer struct {
	YahooID      YahooPlayerID
	FirstName    string
	LastName     string
	Positions    []nhl.Position
	JerseyNumber int       // 0 if not available (retired players)
	Team         string    // optional, may be empty
	ImageURL     string    // optional
	BirthDate    time.Time // optional, zero value if not found
}

// Regex patterns for parsing Yahoo player HTML
var (
	// Title format: "Wayne Gretzky (C) Stats, News..." or "Matt Rempe (C, F, RW) Stats... - NY Rangers - Yahoo Sports"
	titlePattern = regexp.MustCompile(`<title>([^(]+)\(([^)]+)\)[^-]*(?:- ([^-]+) - Yahoo Sports|Stats)`)

	// Empty/deleted player page: "<title> Stats, News, Rumors..." (no name, starts with space)
	emptyTitlePattern = regexp.MustCompile(`<title>\s*Stats,`)

	// Jersey number format: ">#99<" in span elements
	jerseyPattern = regexp.MustCompile(`">#(\d+)<`)

	// Image URL format: https://s.yimg.com/xe/i/us/sp/v/nhl_cutout/players_l/{date}/{player_id}.png
	imagePattern = regexp.MustCompile(`(https://s\.yimg\.com/xe/i/us/sp/v/nhl_cutout/players_l/[^"]+\.png)`)

	// Birth date format: "January 26, 1961" (appears in bio section)
	birthDatePattern = regexp.MustCompile(`([A-Z][a-z]+ \d{1,2}, \d{4})`)
)

// ParseYahooPlayerHTML parses a Yahoo Sports player HTML file and extracts player data.
// The yahooID should be extracted from the filename (e.g., "player-1.html" -> 1).
func ParseYahooPlayerHTML(yahooID YahooPlayerID, html []byte) (*YahooPlayer, error) {
	content := string(html)

	player := &YahooPlayer{
		YahooID: yahooID,
	}

	// Check for empty/deleted player page first
	if emptyTitlePattern.MatchString(content) {
		return nil, ErrPlayerPageEmpty
	}

	// Extract name, positions, and optional team from title
	titleMatch := titlePattern.FindStringSubmatch(content)
	if titleMatch == nil {
		return nil, fmt.Errorf("could not parse player title from HTML")
	}

	// Parse name (trim whitespace)
	fullName := strings.TrimSpace(titleMatch[1])
	parts := strings.SplitN(fullName, " ", 2)
	if len(parts) >= 1 {
		player.FirstName = parts[0]
	}
	if len(parts) >= 2 {
		player.LastName = parts[1]
	}

	// Parse positions (e.g., "C" or "C, F, RW")
	positionsStr := strings.TrimSpace(titleMatch[2])
	player.Positions = parsePositions(positionsStr)

	// Parse optional team
	if len(titleMatch) > 3 && titleMatch[3] != "" {
		player.Team = strings.TrimSpace(titleMatch[3])
	}

	// Extract jersey number (optional)
	jerseyMatch := jerseyPattern.FindStringSubmatch(content)
	if jerseyMatch != nil {
		if num, err := strconv.Atoi(jerseyMatch[1]); err == nil {
			player.JerseyNumber = num
		}
	}

	// Extract image URL (optional)
	imageMatch := imagePattern.FindStringSubmatch(content)
	if imageMatch != nil {
		player.ImageURL = imageMatch[1]
	}

	// Extract birth date (optional)
	birthMatch := birthDatePattern.FindStringSubmatch(content)
	if birthMatch != nil {
		if t, err := time.Parse("January 2, 2006", birthMatch[1]); err == nil {
			player.BirthDate = t
		}
	}

	return player, nil
}

// parsePositions parses a comma-separated position string into a slice of nhl.Position.
// Skips invalid positions (like "F" for generic forward).
func parsePositions(s string) []nhl.Position {
	var positions []nhl.Position
	seen := make(map[nhl.Position]bool)

	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" || p == "F" {
			// Skip empty and generic "Forward" (redundant with C/LW/RW)
			continue
		}

		pos, err := nhl.PositionFromString(p)
		if err != nil {
			// Skip unknown positions
			continue
		}

		// Deduplicate
		if !seen[pos] {
			seen[pos] = true
			positions = append(positions, pos)
		}
	}

	return positions
}
