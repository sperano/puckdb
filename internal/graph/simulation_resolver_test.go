package graph

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/worker/simulation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SignalWorkflow extends the existing MockTemporalClient (defined in
// resolver_test.go) with the signal interface the simulation
// mutations need. Each test that exercises a signal mutation calls
// .On("SignalWorkflow", ...) to set an expectation.
func (m *MockTemporalClient) SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, arg any) error {
	args := m.Called(ctx, workflowID, runID, signalName, arg)
	return args.Error(0)
}

// ============================================================================
// Pure-function helper tests
// ============================================================================

func TestSimPoolWorkflowIDForPool(t *testing.T) {
	t.Parallel()
	cases := []struct {
		poolID   int32
		expected string
	}{
		{1, "sim-pool-1"},
		{42, "sim-pool-42"},
		{100, "sim-pool-100"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.expected, simPoolWorkflowIDForPool(tc.poolID))
	}
}

// flattenRosterPositions is the boundary helper that turns the
// GraphQL list-of-{slot, count} into the map form sqlc params want.
// Pinned independently of createSimPoolImpl so a future refactor of
// the resolver doesn't accidentally break the wire-format contract.
func TestFlattenRosterPositions(t *testing.T) {
	t.Parallel()
	in := []*model.SimRosterPositionInput{
		{Slot: "C", Count: 2},
		{Slot: "LW", Count: 2},
		{Slot: "BN", Count: 5},
	}
	got := flattenRosterPositions(in)
	assert.Equal(t, 2, got[simulation.SlotC])
	assert.Equal(t, 2, got[simulation.SlotLW])
	assert.Equal(t, 5, got[simulation.SlotBN])
	// Slots not in the input default to 0 via map lookup — no
	// zero-fill needed.
	assert.Equal(t, 0, got[simulation.SlotG])
}

// numericFromFloat / numericToFloat round-trip — pin the precision
// contract since the migration moved max_llm_cost_usd_per_pool +
// temperature to NUMERIC columns and the resolver is the boundary
// that encodes float64 input.
func TestNumericFromFloat_RoundTrip(t *testing.T) {
	t.Parallel()
	cases := []float64{0.0, 0.7, 200.0, 12.345678}
	for _, want := range cases {
		n, err := numericFromFloat(want)
		require.NoError(t, err)
		got, err := simulation.NumericToFloat(n)
		require.NoError(t, err)
		assert.InDelta(t, want, got, 1e-6)
	}
}

func TestParseDateOrError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		input     string
		wantValid bool
		wantErr   bool
	}{
		{"empty is no-filter", "", false, false},
		{"valid date", "2024-11-15", true, false},
		{"invalid format", "11/15/2024", false, true},
		{"garbage", "not a date", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDateOrError(tc.input)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.wantValid, got.Valid)
		})
	}
}

func TestFormatPgDate(t *testing.T) {
	t.Parallel()
	d := pgtype.Date{Time: time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC), Valid: true}
	assert.Equal(t, "2024-11-15", formatPgDate(d))
	assert.Empty(t, formatPgDate(pgtype.Date{}))
}

func TestSumRotoPoints(t *testing.T) {
	t.Parallel()
	rows := []sqlcdb.SimStanding{
		{RotoPoints: numericFromFloatForTest(t, 5.0)},
		{RotoPoints: numericFromFloatForTest(t, 3.5)},
		{RotoPoints: numericFromFloatForTest(t, 1.0)},
		{RotoPoints: pgtype.Numeric{}}, // invalid → skipped
	}
	assert.InDelta(t, 9.5, sumRotoPoints(rows), 1e-9)
}

func TestSumRotoPoints_Empty(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 0.0, sumRotoPoints(nil))
}

