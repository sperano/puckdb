package mcpserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTransactionQueries adds the transaction query to fakeYahooQueries.
type fakeTransactionQueries struct {
	*fakeYahooQueries
	moves    []sqlcdb.ListYahooTransactionMovesRow
	movesErr error
	calls    []sqlcdb.ListYahooTransactionMovesParams
}

func (f *fakeTransactionQueries) ListYahooTransactionMoves(_ context.Context, arg sqlcdb.ListYahooTransactionMovesParams) ([]sqlcdb.ListYahooTransactionMovesRow, error) {
	f.calls = append(f.calls, arg)
	return f.moves, f.movesErr
}

func newFakeTransactionQueries() *fakeTransactionQueries {
	return &fakeTransactionQueries{fakeYahooQueries: newFakeYahooQueries()}
}

// callTransactionsTool calls get_yahoo_transactions over q for leagueID with
// extra arguments.
func callTransactionsTool(t *testing.T, q yahooQueries, leagueID int32, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	tool := yahooTestServer(q, nil).GetTool(transactionsToolName)
	require.NotNil(t, tool)
	all := map[string]any{leagueIDArg: float64(leagueID)}
	for name, value := range args {
		all[name] = value
	}
	return callTool(t, tool.Handler, all)
}

func TestYahooTransactionsToolPassesFilters(t *testing.T) {
	sinceDay := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	sinceInstant := time.Date(2026, 10, 1, 12, 30, 0, 0, time.UTC)
	tests := []struct {
		name string
		args map[string]any
		want sqlcdb.ListYahooTransactionMovesParams
	}{
		{"defaults", nil, sqlcdb.ListYahooTransactionMovesParams{RowLimit: defaultTransactionsLimit}},
		{"type is trimmed and lower-cased", map[string]any{transactionTypeArg: " Add/Drop "},
			sqlcdb.ListYahooTransactionMovesParams{RowLimit: defaultTransactionsLimit, MoveType: pgtype.Text{String: "add/drop", Valid: true}}},
		{"empty type and since are no filter", map[string]any{transactionTypeArg: "  ", transactionSinceArg: ""},
			sqlcdb.ListYahooTransactionMovesParams{RowLimit: defaultTransactionsLimit}},
		{"since date is midnight UTC", map[string]any{transactionSinceArg: "2026-10-01"},
			sqlcdb.ListYahooTransactionMovesParams{RowLimit: defaultTransactionsLimit, SinceTs: pgtype.Int8{Int64: sinceDay.Unix(), Valid: true}}},
		{"since timestamp keeps its offset", map[string]any{transactionSinceArg: "2026-10-01T08:30:00-04:00"},
			sqlcdb.ListYahooTransactionMovesParams{RowLimit: defaultTransactionsLimit, SinceTs: pgtype.Int8{Int64: sinceInstant.Unix(), Valid: true}}},
		{"maximum limit", map[string]any{"limit": float64(maxTransactionsLimit)},
			sqlcdb.ListYahooTransactionMovesParams{RowLimit: maxTransactionsLimit}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := newFakeTransactionQueries()
			result := callTransactionsTool(t, q, allowedLeagueID, tt.args)
			require.False(t, result.IsError, resultText(t, result))
			tt.want.LeagueID = allowedLeagueID
			assert.Equal(t, []sqlcdb.ListYahooTransactionMovesParams{tt.want}, q.calls)
		})
	}
}

