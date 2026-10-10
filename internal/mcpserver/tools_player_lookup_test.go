package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	matchedPlayerID   int64 = 8480018
	matchedYahooID    int64 = 7520
	unmatchedPlayerID int64 = 8999999
	unmatchedYahooID  int64 = 9999
)

// positiveIDRange ends the error for an ID outside 1..maxExactJSONInteger.
const positiveIDRange = ": want an integer from 1 to 9007199254740991"

var errDatabaseDown = errors.New("database down")

// fakePlayerQueries holds one NHL player matched to a Yahoo ID and records
// which query each call ran.
type fakePlayerQueries struct {
	err         error
	calls       []string
	searchName  string
	searchParts sqlcdb.SearchPlayersByFullNameParams
}

func matchedPlayer() sqlcdb.Player {
	return sqlcdb.Player{
		ID:        matchedPlayerID,
		YahooID:   pgtype.Int8{Int64: matchedYahooID, Valid: true},
		FirstName: "Nick",
		LastName:  "Suzuki",
	}
}

func (f *fakePlayerQueries) GetPlayer(_ context.Context, id int64) (sqlcdb.Player, error) {
	f.calls = append(f.calls, "GetPlayer")
	if f.err != nil {
		return sqlcdb.Player{}, f.err
	}
	if id != matchedPlayerID {
		return sqlcdb.Player{}, pgx.ErrNoRows
	}
	return matchedPlayer(), nil
}

func (f *fakePlayerQueries) GetPlayerByYahooID(_ context.Context, yahooID pgtype.Int8) (sqlcdb.Player, error) {
	f.calls = append(f.calls, "GetPlayerByYahooID")
	if f.err != nil {
		return sqlcdb.Player{}, f.err
	}
	if !yahooID.Valid || yahooID.Int64 != matchedYahooID {
		return sqlcdb.Player{}, pgx.ErrNoRows
	}
	return matchedPlayer(), nil
}

func (f *fakePlayerQueries) SearchPlayersByName(_ context.Context, name string) ([]sqlcdb.Player, error) {
	f.calls = append(f.calls, "SearchPlayersByName")
	f.searchName = name
	return []sqlcdb.Player{matchedPlayer()}, nil
}

func (f *fakePlayerQueries) SearchPlayersByFullName(_ context.Context, arg sqlcdb.SearchPlayersByFullNameParams) ([]sqlcdb.Player, error) {
	f.calls = append(f.calls, "SearchPlayersByFullName")
	f.searchParts = arg
	return []sqlcdb.Player{matchedPlayer()}, nil
}

func unmatchedYahooMessage(yahooID int64) string {
	return fmt.Sprintf("no NHL player is matched to yahoo_id %d; the player may not be matched yet, try search_player by name", yahooID)
}

// lookupCase is one call of get_player or search_player that the tool
// refuses or answers with an error, and the query it may run first.
type lookupCase struct {
	args      map[string]any
	wantError string
	wantCalls []string
}

// sharedLookupErrors are the error answers both tools give for yahoo_id.
func sharedLookupErrors(alternative string, alternativeValue any) map[string]lookupCase {
	return map[string]lookupCase{
		"both given": {
			args:      map[string]any{alternative: alternativeValue, yahooIDArg: float64(matchedYahooID)},
			wantError: fmt.Sprintf("pass either %s or yahoo_id, not both", alternative),
		},
		"neither given": {
			args:      map[string]any{},
			wantError: fmt.Sprintf("%s or yahoo_id is required", alternative),
		},
		"unmatched yahoo_id": {
			args:      map[string]any{yahooIDArg: float64(unmatchedYahooID)},
			wantError: unmatchedYahooMessage(unmatchedYahooID),
			wantCalls: []string{"GetPlayerByYahooID"},
		},
		"negative yahoo_id": {
			args:      map[string]any{yahooIDArg: float64(-1)},
			wantError: "invalid yahoo_id -1" + positiveIDRange,
		},
		"non-numeric yahoo_id": {
			args:      map[string]any{yahooIDArg: "465.p.7520"},
			wantError: `invalid yahoo_id "465.p.7520"` + positiveIDRange,
		},
		"fractional yahoo_id": {
			args:      map[string]any{yahooIDArg: 7520.5},
			wantError: "invalid yahoo_id 7520.5" + positiveIDRange,
		},
	}
}

func runLookupErrorCases(t *testing.T, newHandler func(playerQueries) server.ToolHandlerFunc, cases map[string]lookupCase) {
	t.Helper()
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			q := &fakePlayerQueries{}
			result := callTool(t, newHandler(q), tc.args)
			assert.True(t, result.IsError)
			assert.Equal(t, tc.wantError, resultText(t, result))
			assert.Equal(t, tc.wantCalls, q.calls)
		})
	}
}

func getPlayerHandler(q playerQueries) server.ToolHandlerFunc {
	return getPlayerTool(q).Handler
}

func searchPlayerHandler(q playerQueries) server.ToolHandlerFunc {
	return searchPlayerTool(q).Handler
}