func TestDecodeSimPoolBase(t *testing.T) {
	t.Parallel()
	cost := numericFromFloatForTest(t, 12.34)
	in := sqlcdb.SimPool{
		ID: 7, Name: "Test", Season: 20242025, Status: "running",
		SimDate:         pgtype.Date{Time: time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC), Valid: true},
		TotalLLMCostUSD: cost,
	}
	out := decodeSimPoolBase(in)
	assert.Equal(t, 7, out.ID)
	assert.Equal(t, "Test", out.Name)
	assert.Equal(t, 20242025, out.Season)
	assert.Equal(t, "running", out.Status)
	require.NotNil(t, out.SimDate)
	assert.Equal(t, "2024-11-15", *out.SimDate)
	assert.InDelta(t, 12.34, out.TotalLlmCostUsd, 1e-6)
}

func TestDecodeSimPoolBase_NullSimDate(t *testing.T) {
	t.Parallel()
	in := sqlcdb.SimPool{ID: 1, Name: "Pre-draft"}
	out := decodeSimPoolBase(in)
	assert.Nil(t, out.SimDate, "null sim_date → nil pointer (GraphQL null)")
}

func TestDecodeStandings_SortsByAgentThenCategory(t *testing.T) {
	t.Parallel()
	rows := []sqlcdb.SimStanding{
		{AgentID: 2, Category: "G", Value: numericFromFloatForTest(t, 50), RotoPoints: numericFromFloatForTest(t, 5)},
		{AgentID: 1, Category: "G", Value: numericFromFloatForTest(t, 30), RotoPoints: numericFromFloatForTest(t, 3)},
		{AgentID: 1, Category: "A", Value: numericFromFloatForTest(t, 40), RotoPoints: numericFromFloatForTest(t, 2)},
	}
	out := decodeStandings(rows)
	require.Len(t, out, 3)
	// Sorted by (agent_id, category) ascending. Agent 1's "A" comes
	// before its "G"; agent 2's "G" comes last.
	assert.Equal(t, 1, out[0].AgentID)
	assert.Equal(t, "A", out[0].Category)
	assert.Equal(t, 1, out[1].AgentID)
	assert.Equal(t, "G", out[1].Category)
	assert.Equal(t, 2, out[2].AgentID)
}

func TestGroupStandingsByDate(t *testing.T) {
	t.Parallel()
	day1 := pgtype.Date{Time: time.Date(2024, 11, 1, 0, 0, 0, 0, time.UTC), Valid: true}
	day2 := pgtype.Date{Time: time.Date(2024, 11, 2, 0, 0, 0, 0, time.UTC), Valid: true}
	rows := []sqlcdb.SimStanding{
		{Date: day2, AgentID: 1, Category: "G", Value: numericFromFloatForTest(t, 10), RotoPoints: numericFromFloatForTest(t, 1)},
		{Date: day1, AgentID: 1, Category: "G", Value: numericFromFloatForTest(t, 5), RotoPoints: numericFromFloatForTest(t, 1)},
		{Date: day1, AgentID: 2, Category: "G", Value: numericFromFloatForTest(t, 3), RotoPoints: numericFromFloatForTest(t, 0)},
	}
	out := groupStandingsByDate(rows)
	require.Len(t, out, 2)
	// day1 first (sorted ascending by date string).
	assert.Len(t, out[0], 2, "day1 has two rows")
	assert.Len(t, out[1], 1, "day2 has one row")
}

// ptrStringIfNotEmpty (used above via decodeSimTransactions/decodeLineupMoves)
// is covered by TestPtrStringIfNotEmpty in convert_test.go — it used to have
// a graph/simulation_helpers.go-local twin named stringPtrIfNotEmpty with an
// identical body and identical test coverage; the twin was removed as part
// of the T3-A dedup pass.

func TestNullPositionString(t *testing.T) {
	t.Parallel()
	assert.Empty(t, nullPositionString(sqlcdb.NullPlayerPosition{Valid: false}))
	got := nullPositionString(sqlcdb.NullPlayerPosition{
		PlayerPosition: sqlcdb.PlayerPositionC, Valid: true,
	})
	assert.Equal(t, "C", got)
}