func TestYahooTransactionsToolRefusesBadArguments(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"zero limit", map[string]any{"limit": float64(0)}, "invalid limit 0: want an integer from 1 to 500"},
		{"limit over maximum", map[string]any{"limit": float64(maxTransactionsLimit + 1)}, "invalid limit 501: want an integer from 1 to 500"},
		{"fractional limit", map[string]any{"limit": 2.5}, "invalid limit 2.5: want an integer from 1 to 500"},
		{"impossible date", map[string]any{transactionSinceArg: "2026-02-30"},
			`invalid since "2026-02-30": want YYYY-MM-DD or an RFC 3339 timestamp`},
		{"timestamp without zone", map[string]any{transactionSinceArg: "2026-10-01T08:30:00"},
			`invalid since "2026-10-01T08:30:00": want YYYY-MM-DD or an RFC 3339 timestamp`},
		{"numeric since", map[string]any{transactionSinceArg: float64(1759300000)},
			"invalid since 1759300000: want YYYY-MM-DD or an RFC 3339 timestamp"},
		{"non-string type", map[string]any{transactionTypeArg: true}, "invalid type true: want a string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := newFakeTransactionQueries()
			result := callTransactionsTool(t, q, allowedLeagueID, tt.args)
			assert.True(t, result.IsError)
			assert.Equal(t, tt.want, resultText(t, result))
			assert.Empty(t, q.calls, "the query must not run with a bad argument")
		})
	}
}

func TestYahooTransactionsToolFormatsMoves(t *testing.T) {
	at := time.Date(2026, 10, 3, 14, 5, 9, 0, time.UTC)
	q := newFakeTransactionQueries()
	q.moves = []sqlcdb.ListYahooTransactionMovesRow{
		{
			Timestamp: pgtype.Int8{Int64: at.Unix(), Valid: true}, TransactionKey: "465.l.1001.tr.7",
			TransactionType: "add/drop", Status: pgtype.Text{String: "successful", Valid: true}, MoveType: "add",
			YahooPlayerID: 6001, NhlPlayerID: pgtype.Int8{Int64: 8478402, Valid: true}, PlayerName: "Connor Center",
			SourceType: "freeagents", DestinationType: teamMoveSide, DestinationTeamID: pgtype.Int4{Int32: 3, Valid: true},
			DestinationTeamName: "Pucks Deep", DestinationTeamKey: "465.l.1001.t.3",
		},
		{
			TransactionKey: "465.l.1001.tr.2", TransactionType: "trade", MoveType: "trade", YahooPlayerID: 6002,
			SourceType: teamMoveSide, SourceTeamKey: "465.l.1001.t.9", DestinationType: "waivers",
		},
	}

	result := callTransactionsTool(t, q, allowedLeagueID, nil)
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, [][]string{
		{"time", "transaction_key", "transaction_type", "status", "move_type", "yahoo_player_id",
			"nhl_player_id", "player_name", "from", "from_team_id", "to", "to_team_id"},
		{"2026-10-03T14:05:09Z", "465.l.1001.tr.7", "add/drop", "successful", "add", "6001",
			"8478402", "Connor Center", "freeagents", "", "Pucks Deep", "3"},
		{"", "465.l.1001.tr.2", "trade", "", "trade", "6002", "", "", "465.l.1001.t.9", "", "waivers", ""},
	}, parseCSV(t, resultText(t, result)))
}

func TestYahooTransactionsToolRefusesLeagueNotImported(t *testing.T) {
	const missingLeagueID = 4040
	q := newFakeTransactionQueries()
	result := callTransactionsTool(t, q, missingLeagueID, nil)
	assert.True(t, result.IsError)
	assert.Equal(t, "unknown league_id 4040", resultText(t, result))
	assert.Empty(t, q.calls)
}

func TestYahooTransactionsToolReportsErrors(t *testing.T) {
	q := newFakeTransactionQueries()
	q.movesErr = errors.New("connection refused")
	result := callTransactionsTool(t, q, allowedLeagueID, nil)
	assert.True(t, result.IsError)
	assert.Equal(t, "connection refused", resultText(t, result))

	q = newFakeTransactionQueries()
	q.lookupErr = errors.New("lookup failed")
	result = callTransactionsTool(t, q, allowedLeagueID, nil)
	assert.True(t, result.IsError)
	assert.Equal(t, "lookup failed", resultText(t, result))
	assert.Empty(t, q.calls)
}