func TestPlayerLookupToolsTakeOptionalYahooID(t *testing.T) {
	for _, tool := range []mcp.Tool{getPlayerTool(nil).Tool, searchPlayerTool(nil).Tool} {
		t.Run(tool.Name, func(t *testing.T) {
			assert.Contains(t, tool.InputSchema.Properties, yahooIDArg)
			assert.Empty(t, tool.InputSchema.Required, "the alternatives are checked by the handler")
			assert.Contains(t, tool.Description, "Yahoo player ID")
		})
	}
}

func TestGetPlayerErrors(t *testing.T) {
	cases := sharedLookupErrors(playerIDArg, float64(matchedPlayerID))
	cases["unknown player_id"] = lookupCase{
		args:      map[string]any{playerIDArg: float64(unmatchedPlayerID)},
		wantError: fmt.Sprintf("no player with player_id %d", unmatchedPlayerID),
		wantCalls: []string{"GetPlayer"},
	}
	cases["negative player_id"] = lookupCase{
		args:      map[string]any{playerIDArg: float64(-1)},
		wantError: "invalid player_id -1" + positiveIDRange,
	}
	runLookupErrorCases(t, getPlayerHandler, cases)
}

func TestSearchPlayerErrors(t *testing.T) {
	cases := sharedLookupErrors(playerNameArg, "Suzuki")
	cases["blank name"] = lookupCase{
		args:      map[string]any{playerNameArg: "   "},
		wantError: "name or yahoo_id is required",
	}
	runLookupErrorCases(t, searchPlayerHandler, cases)
}

func TestGetPlayerFindsPlayer(t *testing.T) {
	tests := map[string]struct {
		args      map[string]any
		wantCalls []string
	}{
		"by player_id":                {map[string]any{playerIDArg: float64(matchedPlayerID)}, []string{"GetPlayer"}},
		"by yahoo_id":                 {map[string]any{yahooIDArg: float64(matchedYahooID)}, []string{"GetPlayerByYahooID"}},
		"by yahoo_id as string":       {map[string]any{yahooIDArg: fmt.Sprint(matchedYahooID)}, []string{"GetPlayerByYahooID"}},
		"zero player_id means absent": {map[string]any{playerIDArg: float64(0), yahooIDArg: float64(matchedYahooID)}, []string{"GetPlayerByYahooID"}},
		"zero yahoo_id means absent":  {map[string]any{playerIDArg: float64(matchedPlayerID), yahooIDArg: float64(0)}, []string{"GetPlayer"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			q := &fakePlayerQueries{}
			result := callTool(t, getPlayerTool(q).Handler, tt.args)
			require.False(t, result.IsError, resultText(t, result))
			assert.Equal(t, tt.wantCalls, q.calls)

			var got struct {
				ID      int64 `json:"id"`
				YahooID int64 `json:"yahoo_id"`
			}
			require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &got))
			assert.Equal(t, matchedPlayerID, got.ID)
			assert.Equal(t, matchedYahooID, got.YahooID)
		})
	}
}

func TestSearchPlayerByYahooIDReturnsMatchedPlayer(t *testing.T) {
	q := &fakePlayerQueries{}
	result := callTool(t, searchPlayerTool(q).Handler, map[string]any{yahooIDArg: float64(matchedYahooID)})
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, []string{"GetPlayerByYahooID"}, q.calls)

	rows := parseCSV(t, resultText(t, result))
	require.Len(t, rows, 2, "header and one player")
	assert.Equal(t, fmt.Sprint(matchedPlayerID), rows[1][columnIndex(t, rows[0], "id")])
	assert.Equal(t, fmt.Sprint(matchedYahooID), rows[1][columnIndex(t, rows[0], "yahoo_id")])
}

func TestSearchPlayerByName(t *testing.T) {
	q := &fakePlayerQueries{}
	result := callTool(t, searchPlayerTool(q).Handler, map[string]any{playerNameArg: "  Suzuki "})
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, []string{"SearchPlayersByName"}, q.calls)
	assert.Equal(t, "Suzuki", q.searchName)

	q = &fakePlayerQueries{}
	result = callTool(t, searchPlayerTool(q).Handler, map[string]any{playerNameArg: "Pierre Luc Dubois"})
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, []string{"SearchPlayersByFullName"}, q.calls)
	assert.Equal(t, sqlcdb.SearchPlayersByFullNameParams{Lower: "Pierre", Lower_2: "Luc Dubois"}, q.searchParts)
}

// TestYahooLookupPassesDatabaseErrors keeps a real failure distinct from
// "not found".
func TestYahooLookupPassesDatabaseErrors(t *testing.T) {
	for name, tool := range map[string]server.ToolHandlerFunc{
		"get_player":    getPlayerHandler(&fakePlayerQueries{err: errDatabaseDown}),
		"search_player": searchPlayerHandler(&fakePlayerQueries{err: errDatabaseDown}),
	} {
		t.Run(name, func(t *testing.T) {
			result := callTool(t, tool, map[string]any{yahooIDArg: float64(matchedYahooID)})
			assert.True(t, result.IsError)
			assert.Equal(t, errDatabaseDown.Error(), resultText(t, result))
		})
	}
}