// ============================================================================
// Mutation resolver — signal flow via mock Temporal client.
//
// loadSimPool requires a Queries handle, so each signal test stops
// at the SignalWorkflow assertion: we make loadSimPool fail by
// nil-Queries (a ListSimPools call would panic) and assert the
// signal fired with the right workflow ID and signal name. The
// post-signal pool reload is exercised end-to-end in the integration
// test layer.
// ============================================================================

// numericFromFloatForTest constructs a pgtype.Numeric from a float64
// for fixture rows. Six fractional digits matches the precision
// the simulation package's numericFromFloat uses on the write path.
func numericFromFloatForTest(t require.TestingT, f float64) pgtype.Numeric {
	var n pgtype.Numeric
	require.NoError(t, n.Scan(strconv.FormatFloat(f, 'f', 6, 64)))
	return n
}

// ============================================================================
// PoolNotFoundError + mapGetSimPoolErr — typed lookup-miss handling
// ============================================================================

func TestPoolNotFoundError_Message(t *testing.T) {
	t.Parallel()
	e := &PoolNotFoundError{ID: 42}
	assert.Equal(t, "no pool found with id 42", e.Error())
}

func TestMapGetSimPoolErr_TranslatesNoRows(t *testing.T) {
	t.Parallel()
	got := mapGetSimPoolErr(pgx.ErrNoRows, 7)
	var nf *PoolNotFoundError
	require.True(t, errors.As(got, &nf), "pgx.ErrNoRows must surface as *PoolNotFoundError")
	assert.Equal(t, int32(7), nf.ID)
}

func TestMapGetSimPoolErr_WrapsOtherErrors(t *testing.T) {
	t.Parallel()
	boom := errors.New("connection reset")
	got := mapGetSimPoolErr(boom, 7)
	var nf *PoolNotFoundError
	assert.False(t, errors.As(got, &nf), "non-no-rows errors must NOT be PoolNotFoundError")
	assert.ErrorIs(t, got, boom, "underlying cause must remain unwrappable for log/diagnostic purposes")
	assert.Contains(t, got.Error(), "get sim pool 7")
}

// ============================================================================
// resolveLatestStandingsDate — B6: real DB errors must not be swallowed
// ============================================================================

func TestResolveLatestStandingsDate(t *testing.T) {
	t.Parallel()
	validDate := pgtype.Date{Time: time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC), Valid: true}

	t.Run("no error passes the date through", func(t *testing.T) {
		t.Parallel()
		got, err := resolveLatestStandingsDate(validDate, nil)
		require.NoError(t, err)
		assert.Equal(t, validDate, got)
	})

	t.Run("nil-valid date (no standings yet) passes through without error", func(t *testing.T) {
		t.Parallel()
		got, err := resolveLatestStandingsDate(pgtype.Date{}, nil)
		require.NoError(t, err)
		assert.False(t, got.Valid)
	})

	t.Run("ErrNoRows is treated as empty standings, not an error", func(t *testing.T) {
		t.Parallel()
		got, err := resolveLatestStandingsDate(pgtype.Date{}, pgx.ErrNoRows)
		require.NoError(t, err)
		assert.False(t, got.Valid, "no-rows must surface as an invalid (empty) date")
	})

	t.Run("a real DB error is returned, not swallowed", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("connection reset by peer")
		got, err := resolveLatestStandingsDate(validDate, boom)
		require.Error(t, err)
		assert.ErrorIs(t, err, boom, "underlying cause must remain unwrappable")
		assert.False(t, got.Valid, "on error the returned date must be the empty zero value")
	})
}

