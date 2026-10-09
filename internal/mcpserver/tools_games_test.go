package mcpserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGamesBySeasonPassesExactSeason reproduces the defect the strict
// integer path fixes: a season that an unchecked int32 cast wraps, or a
// fractional one GetInt truncates, used to reach SQL as 20252026.
func TestGamesBySeasonPassesExactSeason(t *testing.T) {
	for name, raw := range map[string]any{"json number": float64(testSeasonID), "numeric string": "20252026"} {
		t.Run(name, func(t *testing.T) {
			result, db := callNHLTool(t, "get_games_by_season", map[string]any{"season": raw})
			require.False(t, result.IsError, resultText(t, result))
			require.Len(t, db.calls, 1)
			assert.Equal(t, []any{testSeasonID}, db.calls[0].args)
		})
	}

	for name, raw := range map[string]any{
		"wrapped":    float64(wrappedSeasonID),
		"fractional": 20252026.9,
		"negative":   -float64(testSeasonID),
		"zero":       float64(0),
	} {
		t.Run(name, func(t *testing.T) {
			result, db := callNHLTool(t, "get_games_by_season", map[string]any{"season": raw})
			assert.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), "invalid season")
			assert.Empty(t, db.calls)
		})
	}
}

func TestGetGameKeepsJSONErrorShape(t *testing.T) {
	result, db := callNHLTool(t, "get_game", map[string]any{"game_id": float64(2025020001)})
	require.Len(t, db.calls, 1)
	assert.Equal(t, []any{int64(2025020001)}, db.calls[0].args)
	assert.True(t, result.IsError)
	assert.Equal(t, "no rows in result set", resultText(t, result))
}
