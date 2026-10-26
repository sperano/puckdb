package graph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/fixtures/simfixtures"
	"github.com/sperano/puckdb/internal/graph/model"
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
// that createSimPool starts the workflow under the ID the
// sim_pools.workflow_id generated column stores for the new pool.
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

	created, err := r.createSimPoolImpl(context.Background(), model.CreateSimPoolInput{
		Name: "identity", Season: helperFixtureSeason, Categories: []string{"G"},
		WaiverDays: 2, DraftRounds: 2,
		Agents: []*model.CreateSimAgentInput{{Provider: "anthropic", Model: "claude-sonnet-4-6", Strategy: "s"}},
	})
	require.NoError(t, err)

	var stored string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT workflow_id FROM sim_pools WHERE id = $1`, created.ID).Scan(&stored))
	assert.Equal(t, stored, started.ID)
	assert.Equal(t, simulation.WorkflowIDForPool(int32(created.ID)), started.ID)
	assert.Equal(t, simulation.SimPoolWorkflowName, startedWorkflow)
}