// TestCreateSimPoolImpl_RequiresDB pins B7's precondition: the transactional
// insert path needs a pgx pool handle. Without one it must fail fast with
// errDatabaseNotConfigured rather than nil-panic on r.DB.Begin. Full commit/
// rollback ordering is covered in the DB-backed integration layer.
func TestCreateSimPoolImpl_RequiresDB(t *testing.T) {
	t.Parallel()
	r := &Resolver{} // no DB, no Queries
	_, err := r.createSimPoolImpl(context.Background(), model.CreateSimPoolInput{})
	require.ErrorIs(t, err, errDatabaseNotConfigured)
}

// panicTxBeginner satisfies txBeginner so createSimPoolImpl gets past the
// errDatabaseNotConfigured check, but panics if actually called — pricing
// rejection must happen before any transaction is opened, let alone before
// the season lookup (which needs Queries, deliberately left nil here).
type panicTxBeginner struct{}

func (panicTxBeginner) Begin(context.Context) (pgx.Tx, error) {
	panic("createSimPoolImpl must reject an unpriced agent before opening a transaction")
}

// TestCreateSimPoolImpl_RejectsUnpricedModel pins SIM-U2's fix: an agent
// whose (provider, model) has no pricing-table row must fail pool
// creation up front, by name, instead of silently costing $0 forever.
func TestCreateSimPoolImpl_RejectsUnpricedModel(t *testing.T) {
	t.Parallel()
	r := &Resolver{DB: panicTxBeginner{}}
	input := model.CreateSimPoolInput{
		Agents: []*model.CreateSimAgentInput{
			{Provider: "anthropic", Model: "claude-sonnet-4-6"},
			{Provider: "openai", Model: "gpt-9-ultra-mystery"},
		},
	}
	_, err := r.createSimPoolImpl(context.Background(), input)
	require.Error(t, err)
	assert.ErrorIs(t, err, simulation.ErrUnpricedModel)
	assert.Contains(t, err.Error(), "agent #1", "the error must identify which agent is unpriced")
}

// errSeasonLookupProbe is configured as recordingDBTX.queryRowErr so a
// test can tell "got past pricing, failed at the season lookup" apart
// from "failed at pricing" without a real database.
var errSeasonLookupProbe = errors.New("season lookup probe")

// An Ollama agent with an arbitrary local model tag must not be rejected
// by the pricing gate, even though the tag has no table row. Reaching the
// (probed) season lookup proves the pricing gate let it through, rather
// than merely proving some later check failed for an unrelated reason.
func TestCreateSimPoolImpl_OllamaModelPassesPricingGate(t *testing.T) {
	t.Parallel()
	r := &Resolver{
		DB:      panicTxBeginner{},
		Queries: sqlcdb.New(&recordingDBTX{queryRowErr: errSeasonLookupProbe}),
	}
	input := model.CreateSimPoolInput{
		Agents: []*model.CreateSimAgentInput{
			{Provider: "ollama", Model: "some-custom-finetune:latest"},
		},
	}
	_, err := r.createSimPoolImpl(context.Background(), input)
	require.Error(t, err)
	assert.ErrorIs(t, err, errSeasonLookupProbe,
		"an Ollama agent must clear the pricing gate and fail at the season lookup, not at pricing")
}

// ============================================================================
// currentDraftAction — snake-draft "currently picking" derivation
// ============================================================================

// agentAt builds a sqlcdb.SimAgent fixture with id, team_name, and an
// optional shuffled draft_position (pass 0 for NULL).
func agentAt(id int32, teamName string, draftPos int32) sqlcdb.SimAgent {
	a := sqlcdb.SimAgent{ID: id, TeamName: teamName}
	if draftPos > 0 {
		a.DraftPosition = pgtype.Int4{Int32: draftPos, Valid: true}
	}
	return a
}

func TestCurrentDraftAction_NilWhenNotDraftStatus(t *testing.T) {
	t.Parallel()
	pool := sqlcdb.SimPool{Status: "running", DraftRounds: 2}
	got := currentDraftAction(pool, []sqlcdb.SimAgent{agentAt(1, "A", 1)}, 0)
	assert.Nil(t, got, "non-draft status must yield nil")
}