func TestMoveSide(t *testing.T) {
	assert.Equal(t, "Pucks Deep", moveSide(teamMoveSide, "Pucks Deep", "465.l.1.t.3"))
	assert.Equal(t, "465.l.1.t.3", moveSide(teamMoveSide, "", "465.l.1.t.3"))
	assert.Equal(t, "freeagents", moveSide("freeagents", "", ""))
	assert.Equal(t, "", moveSide("", "", ""))
}

// PostgreSQL fixture of TestYahooTransactionsToolReadsDatabase: league
// 990747 with two teams, and another league whose transaction must never
// show up.
const (
	pgTxLeagueID       = 990747
	pgTxLeagueKey      = "465.l.990747"
	pgTxOtherLeagueID  = 990748
	pgTxOtherLeagueKey = "465.l.990748"
	pgTxTeam1Key       = pgTxLeagueKey + ".t.1"
	pgTxTeam2Key       = pgTxLeagueKey + ".t.2"
	// Yahoo player IDs; pgTxNHLPlayerID is matched to pgTxNHLMatchedYahooID.
	pgTxPoolYahooID       = 990747001
	pgTxNHLMatchedYahooID = 990747002
	pgTxTradeYahooID1     = 990747003
	pgTxTradeYahooID2     = 990747004
	pgTxAddYahooID        = 990747005
	pgTxUndatedYahooID    = 990747006
	pgTxNHLPlayerID       = 990747002
)

var pgTxDay = time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)

// pgTxMove is one stored player move of the fixture.
type pgTxMove struct {
	key, txType         string
	at                  *time.Time
	playerID            int
	moveType            string
	srcType, srcTeamKey string
	dstType, dstTeamKey string
	leagueID            int
}

func pgTxMoves() []pgTxMove {
	day := func(d int, h int) *time.Time {
		at := pgTxDay.AddDate(0, 0, d).Add(time.Duration(h) * time.Hour)
		return &at
	}
	return []pgTxMove{
		{key: "tr.1", txType: "add", at: day(0, 9), playerID: pgTxAddYahooID, moveType: "add",
			srcType: "freeagents", dstType: teamMoveSide, dstTeamKey: pgTxTeam2Key},
		{key: "tr.2", txType: "add/drop", at: day(1, 10), playerID: pgTxPoolYahooID, moveType: "add",
			srcType: "freeagents", dstType: teamMoveSide, dstTeamKey: pgTxTeam1Key},
		{key: "tr.2", txType: "add/drop", at: day(1, 10), playerID: pgTxNHLMatchedYahooID, moveType: "drop",
			srcType: teamMoveSide, srcTeamKey: pgTxTeam1Key, dstType: "waivers"},
		{key: "tr.3", txType: "trade", at: day(2, 11), playerID: pgTxTradeYahooID2, moveType: "trade",
			srcType: teamMoveSide, srcTeamKey: pgTxTeam2Key, dstType: teamMoveSide, dstTeamKey: pgTxTeam1Key},
		{key: "tr.3", txType: "trade", at: day(2, 11), playerID: pgTxTradeYahooID1, moveType: "trade",
			srcType: teamMoveSide, srcTeamKey: pgTxTeam1Key, dstType: teamMoveSide, dstTeamKey: pgTxTeam2Key},
		{key: "tr.4", txType: "commish", playerID: pgTxUndatedYahooID, moveType: "add",
			srcType: "freeagents", dstType: teamMoveSide, dstTeamKey: pgTxTeam2Key},
		{key: "tr.9", txType: "trade", at: day(3, 0), playerID: pgTxTradeYahooID1, moveType: "trade",
			srcType: teamMoveSide, dstType: teamMoveSide, leagueID: pgTxOtherLeagueID},
	}
}

