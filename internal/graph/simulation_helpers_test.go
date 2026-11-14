package graph

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/fixtures/simfixtures"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/worker/simulation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
)

// helperFixtureSeason is the seasons row TestCreateSimPoolStartsTheStoredWorkflowID
// creates its pool in.
const helperFixtureSeason = 20242025

func TestDecodeSimPoolBaseRendersWindowDates(t *testing.T) {
	t.Parallel()
	out := decodeSimPoolBase(sqlcdb.SimPool{
		SimDate:   pgtype.Date{Time: time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC), Valid: true},
		StartDate: pgtype.Date{Time: time.Date(2024, 10, 4, 0, 0, 0, 0, time.UTC), Valid: true},
		EndDate:   pgtype.Date{Time: time.Date(2025, 4, 17, 0, 0, 0, 0, time.UTC)},
	})
	require.NotNil(t, out.SimDate)
	assert.Equal(t, "2024-11-15", *out.SimDate)
	require.NotNil(t, out.StartDate)
	assert.Equal(t, "2024-10-04", *out.StartDate)
	assert.Nil(t, out.EndDate, "a NULL end_date is GraphQL null, whatever its time")
}

func TestDecodeSimAgentBaseDraftPosition(t *testing.T) {
	t.Parallel()
	assert.Nil(t, decodeSimAgentBase(sqlcdb.SimAgent{DraftPosition: pgtype.Int4{Int32: 3}}).DraftPosition)
	got := decodeSimAgentBase(sqlcdb.SimAgent{DraftPosition: pgtype.Int4{Int32: 3, Valid: true}}).DraftPosition
	require.NotNil(t, got)
	assert.Equal(t, 3, *got)
}

// TestDecodeSimTransactionsRendersNullableColumns pins the GraphQL shape of
// every nullable column: NULL is null, a value (zero and empty included) is
// kept. No player IDs and no lineup_set rows, so nothing reaches the
// database.
func TestDecodeSimTransactionsRendersNullableColumns(t *testing.T) {
	t.Parallel()
	txs := []sqlcdb.SimTransaction{
		{ID: 1, AgentID: 2, Type: "draft_pick",
			Date:  pgtype.Date{Time: time.Date(2024, 10, 4, 0, 0, 0, 0, time.UTC), Valid: true},
			Round: pgtype.Int4{Int32: 1, Valid: true}, Pick: pgtype.Int4{Valid: true},
			ErrorKind: pgtype.Text{String: "timeout", Valid: true}, ErrorDetail: pgtype.Text{Valid: true}},
		{ID: 3, AgentID: 2, Type: "add",
			Round: pgtype.Int4{Int32: 9}, ErrorKind: pgtype.Text{String: "ignored"}},
	}
	out, err := (&Resolver{}).decodeSimTransactions(context.Background(), txs)
	require.NoError(t, err)
	require.Len(t, out, 2)

	assert.Equal(t, "2024-10-04", out[0].Date)
	require.NotNil(t, out[0].Round)
	assert.Equal(t, 1, *out[0].Round)
	require.NotNil(t, out[0].Pick)
	assert.Equal(t, 0, *out[0].Pick)
	require.NotNil(t, out[0].ErrorKind)
	assert.Equal(t, "timeout", *out[0].ErrorKind)
	require.NotNil(t, out[0].ErrorDetail)
	assert.Equal(t, "", *out[0].ErrorDetail)

	assert.Equal(t, "", out[1].Date, "a NULL date renders empty")
	assert.Nil(t, out[1].Round)
	assert.Nil(t, out[1].Pick)
	assert.Nil(t, out[1].ErrorKind)
	assert.Nil(t, out[1].ErrorDetail)
}

// TestSimTransactionsRejectsBadDate pins the date filter's error text,
// unchanged from the resolver's former private parser.
func TestSimTransactionsRejectsBadDate(t *testing.T) {
	t.Parallel()
	q := &queryResolver{&Resolver{}} // no Queries: must fail before any lookup
	for _, bad := range []string{"2024-02-30", "11/15/2024", "2024-11-5", "2024-11-15T00:00:00Z"} {
		date := bad
		_, err := q.SimTransactions(context.Background(), 1, nil, &date, nil)
		require.Error(t, err)
		assert.Regexp(t, `^invalid date "`+date+`" \(expected YYYY-MM-DD\): parsing time `, err.Error())
		var parseErr *time.ParseError
		assert.ErrorAs(t, err, &parseErr)
	}
}