func TestCurrentDraftAction_PreShufflePlaceholder(t *testing.T) {
	t.Parallel()
	pool := sqlcdb.SimPool{Status: "draft", DraftRounds: 2}
	agents := []sqlcdb.SimAgent{
		{ID: 1, TeamName: "A"},
		{ID: 2, TeamName: "B"},
	}
	got := currentDraftAction(pool, agents, 0)
	require.NotNil(t, got, "pre-shuffle must show Round 1, Pick 1 placeholder")
	assert.Equal(t, 1, got.Round)
	assert.Equal(t, 1, got.Pick)
	assert.Equal(t, 4, got.TotalPicks) // 2 rounds × 2 agents
	assert.Equal(t, 0, got.AgentID, "AgentID=0 signals 'order not yet assigned'")
}

func TestCurrentDraftAction_NilWhenPoolHasNoAgents(t *testing.T) {
	t.Parallel()
	pool := sqlcdb.SimPool{Status: "draft", DraftRounds: 2}
	assert.Nil(t, currentDraftAction(pool, nil, 0),
		"draft pool with zero agents must yield nil — nothing to render")
}

func TestCurrentDraftAction_FirstPick(t *testing.T) {
	t.Parallel()
	pool := sqlcdb.SimPool{Status: "draft", DraftRounds: 3}
	agents := []sqlcdb.SimAgent{
		agentAt(1, "Alpha", 2),
		agentAt(2, "Beta", 1), // Beta drew position 1
		agentAt(3, "Gamma", 3),
	}
	got := currentDraftAction(pool, agents, 0)
	require.NotNil(t, got)
	assert.Equal(t, 1, got.Round)
	assert.Equal(t, 1, got.Pick)
	assert.Equal(t, 9, got.TotalPicks) // 3 rounds × 3 teams
	assert.Equal(t, 2, got.AgentID)
}

func TestCurrentDraftAction_SnakeReversesOnEvenRounds(t *testing.T) {
	t.Parallel()
	pool := sqlcdb.SimPool{Status: "draft", DraftRounds: 3}
	agents := []sqlcdb.SimAgent{
		agentAt(1, "P1", 1),
		agentAt(2, "P2", 2),
		agentAt(3, "P3", 3),
	}
	// Agents in draft_position order: id=1 (pos=1), id=2 (pos=2), id=3 (pos=3).
	// Round 1: 1, 2, 3 (picks 1-3)
	// Round 2: 3, 2, 1 (picks 4-6) — snake reverse
	// Round 3: 1, 2, 3 (picks 7-9) — back to forward
	cases := []struct {
		completed   int64
		wantRound   int
		wantPick    int
		wantAgentID int
	}{
		{0, 1, 1, 1},
		{1, 1, 2, 2},
		{2, 1, 3, 3},
		{3, 2, 1, 3}, // round 2 first pick goes to whoever was last in round 1
		{4, 2, 2, 2},
		{5, 2, 3, 1},
		{6, 3, 1, 1}, // round 3 first pick: back to original order
		{7, 3, 2, 2},
		{8, 3, 3, 3},
	}
	for _, tc := range cases {
		got := currentDraftAction(pool, agents, tc.completed)
		require.NotNil(t, got, "completed=%d", tc.completed)
		assert.Equal(t, tc.wantRound, got.Round, "completed=%d round", tc.completed)
		assert.Equal(t, tc.wantPick, got.Pick, "completed=%d pick", tc.completed)
		assert.Equal(t, tc.wantAgentID, got.AgentID, "completed=%d agent", tc.completed)
	}
}

