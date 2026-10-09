package mcpserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEdgeSeasonKeepsBothForms pins that the Edge tools still pass either
// season form unchanged: whether they should take a start year or a season
// ID is undecided, so only the int4 range is enforced.
func TestEdgeSeasonKeepsBothForms(t *testing.T) {
	const startYear int32 = 2024
	for _, season := range []int32{startYear, testSeasonID} {
		result, db := callNHLTool(t, "get_edge_skater_stats", map[string]any{playerIDArg: float64(matchedPlayerID), "season": float64(season)})
		require.NotEmpty(t, db.calls, resultText(t, result))
		assert.Contains(t, db.calls[0].args, season)
	}

	result, db := callNHLTool(t, "get_edge_team_stats", map[string]any{"team_id": float64(8), "season": float64(wrappedSeasonID)})
	assert.True(t, result.IsError)
	assert.Equal(t, "invalid season 4315219322: want an integer from 1 to 2147483647", resultText(t, result))
	assert.Empty(t, db.calls)
}