func TestYahooTransactionsToolReadsDatabase(t *testing.T) {
	pool := openMCPTestDB(t)
	cleanupTransactionLeagues(t, pool)
	t.Cleanup(func() { cleanupTransactionLeagues(t, pool) })
	seedTransactionLeagues(t, pool)
	tool := yahooTestServer(sqlcdb.New(pool), []string{pgTxLeagueKey}).GetTool(transactionsToolName)
	require.NotNil(t, tool)

	call := func(t *testing.T, args map[string]any) [][]string {
		t.Helper()
		all := map[string]any{leagueIDArg: float64(pgTxLeagueID)}
		for name, value := range args {
			all[name] = value
		}
		result := callTool(t, tool.Handler, all)
		text := resultText(t, result)
		require.False(t, result.IsError, text)
		if text == noResultsText {
			return nil
		}
		return parseCSV(t, text)
	}

	t.Run("all moves newest first, undated last", func(t *testing.T) {
		rows := call(t, nil)
		assert.Equal(t, []string{
			"tr.3 trade 990747003 Team One -> Team Two",
			"tr.3 trade 990747004 Team Two -> Team One",
			"tr.2 add 990747001 freeagents -> Team One",
			"tr.2 drop 990747002 Team One -> waivers",
			"tr.1 add 990747005 freeagents -> Team Two",
			"tr.4 add 990747006 freeagents -> Team Two",
		}, moveSummaries(t, rows))
		header := rows[0]
		byPlayer := map[string][]string{}
		for _, row := range rows[1:] {
			byPlayer[row[columnIndex(t, header, "yahoo_player_id")]] = row
		}
		name := columnIndex(t, header, "player_name")
		assert.Equal(t, "Pool Name", byPlayer["990747001"][name], "name from the league's Yahoo pool")
		assert.Equal(t, "Nhl Matched", byPlayer["990747002"][name], "name from the matched NHL player")
		assert.Equal(t, "990747002", byPlayer["990747002"][columnIndex(t, header, "nhl_player_id")])
		assert.Equal(t, "", byPlayer["990747003"][name], "unknown player")
		assert.Equal(t, "1", byPlayer["990747003"][columnIndex(t, header, "from_team_id")])
		assert.Equal(t, "2", byPlayer["990747003"][columnIndex(t, header, "to_team_id")])
		assert.Equal(t, "2026-10-12T11:00:00Z", byPlayer["990747003"][columnIndex(t, header, "time")])
		assert.Equal(t, "", byPlayer["990747006"][columnIndex(t, header, "time")])
	})
	t.Run("limit counts transactions", func(t *testing.T) {
		assert.Equal(t, []string{
			"tr.3 trade 990747003 Team One -> Team Two",
			"tr.3 trade 990747004 Team Two -> Team One",
		}, moveSummaries(t, call(t, map[string]any{"limit": float64(1)})))
	})
	t.Run("type matches the move", func(t *testing.T) {
		assert.Equal(t, []string{
			"tr.2 add 990747001 freeagents -> Team One",
			"tr.1 add 990747005 freeagents -> Team Two",
			"tr.4 add 990747006 freeagents -> Team Two",
		}, moveSummaries(t, call(t, map[string]any{transactionTypeArg: "add"})))
	})
	t.Run("type matches the transaction", func(t *testing.T) {
		assert.Equal(t, []string{
			"tr.2 add 990747001 freeagents -> Team One",
			"tr.2 drop 990747002 Team One -> waivers",
		}, moveSummaries(t, call(t, map[string]any{transactionTypeArg: "ADD/DROP"})))
	})
	t.Run("since drops older and undated transactions", func(t *testing.T) {
		assert.Equal(t, []string{
			"tr.3 trade 990747003 Team One -> Team Two",
			"tr.3 trade 990747004 Team Two -> Team One",
			"tr.2 add 990747001 freeagents -> Team One",
			"tr.2 drop 990747002 Team One -> waivers",
		}, moveSummaries(t, call(t, map[string]any{transactionSinceArg: "2026-10-11T10:00:00Z"})))
	})
	t.Run("no match", func(t *testing.T) {
		assert.Nil(t, call(t, map[string]any{transactionTypeArg: "waiver"}))
	})
}