func TestCurrentDraftAction_NilWhenDraftComplete(t *testing.T) {
	t.Parallel()
	pool := sqlcdb.SimPool{Status: "draft", DraftRounds: 2}
	agents := []sqlcdb.SimAgent{
		agentAt(1, "A", 1),
		agentAt(2, "B", 2),
	}
	// 4 picks total (2 rounds × 2 teams). After 4 completed, no next pick.
	assert.Nil(t, currentDraftAction(pool, agents, 4),
		"all picks complete must yield nil")
	assert.Nil(t, currentDraftAction(pool, agents, 5),
		"over-count (defensive) must yield nil too")
}

func TestCurrentDraftAction_OrdersByDraftPositionNotInputOrder(t *testing.T) {
	t.Parallel()
	pool := sqlcdb.SimPool{Status: "draft", DraftRounds: 1}
	// Agents arrive in input/id order, but draft_position is shuffled.
	// currentDraftAction must sort by draft_position, not by slice order.
	agents := []sqlcdb.SimAgent{
		agentAt(1, "Inputted-First", 3),
		agentAt(2, "Inputted-Second", 1),
		agentAt(3, "Inputted-Third", 2),
	}
	got := currentDraftAction(pool, agents, 0)
	require.NotNil(t, got)
	assert.Equal(t, 2, got.AgentID,
		"pick 1 of round 1 goes to whoever has draft_position=1 (agent #2), not to whoever the slice listed first")
}

func TestCurrentDraftAction_IgnoresAgentsWithNullPosition(t *testing.T) {
	t.Parallel()
	pool := sqlcdb.SimPool{Status: "draft", DraftRounds: 1}
	// Mixed: some agents shuffled, some not (shouldn't happen in
	// practice but the function should defend against it by simply
	// dropping NULL-positioned agents from the order calculation).
	agents := []sqlcdb.SimAgent{
		agentAt(1, "Shuffled", 1),
		{ID: 2, TeamName: "Unshuffled"}, // NULL draft_position
	}
	got := currentDraftAction(pool, agents, 0)
	require.NotNil(t, got)
	assert.Equal(t, 1, got.AgentID, "shuffled agent #1 is on the clock")
	assert.Equal(t, 1, got.TotalPicks, "totalPicks counts only positioned agents")
}

// ============================================================================
// SimPool forceResolver split — the simPools list query must issue exactly
// one (scalars-only) query, with the expensive nested fields (agents /
// standings / currentDraftAction) fetched lazily per pool only when selected.
//
// These tests drive the resolvers through a recording DBTX fake injected as
// r.Queries, asserting on which sqlc queries actually reach the database.
// ============================================================================

// recordingDBTX is a sqlcdb.DBTX that records the name of every query it is
// asked to run and returns synthetic empty results. ListSimPools optionally
// yields simPoolRows rows so a scalars-only list query can be observed with
// pools actually present (the regression the forceResolver split guards: the
// list path must NOT fan out into per-pool nested queries).
type recordingDBTX struct {
	mu          sync.Mutex
	names       []string
	simPoolRows int
	// queryRowErr, when set, is what every QueryRow's Scan returns —
	// lets a test observe "the resolver reached a :one query and that
	// query failed" without a real database.
	queryRowErr error
}

// queryName extracts the "ListSimPools" style identifier sqlc bakes into each
// generated query constant as a leading "-- name: <Name> :<kind>" comment.
func queryName(sql string) string {
	const marker = "name: "
	i := strings.Index(sql, marker)
	if i < 0 {
		return sql
	}
	rest := sql[i+len(marker):]
	if j := strings.IndexAny(rest, " \t\r\n"); j >= 0 {
		return rest[:j]
	}
	return rest
}

func (d *recordingDBTX) record(sql string) {
	d.mu.Lock()
	d.names = append(d.names, queryName(sql))
	d.mu.Unlock()
}

// recorded returns a copy of the query-name log.
func (d *recordingDBTX) recorded() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.names...)
}

func (d *recordingDBTX) Exec(_ context.Context, sql string, _ ...interface{}) (pgconn.CommandTag, error) {
	d.record(sql)
	return pgconn.CommandTag{}, nil
}

