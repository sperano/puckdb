package mcpserver

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testGameID int64 = 2025020001

func TestGamePlayEventsZeroPeriodAndLimitMeanNoFilter(t *testing.T) {
	tests := map[string]struct {
		args       map[string]any
		wantPeriod pgtype.Int4
		wantLimit  pgtype.Int4
	}{
		"absent":   {args: map[string]any{}},
		"zero":     {args: map[string]any{"period": float64(0), "limit": float64(0)}},
		"explicit": {args: map[string]any{"period": float64(4), "limit": float64(2)}, wantPeriod: pgtype.Int4{Int32: 4, Valid: true}, wantLimit: pgtype.Int4{Int32: 2, Valid: true}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.args["game_id"] = float64(testGameID)
			result, db := callNHLTool(t, "get_game_play_events", tc.args)
			require.False(t, result.IsError, resultText(t, result))
			require.Len(t, db.calls, 1)
			// GetGamePlayEvents binds game_id, period, type_desc_keys, limit.
			args := db.calls[0].args
			assert.Equal(t, testGameID, args[0])
			assert.Equal(t, tc.wantPeriod, args[1])
			assert.Equal(t, tc.wantLimit, args[3])
		})
	}
}

func TestGamePlayEventsRejectsNegativeLimit(t *testing.T) {
	// The old handler ignored a negative limit; it is now refused.
	result, db := callNHLTool(t, "get_game_play_events", map[string]any{"game_id": float64(testGameID), "limit": float64(-1)})
	assert.True(t, result.IsError)
	assert.Equal(t, "invalid limit -1: want an integer from 0 to 2147483647", resultText(t, result))
	assert.Empty(t, db.calls)
}

func TestFirstMatchingEventPerTeamValidatesEveryGameID(t *testing.T) {
	args := map[string]any{"game_ids": []any{float64(testGameID), "2025020002"}, "type_desc_keys": []any{"goal"}}
	result, db := callNHLTool(t, "get_first_matching_event_per_team", args)
	require.False(t, result.IsError, resultText(t, result))
	require.Len(t, db.calls, 1)
	assert.Equal(t, []int64{testGameID, testGameID + 1}, db.calls[0].args[0])

	args["game_ids"] = []any{float64(testGameID), float64(testGameID) + 0.5}
	result, db = callNHLTool(t, "get_first_matching_event_per_team", args)
	assert.True(t, result.IsError)
	assert.Equal(t, "invalid game_ids[1] 2025020001.5: want an integer from 1 to 9007199254740991", resultText(t, result))
	assert.Empty(t, db.calls)

	args["game_ids"] = []any{}
	result, db = callNHLTool(t, "get_first_matching_event_per_team", args)
	assert.Equal(t, "game_ids must be non-empty", resultText(t, result))
	assert.Empty(t, db.calls)
}
