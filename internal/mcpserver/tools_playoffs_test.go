package mcpserver

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlayoffRoundZeroMeansAllRounds(t *testing.T) {
	tests := map[string]struct {
		round any
		want  pgtype.Int4
	}{
		"absent": {},
		"zero":   {round: float64(0)},
		"final":  {round: float64(lastPlayoffRound), want: pgtype.Int4{Int32: lastPlayoffRound, Valid: true}},
	}
	for _, tool := range []string{"get_playoff_games", "get_playoff_series"} {
		for name, tc := range tests {
			t.Run(tool+"/"+name, func(t *testing.T) {
				args := map[string]any{"season": float64(testSeasonID)}
				if tc.round != nil {
					args["round"] = tc.round
				}
				result, db := callNHLTool(t, tool, args)
				require.False(t, result.IsError, resultText(t, result))
				require.Len(t, db.calls, 1)
				// Both queries bind season, then round.
				assert.Equal(t, testSeasonID, db.calls[0].args[0])
				assert.Equal(t, tc.want, db.calls[0].args[1])
			})
		}
	}
}

func TestPlayoffRoundOutOfRange(t *testing.T) {
	result, db := callNHLTool(t, "get_playoff_games", map[string]any{"season": float64(testSeasonID), "round": float64(lastPlayoffRound + 1)})
	assert.True(t, result.IsError)
	assert.Equal(t, "invalid round 5: want an integer from 1 to 4", resultText(t, result))
	assert.Empty(t, db.calls)
}

func TestListGamesZeroFiltersAreNull(t *testing.T) {
	result, db := callNHLTool(t, "list_games", map[string]any{"season": float64(0), "team_id": float64(0), "limit": float64(0)})
	require.False(t, result.IsError, resultText(t, result))
	require.Len(t, db.calls, 1)
	// ListGames binds season, game_type, game_state, team_id, start_date,
	// end_date, limit.
	args := db.calls[0].args
	assert.Equal(t, pgtype.Int4{}, args[0])
	assert.Equal(t, pgtype.Int8{}, args[3])
	assert.Equal(t, pgtype.Int4{}, args[6])
}

func TestListGamesPassesFilters(t *testing.T) {
	const teamID int64 = 8
	result, db := callNHLTool(t, "list_games", map[string]any{"season": float64(testSeasonID), "team_id": float64(teamID), "limit": float64(3)})
	require.False(t, result.IsError, resultText(t, result))
	args := db.calls[0].args
	assert.Equal(t, pgtype.Int4{Int32: testSeasonID, Valid: true}, args[0])
	assert.Equal(t, pgtype.Int8{Int64: teamID, Valid: true}, args[3])
	assert.Equal(t, pgtype.Int4{Int32: 3, Valid: true}, args[6])
}

func TestStanleyCupWinnersZeroLimitIsDefault(t *testing.T) {
	result, db := callNHLTool(t, "get_stanley_cup_winners", map[string]any{"limit": float64(0)})
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, []any{pgtype.Int4{}}, db.calls[0].args)
}