func (d *recordingDBTX) Query(_ context.Context, sql string, _ ...interface{}) (pgx.Rows, error) {
	d.record(sql)
	rows := 0
	if queryName(sql) == "ListSimPools" {
		rows = d.simPoolRows
	}
	return &fakeRows{remaining: rows}, nil
}

func (d *recordingDBTX) QueryRow(_ context.Context, sql string, _ ...interface{}) pgx.Row {
	d.record(sql)
	return fakeRow{err: d.queryRowErr}
}

func (d *recordingDBTX) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	return 0, nil
}

func (d *recordingDBTX) SendBatch(_ context.Context, _ *pgx.Batch) pgx.BatchResults {
	return nil
}

// fakeRows is a minimal pgx.Rows that yields `remaining` synthetic rows.
// Scan leaves every dest untouched, so :many queries decode to zero-value
// structs — enough to exercise query routing without a real database.
type fakeRows struct{ remaining int }

func (r *fakeRows) Next() bool {
	if r.remaining > 0 {
		r.remaining--
		return true
	}
	return false
}
func (r *fakeRows) Scan(_ ...any) error                          { return nil }
func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

// fakeRow is a minimal pgx.Row whose Scan leaves dest untouched and
// returns err (nil by default) — a :one query decodes to a zero-value
// result unless the test configures a failure via recordingDBTX.queryRowErr.
type fakeRow struct{ err error }

func (r fakeRow) Scan(_ ...any) error { return r.err }

// resolverWithDB builds a graph Resolver whose Queries handle runs against the
// given recording DBTX.
func resolverWithDB(db *recordingDBTX) *Resolver {
	return &Resolver{Queries: sqlcdb.New(db)}
}

// TestSimPools_ScalarsOnlyIssuesSingleQuery is the core regression guard: the
// simPools list resolver must issue exactly one ListSimPools query and never
// fan out into per-pool nested reads, even when pools are present.
func TestSimPools_ScalarsOnlyIssuesSingleQuery(t *testing.T) {
	t.Parallel()
	db := &recordingDBTX{simPoolRows: 3}
	r := resolverWithDB(db)

	pools, err := r.Query().SimPools(context.Background())
	require.NoError(t, err)
	assert.Len(t, pools, 3, "three scalar pools decoded from the single list query")

	assert.Equal(t, []string{"ListSimPools"}, db.recorded(),
		"scalars-only simPools must issue exactly one query and no per-pool nested reads")
}

// TestSimPoolAgents_LazyPerPoolQueries pins that the SimPool.agents field
// resolver fetches its data lazily and pool-scoped: one agents read, the
// shared latest-standings probe, and one pool-wide roster read. Crucially it
// must NOT touch ListSimPools (that belongs to the list path).
func TestSimPoolAgents_LazyPerPoolQueries(t *testing.T) {
	t.Parallel()
	db := &recordingDBTX{}
	r := resolverWithDB(db)

	agents, err := r.SimPool().Agents(context.Background(), &model.SimPool{ID: 7})
	require.NoError(t, err)
	assert.Empty(t, agents)

	got := db.recorded()
	assert.ElementsMatch(t,
		[]string{"ListSimAgentsByPool", "GetSimStandingsLatestDate", "ListSimRosterByPool"},
		got,
		"agents field resolver issues only pool-scoped queries")
	assert.NotContains(t, got, "ListSimPools")
}

// TestSimPoolStandings_LazyLatestSnapshot pins that the SimPool.standings
// field resolver reads only the latest-standings probe when the pool has no
// standings yet (invalid latest date short-circuits before ListSimStandingsByDate).
func TestSimPoolStandings_LazyLatestSnapshot(t *testing.T) {
	t.Parallel()
	db := &recordingDBTX{}
	r := resolverWithDB(db)

	standings, err := r.SimPool().Standings(context.Background(), &model.SimPool{ID: 7})
	require.NoError(t, err)
	assert.Empty(t, standings)

	assert.Equal(t, []string{"GetSimStandingsLatestDate"}, db.recorded(),
		"no standings yet → only the latest-date probe, no snapshot fetch")
}