func TestParseSimWindowErrorText(t *testing.T) {
	t.Parallel()
	seasonStart := time.Date(2024, 10, 4, 0, 0, 0, 0, time.UTC)
	seasonEnd := time.Date(2025, 4, 17, 0, 0, 0, 0, time.UTC)
	bad, early, late := "2025-02-30", "2024-10-03", "2025-03-31"
	inverted := "2025-03-01"

	_, _, err := parseSimWindow(nil, &bad, seasonStart, seasonEnd)
	assert.EqualError(t, err, `invalid endDate "2025-02-30" (expected YYYY-MM-DD): parsing time "2025-02-30": day out of range`)

	_, _, err = parseSimWindow(&early, nil, seasonStart, seasonEnd)
	assert.EqualError(t, err, "startDate 2024-10-03 outside the season's standings range (2024-10-04 .. 2025-04-17)")

	_, _, err = parseSimWindow(&late, &inverted, seasonStart, seasonEnd)
	assert.EqualError(t, err, "startDate 2025-03-31 is after endDate 2025-03-01")
}

func TestCancelSimPoolCancelsPoolWorkflow(t *testing.T) {
	t.Parallel()
	boom := errors.New("temporal down")
	mc := &MockTemporalClient{}
	mc.On("CancelWorkflow", mock.Anything, "sim-pool-42", "").Return(boom)

	_, err := (&mutationResolver{&Resolver{TemporalClient: mc}}).CancelSimPool(context.Background(), 42)
	require.ErrorIs(t, err, boom)
	mc.AssertExpectations(t)
}