// moveSummaries condenses each row to "key move_type player from -> to",
// with the transaction key's league prefix cut.
func moveSummaries(t *testing.T, rows [][]string) []string {
	t.Helper()
	require.NotEmpty(t, rows)
	header := rows[0]
	cols := []int{
		columnIndex(t, header, "transaction_key"), columnIndex(t, header, "move_type"),
		columnIndex(t, header, "yahoo_player_id"), columnIndex(t, header, "from"), columnIndex(t, header, "to"),
	}
	var out []string
	for _, row := range rows[1:] {
		key := row[cols[0]][len(pgTxLeagueKey)+1:]
		out = append(out, key+" "+row[cols[1]]+" "+row[cols[2]]+" "+row[cols[3]]+" -> "+row[cols[4]])
	}
	return out
}

func seedTransactionLeagues(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	for _, l := range []struct {
		id  int
		key string
	}{{pgTxLeagueID, pgTxLeagueKey}, {pgTxOtherLeagueID, pgTxOtherLeagueKey}} {
		exec(`INSERT INTO yahoo_leagues (id, league_key, name, season) VALUES ($1, $2, 'MCP test', $3)`, l.id, l.key, pgTestSeason)
	}
	exec(`INSERT INTO yahoo_teams (league_id, id, team_key, name) VALUES ($1, 1, $2, 'Team One'), ($1, 2, $3, 'Team Two')`,
		pgTxLeagueID, pgTxTeam1Key, pgTxTeam2Key)
	exec(`INSERT INTO players (id, yahoo_id, first_name, last_name) VALUES ($1, $2, 'Nhl', 'Matched')`,
		pgTxNHLPlayerID, pgTxNHLMatchedYahooID)
	exec(`INSERT INTO yahoo_league_players (league_key, season, league_id, game_key, player_id, player_key, full_name,
		 eligible_positions, fetched_at) VALUES ($1, $2, $3, 465, $4, '465.p.1', 'Pool Name', '{C}', now())`,
		pgTxLeagueKey, pgTestSeason, pgTxLeagueID, pgTxPoolYahooID)
	for _, m := range pgTxMoves() {
		leagueID, leagueKey := pgTxLeagueID, pgTxLeagueKey
		if m.leagueID != 0 {
			leagueID, leagueKey = m.leagueID, pgTxOtherLeagueKey
		}
		var ts pgtype.Int8
		if m.at != nil {
			ts = pgtype.Int8{Int64: m.at.Unix(), Valid: true}
		}
		key := leagueKey + "." + m.key
		exec(`INSERT INTO yahoo_transactions (league_id, transaction_key, type, timestamp, status)
		      VALUES ($1, $2, $3, $4, 'successful') ON CONFLICT DO NOTHING`, leagueID, key, m.txType, ts)
		exec(`INSERT INTO yahoo_transaction_players (league_id, transaction_key, player_id, type,
		      source_type, source_team_key, destination_type, destination_team_key)
		      VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			leagueID, key, m.playerID, m.moveType, m.srcType, m.srcTeamKey, m.dstType, m.dstTeamKey)
	}
}

// cleanupTransactionLeagues removes the fixture; transactions reference the
// league without a cascade, so they go first.
func cleanupTransactionLeagues(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	leagueIDs := []int32{pgTxLeagueID, pgTxOtherLeagueID}
	for _, sql := range []string{
		`DELETE FROM yahoo_transactions WHERE league_id = ANY($1)`,
		`DELETE FROM yahoo_league_players WHERE league_id = ANY($1)`,
		`DELETE FROM yahoo_leagues WHERE id = ANY($1)`,
	} {
		_, err := pool.Exec(ctx, sql, leagueIDs)
		require.NoError(t, err)
	}
	_, err := pool.Exec(ctx, `DELETE FROM players WHERE id = $1`, pgTxNHLPlayerID)
	require.NoError(t, err)
}