// TestSimPoolCurrentDraftAction_LazyPerPoolQueries pins that the
// SimPool.currentDraftAction field resolver reads the pool row, its agents,
// and the draft-pick count — and returns nil for a non-draft pool.
func TestSimPoolCurrentDraftAction_LazyPerPoolQueries(t *testing.T) {
	t.Parallel()
	db := &recordingDBTX{}
	r := resolverWithDB(db)

	action, err := r.SimPool().CurrentDraftAction(context.Background(), &model.SimPool{ID: 7})
	require.NoError(t, err)
	assert.Nil(t, action, "zero-status pool is not drafting → nil action")

	assert.ElementsMatch(t,
		[]string{"GetSimPool", "ListSimAgentsByPool", "CountSimDraftPicks"},
		db.recorded(),
		"currentDraftAction issues only pool-scoped queries")
}

// TestParseSimWindow pins the startDate/endDate validation rules:
// nil → invalid (use season bound), in-range dates pass through,
// out-of-range or inverted windows error.
func TestParseSimWindow(t *testing.T) {
	t.Parallel()
	seasonStart := time.Date(2024, 10, 1, 0, 0, 0, 0, time.UTC)
	seasonEnd := time.Date(2025, 4, 15, 0, 0, 0, 0, time.UTC)
	str := func(s string) *string { return &s }

	tests := []struct {
		name       string
		start, end *string
		wantStart  string // "" = expect Valid=false
		wantEnd    string
		wantErr    string // "" = expect success
	}{
		{name: "both nil defaults to season bounds", wantStart: "", wantEnd: ""},
		{name: "both empty strings same as nil", start: str(""), end: str("")},
		{name: "explicit window inside season",
			start: str("2025-03-01"), end: str("2025-03-31"),
			wantStart: "2025-03-01", wantEnd: "2025-03-31"},
		{name: "start only", start: str("2025-01-01"), wantStart: "2025-01-01"},
		{name: "end only", end: str("2024-12-31"), wantEnd: "2024-12-31"},
		{name: "start on season start boundary", start: str("2024-10-01"), wantStart: "2024-10-01"},
		{name: "end on season end boundary", end: str("2025-04-15"), wantEnd: "2025-04-15"},
		{name: "garbage start", start: str("March 5"), wantErr: "invalid startDate"},
		{name: "start before season", start: str("2024-09-30"), wantErr: "outside the season's standings range"},
		{name: "end after season", end: str("2025-04-16"), wantErr: "outside the season's standings range"},
		{name: "inverted explicit window",
			start: str("2025-03-31"), end: str("2025-03-01"),
			wantErr: "startDate 2025-03-31 is after endDate 2025-03-01"},
		{name: "start equal to season-default end is a legal one-day window",
			// endDate omitted → effective end is the season end; a
			// start ON that date leaves exactly one sim day. (A start
			// PAST it is unrepresentable — the range check rejects it
			// before the window comparison.)
			start:     str("2025-04-15"),
			wantStart: "2025-04-15",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			start, end, err := parseSimWindow(tc.start, tc.end, seasonStart, seasonEnd)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			if tc.wantStart == "" {
				assert.False(t, start.Valid, "start should be unset")
			} else {
				require.True(t, start.Valid)
				assert.Equal(t, tc.wantStart, start.Time.Format("2006-01-02"))
			}
			if tc.wantEnd == "" {
				assert.False(t, end.Valid, "end should be unset")
			} else {
				require.True(t, end.Valid)
				assert.Equal(t, tc.wantEnd, end.Time.Format("2006-01-02"))
			}
		})
	}
}
