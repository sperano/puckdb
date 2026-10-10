package mcpserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlayersByBirthplaceLimit(t *testing.T) {
	tests := map[string]struct {
		limit any
		want  int32
	}{
		"absent":   {want: defaultBirthplaceLimit},
		"zero":     {limit: float64(0), want: defaultBirthplaceLimit},
		"explicit": {limit: float64(7), want: 7},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			args := map[string]any{"birth_country": "CAN"}
			if tc.limit != nil {
				args["limit"] = tc.limit
			}
			result, db := callNHLTool(t, "get_players_by_birthplace", args)
			require.False(t, result.IsError, resultText(t, result))
			require.Len(t, db.calls, 1)
			// GetPlayersByBirthplace binds the limit first.
			assert.Equal(t, tc.want, db.calls[0].args[0])
		})
	}

	// The old handler silently replaced a negative limit with the default.
	result, db := callNHLTool(t, "get_players_by_birthplace", map[string]any{"birth_country": "CAN", "limit": float64(-5)})
	assert.True(t, result.IsError)
	assert.Equal(t, "invalid limit -5: want an integer from 0 to 2147483647", resultText(t, result))
	assert.Empty(t, db.calls)
}
