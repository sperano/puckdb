package store

import (
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseYahooPlayerHTML(t *testing.T) {
	t.Parallel()

	t.Run("full player with all fields", func(t *testing.T) {
		html := []byte(`
<!DOCTYPE html>
<html>
<head>
<title>Matt Rempe (C, F, RW) Stats, News... - NY Rangers - Yahoo Sports</title>
</head>
<body>
<span">#99<</span>
<img src="https://s.yimg.com/xe/i/us/sp/v/nhl_cutout/players_l/20240115/12345.png" />
<div>January 26, 1999</div>
</body>
</html>`)

		player, err := ParseYahooPlayerHTML(12345, html)
		require.NoError(t, err)

		assert.Equal(t, 12345, player.YahooID)
		assert.Equal(t, "Matt", player.FirstName)
		assert.Equal(t, "Rempe", player.LastName)
		assert.Equal(t, "NY Rangers", player.Team)
		assert.Equal(t, 99, player.JerseyNumber)
		assert.Contains(t, player.ImageURL, "12345.png")
		assert.Equal(t, time.Date(1999, time.January, 26, 0, 0, 0, 0, time.UTC), player.BirthDate)

		// Should have C and RW, but not F (skipped)
		assert.Len(t, player.Positions, 2)
		assert.Contains(t, player.Positions, nhl.PositionCenter)
		assert.Contains(t, player.Positions, nhl.PositionRightWing)
	})

	t.Run("retired player - no team in title", func(t *testing.T) {
		html := []byte(`
<!DOCTYPE html>
<html>
<head>
<title>Wayne Gretzky (C) Stats, News, Bio | NHL</title>
</head>
<body>
<div>January 26, 1961</div>
</body>
</html>`)

		player, err := ParseYahooPlayerHTML(99, html)
		require.NoError(t, err)

		assert.Equal(t, 99, player.YahooID)
		assert.Equal(t, "Wayne", player.FirstName)
		assert.Equal(t, "Gretzky", player.LastName)
		assert.Empty(t, player.Team) // No team for retired player
		assert.Equal(t, 0, player.JerseyNumber)
		assert.Empty(t, player.ImageURL)
		assert.Len(t, player.Positions, 1)
		assert.Equal(t, nhl.PositionCenter, player.Positions[0])
	})

	t.Run("goalie", func(t *testing.T) {
		html := []byte(`
<!DOCTYPE html>
<html>
<head>
<title>Igor Shesterkin (G) Stats, News... - NY Rangers - Yahoo Sports</title>
</head>
<body>
<span">#31<</span>
</body>
</html>`)

		player, err := ParseYahooPlayerHTML(31, html)
		require.NoError(t, err)

		assert.Equal(t, "Igor", player.FirstName)
		assert.Equal(t, "Shesterkin", player.LastName)
		assert.Equal(t, 31, player.JerseyNumber)
		assert.Len(t, player.Positions, 1)
		assert.Equal(t, nhl.PositionGoalie, player.Positions[0])
	})

	t.Run("empty player page", func(t *testing.T) {
		html := []byte(`
<!DOCTYPE html>
<html>
<head>
<title> Stats, News, Rumors | NHL</title>
</head>
<body></body>
</html>`)

		_, err := ParseYahooPlayerHTML(123, html)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPlayerPageEmpty)
	})

	t.Run("invalid title format", func(t *testing.T) {
		html := []byte(`
<!DOCTYPE html>
<html>
<head>
<title>Some Random Page</title>
</head>
<body></body>
</html>`)

		_, err := ParseYahooPlayerHTML(123, html)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not parse player title")
	})

	t.Run("single name player", func(t *testing.T) {
		html := []byte(`
<!DOCTYPE html>
<html>
<head>
<title>Madonna (LW) Stats, News... - Vegas - Yahoo Sports</title>
</head>
<body></body>
</html>`)

		player, err := ParseYahooPlayerHTML(1, html)
		require.NoError(t, err)

		assert.Equal(t, "Madonna", player.FirstName)
		assert.Empty(t, player.LastName)
	})

	t.Run("defenseman", func(t *testing.T) {
		html := []byte(`
<!DOCTYPE html>
<html>
<head>
<title>Adam Fox (D) Stats, News... - NY Rangers - Yahoo Sports</title>
</head>
<body>
<span">#23<</span>
</body>
</html>`)

		player, err := ParseYahooPlayerHTML(23, html)
		require.NoError(t, err)

		assert.Equal(t, "Adam", player.FirstName)
		assert.Equal(t, "Fox", player.LastName)
		assert.Len(t, player.Positions, 1)
		assert.Equal(t, nhl.PositionDefense, player.Positions[0])
	})

	t.Run("left wing", func(t *testing.T) {
		html := []byte(`
<!DOCTYPE html>
<html>
<head>
<title>Alex Ovechkin (LW) Stats, News... - Washington - Yahoo Sports</title>
</head>
<body></body>
</html>`)

		player, err := ParseYahooPlayerHTML(8, html)
		require.NoError(t, err)

		assert.Len(t, player.Positions, 1)
		assert.Equal(t, nhl.PositionLeftWing, player.Positions[0])
	})
}

func TestParsePositions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []nhl.Position
	}{
		{
			name:     "single position - center",
			input:    "C",
			expected: []nhl.Position{nhl.PositionCenter},
		},
		{
			name:     "single position - goalie",
			input:    "G",
			expected: []nhl.Position{nhl.PositionGoalie},
		},
		{
			name:     "multiple positions",
			input:    "C, LW, RW",
			expected: []nhl.Position{nhl.PositionCenter, nhl.PositionLeftWing, nhl.PositionRightWing},
		},
		{
			name:     "skips generic forward F",
			input:    "C, F, RW",
			expected: []nhl.Position{nhl.PositionCenter, nhl.PositionRightWing},
		},
		{
			name:     "only F returns empty",
			input:    "F",
			expected: nil,
		},
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "unknown position skipped",
			input:    "C, XYZ, D",
			expected: []nhl.Position{nhl.PositionCenter, nhl.PositionDefense},
		},
		{
			name:     "deduplicates positions",
			input:    "C, C, LW, C",
			expected: []nhl.Position{nhl.PositionCenter, nhl.PositionLeftWing},
		},
		{
			name:     "whitespace handling",
			input:    "  C  ,  RW  ",
			expected: []nhl.Position{nhl.PositionCenter, nhl.PositionRightWing},
		},
		{
			name:     "defense",
			input:    "D",
			expected: []nhl.Position{nhl.PositionDefense},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parsePositions(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