// TestCreateSimPoolStartsTheStoredWorkflowID checks, on real PostgreSQL,
// that createSimPool stores the validated config and starts the workflow
// under the ID the sim_pools.workflow_id generated column stores for the
// new pool.
func TestCreateSimPoolStartsTheStoredWorkflowID(t *testing.T) {
	t.Parallel()
	pool := simfixtures.NewPool(t, simfixtures.NewDatabase(t))
	simfixtures.NewSeeder(t, pool).Exec(`INSERT INTO seasons (id, standings_start, standings_end)
		VALUES ($1, DATE '2024-10-04', DATE '2025-04-17')`, helperFixtureSeason)

	var started client.StartWorkflowOptions
	var startedWorkflow any
	mc := &MockTemporalClient{}
	mc.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			started = args.Get(1).(client.StartWorkflowOptions)
			startedWorkflow = args.Get(2)
		}).
		Return(&MockWorkflowRun{}, nil)
	r := &Resolver{DB: pool, Queries: sqlcdb.New(pool), TemporalClient: mc}

	input := validSimPoolInput()
	stop := "team_name"
	input.StopAfter = &stop
	created, err := r.createSimPoolImpl(context.Background(), input)
	require.NoError(t, err)

	var stored, storedStop string
	var categories []string
	var rosterUtil, rosterBN, rosterIR int32
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT workflow_id, stop_after, categories, roster_util, roster_bn, roster_ir
		FROM sim_pools WHERE id = $1`, created.ID).
		Scan(&stored, &storedStop, &categories, &rosterUtil, &rosterBN, &rosterIR))
	assert.Equal(t, stored, started.ID)
	assert.Equal(t, "team_name", storedStop)
	assert.Equal(t, simulation.SupportedCategories(), categories)
	assert.Equal(t, []int32{1, 6, 3}, []int32{rosterUtil, rosterBN, rosterIR})
	var providers []string
	rows, err := pool.Query(context.Background(),
		`SELECT provider FROM sim_agents WHERE pool_id = $1 ORDER BY id`, created.ID)
	require.NoError(t, err)
	for rows.Next() {
		var p string
		require.NoError(t, rows.Scan(&p))
		providers = append(providers, p)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"anthropic", "ollama"}, providers, "providers are stored lowercased")
	assert.Equal(t, simulation.WorkflowIDForPool(int32(created.ID)), started.ID)
	assert.Equal(t, simulation.SimPoolWorkflowName, startedWorkflow)
}

// TestDecodeSimTransactionsLoadsLineupMovesInOneQuery pins SIM-U6: the
// moves of every lineup_set row come from one batched query, not one
// query per row. No player IDs, so no name lookup follows.
func TestDecodeSimTransactionsLoadsLineupMovesInOneQuery(t *testing.T) {
	t.Parallel()
	db := &recordingDBTX{}
	txs := []sqlcdb.SimTransaction{
		{ID: 1, Type: string(simulation.TransactionTypeLineupSet)},
		{ID: 2, Type: string(simulation.TransactionTypeAdd)},
		{ID: 3, Type: string(simulation.TransactionTypeLineupSet)},
		{ID: 4, Type: string(simulation.TransactionTypeLineupSet)},
	}
	_, err := resolverWithDB(db).decodeSimTransactions(context.Background(), txs)
	require.NoError(t, err)
	assert.Equal(t, []string{"ListSimLineupMovesByTransactions"}, db.recorded())

	db = &recordingDBTX{}
	_, err = resolverWithDB(db).decodeSimTransactions(context.Background(), txs[1:2])
	require.NoError(t, err)
	assert.Empty(t, db.recorded(), "no lineup_set row, no moves query")
}

func TestDecodeSimTransactionsWrapsALineupMovesQueryFailure(t *testing.T) {
	t.Parallel()
	queryErr := errors.New("connection reset")
	db := &recordingDBTX{queryErr: queryErr}
	txs := []sqlcdb.SimTransaction{
		{ID: 1, Type: string(simulation.TransactionTypeLineupSet)},
		{ID: 2, Type: string(simulation.TransactionTypeLineupSet)},
	}
	_, err := resolverWithDB(db).decodeSimTransactions(context.Background(), txs)
	require.ErrorIs(t, err, queryErr)
	assert.Contains(t, err.Error(), "list lineup moves for 2 transactions")
}

// TestSimTransactionsGroupsLineupMovesOnPostgres checks the batched moves
// query on real PostgreSQL: each lineup_set row gets its own moves, in
// sequence order, with player and displaced-player names.
func TestSimTransactionsGroupsLineupMovesOnPostgres(t *testing.T) {
	t.Parallel()
	pool := simfixtures.NewPool(t, simfixtures.NewDatabase(t))
	seed := simfixtures.NewSeeder(t, pool)
	poolID := seed.Pool("lineups")
	agentID := seed.Agent(simfixtures.Agent{PoolID: poolID, Model: "claude-sonnet-4-6"})
	for id, last := range map[int64]string{1: "One", 2: "Two", 3: "Three", 4: "Four"} {
		seed.Player(id, "P", last, nil)
	}
	insertTx := func(txType string, playerID *int64) int32 {
		var id int32
		require.NoError(t, pool.QueryRow(context.Background(),
			`INSERT INTO sim_transactions (pool_id, agent_id, date, type, player_id)
			VALUES ($1, $2, DATE '2024-10-10', $3, $4) RETURNING id`,
			poolID, agentID, txType, playerID).Scan(&id))
		return id
	}
	first := insertTx(string(simulation.TransactionTypeLineupSet), nil)
	four := int64(4)
	insertTx(string(simulation.TransactionTypeAdd), &four)
	second := insertTx(string(simulation.TransactionTypeLineupSet), nil)
	// Inserted out of sequence order; the query must sort them.
	seed.Exec(`INSERT INTO sim_lineup_moves (transaction_id, sequence, player_id, from_slot, to_slot, displaced_player_id)
		VALUES ($1, 2, 2, 'BN', 'LW', NULL), ($1, 1, 1, 'BN', 'C', 3), ($2, 1, 3, 'BN', 'D', NULL)`, first, second)

	out, err := (&queryResolver{&Resolver{Queries: sqlcdb.New(pool)}}).
		SimTransactions(context.Background(), int(poolID), nil, nil, nil)
	require.NoError(t, err)
	moves := map[int][]string{}
	for _, tx := range out {
		for _, m := range tx.LineupMoves {
			line := m.PlayerName + " " + m.FromSlot + "->" + m.ToSlot
			if m.DisplacedPlayerName != nil {
				line += " displacing " + *m.DisplacedPlayerName
			}
			moves[tx.ID] = append(moves[tx.ID], line)
		}
	}
	assert.Equal(t, map[int][]string{
		int(first):  {"P One BN->C displacing P Three", "P Two BN->LW"},
		int(second): {"P Three BN->D"},
	}, moves)
}

// TestDecodeSimTransactionsRejectsMalformedNumerics pins SIM-U6: a cost
// or cap that does not convert to a finite float fails the read, naming
// the row and column, instead of rendering as 0.
func TestDecodeSimTransactionsRejectsMalformedNumerics(t *testing.T) {
	t.Parallel()
	const tooLargeExponent = 400 // 1e400 overflows float64
	tests := map[string]struct {
		tx   sqlcdb.SimTransaction
		want string
	}{
		"cost out of float64 range": {
			sqlcdb.SimTransaction{ID: 7, CostUSD: pgtype.Numeric{Int: big.NewInt(1), Exp: tooLargeExponent, Valid: true}},
			"sim transaction 7 cost_usd: ",
		},
		"cost NaN": {
			sqlcdb.SimTransaction{ID: 8, CostUSD: pgtype.Numeric{NaN: true, Valid: true}},
			"sim transaction 8 cost_usd: not a finite number",
		},
		"cap infinite": {
			sqlcdb.SimTransaction{ID: 9, CapUSD: pgtype.Numeric{InfinityModifier: pgtype.Infinity, Valid: true}},
			"sim transaction 9 cap_usd: not a finite number",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := (&Resolver{}).decodeSimTransactions(context.Background(), []sqlcdb.SimTransaction{tc.tx})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	cost := pgtype.Numeric{Int: big.NewInt(125), Exp: -2, Valid: true}
	out, err := (&Resolver{}).decodeSimTransactions(context.Background(),
		[]sqlcdb.SimTransaction{{ID: 10, CostUSD: cost}})
	require.NoError(t, err)
	require.NotNil(t, out[0].CostUsd)
	assert.InDelta(t, 1.25, *out[0].CostUsd, 1e-9)
	assert.Nil(t, out[0].CapUsd, "a NULL cap stays null")
}
