package simulation

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Shared query fakes. Each activity's tests drive it through a fake of
// its own query group (collectQueriesFake, draftQueriesFake, ...),
// declared in the activity's _test file or, for the test files already
// past the size guideline, its _fake_test.go sibling. The methods
// several groups share live here, one fake per concern, and a group
// fake embeds the ones its interface needs; the fakes a group embeds
// never declare the same method, so each embedding compiles. Every fake records the args it was called with and
// returns scripted results — pattern lifted from worker/nhl tests.
// ============================================================================

// stubTransactor is the test Transactor. Calls fn with the supplied
// queries and returns the result, or — if commitErr is set — returns
// commitErr after fn runs (simulating a Postgres commit failure on a
// successful query batch). The stub does NOT roll back fn's side
// effects, since they were applied to a fake anyway; tests assert by
// counting calls + the surfaced error.
type stubTransactor[Q any] struct {
	queries    Q
	inTxCalled int
	fnErr      error   // captured fn return value
	commitErr  error   // injected post-fn error, simulating commit failure
	beforeFn   func()  // optional hook to mutate queries between InTx invocations
	ctxErrs    []error // ctx.Err() at each InTx call: a done context fails Begin in PgxTransactor
	deadlines  []bool  // whether each InTx call's context carries a deadline
}

func (t *stubTransactor[Q]) InTx(ctx context.Context, fn func(Q) error) error {
	t.inTxCalled++
	t.ctxErrs = append(t.ctxErrs, ctx.Err())
	_, hasDeadline := ctx.Deadline()
	t.deadlines = append(t.deadlines, hasDeadline)
	if err := ctx.Err(); err != nil {
		return err // PgxTransactor's Begin fails before fn runs
	}
	if t.beforeFn != nil {
		t.beforeFn()
	}
	t.fnErr = fn(t.queries)
	if t.fnErr != nil {
		return t.fnErr
	}
	return t.commitErr
}

// pgDate constructs a pgtype.Date from a "2006-01-02" string —
// avoids manual struct-field gymnastics in every test case.
func pgDate(t require.TestingT, s string) pgtype.Date {
	tm, err := time.Parse("2006-01-02", s)
	require.NoError(t, err)
	return pgtype.Date{Time: tm, Valid: true}
}

// ----------------------------------------------------------------------------
// turnRowsFake: sim_agent_turns and its children (turnTelemetryQueries,
// abortedTurnQueries' header insert, turnChildQueries).
// ----------------------------------------------------------------------------

var (
	_ turnTelemetryQueries = (*turnRowsFake)(nil)
	_ turnChildQueries     = (*turnRowsFake)(nil)
)

// turnRowsFake captures turn telemetry — tests inspect these to assert
// that the activity wrote the expected telemetry rows; tests that don't
// care can ignore them.
type turnRowsFake struct {
	insertTurnCalls  []sqlcdb.InsertSimAgentTurnParams
	insertTurnErr    error
	insertTurnNextID int32 // 0 = derive id from call count; >0 = return this and increment
	deleteTurnCalls  []sqlcdb.DeleteSimAgentTurnIdempotentParams
	deleteTurnErr    error

	// InsertSimAgentTurnIfAbsent (the aborted-turn write). turnTaken
	// makes it find the coordinate held by another attempt's turn.
	insertTurnIfAbsentCalls []sqlcdb.InsertSimAgentTurnIfAbsentParams
	insertTurnIfAbsentErr   error
	turnTaken               bool

	insertTurnRoundBatches [][]sqlcdb.InsertSimAgentTurnRoundParams
	insertTurnRoundErr     error

	insertToolCallBatches [][]sqlcdb.InsertSimAgentToolCallParams
	insertToolCallErr     error

	insertTurnMessageBatches [][]sqlcdb.InsertSimAgentTurnMessageParams
	insertTurnMessageErr     error
}

func (s *turnRowsFake) DeleteSimAgentTurnIdempotent(_ context.Context, arg sqlcdb.DeleteSimAgentTurnIdempotentParams) error {
	s.deleteTurnCalls = append(s.deleteTurnCalls, arg)
	return s.deleteTurnErr
}

func (s *turnRowsFake) InsertSimAgentTurn(_ context.Context, arg sqlcdb.InsertSimAgentTurnParams) (int32, error) {
	s.insertTurnCalls = append(s.insertTurnCalls, arg)
	if s.insertTurnErr != nil {
		return 0, s.insertTurnErr
	}
	id := s.insertTurnNextID
	if id == 0 {
		id = int32(len(s.insertTurnCalls))
	} else {
		s.insertTurnNextID++
	}
	return id, nil
}

func (s *turnRowsFake) InsertSimAgentTurnIfAbsent(_ context.Context, arg sqlcdb.InsertSimAgentTurnIfAbsentParams) (int32, error) {
	s.insertTurnIfAbsentCalls = append(s.insertTurnIfAbsentCalls, arg)
	if s.insertTurnIfAbsentErr != nil {
		return 0, s.insertTurnIfAbsentErr
	}
	if s.turnTaken {
		return 0, pgx.ErrNoRows
	}
	return int32(len(s.insertTurnIfAbsentCalls)), nil
}

func (s *turnRowsFake) InsertSimAgentTurnRound(_ context.Context, arg []sqlcdb.InsertSimAgentTurnRoundParams) (int64, error) {
	s.insertTurnRoundBatches = append(s.insertTurnRoundBatches, arg)
	return int64(len(arg)), s.insertTurnRoundErr
}

func (s *turnRowsFake) InsertSimAgentToolCall(_ context.Context, arg []sqlcdb.InsertSimAgentToolCallParams) (int64, error) {
	s.insertToolCallBatches = append(s.insertToolCallBatches, arg)
	return int64(len(arg)), s.insertToolCallErr
}

func (s *turnRowsFake) InsertSimAgentTurnMessage(_ context.Context, arg []sqlcdb.InsertSimAgentTurnMessageParams) (int64, error) {
	s.insertTurnMessageBatches = append(s.insertTurnMessageBatches, arg)
	return int64(len(arg)), s.insertTurnMessageErr
}

// ----------------------------------------------------------------------------
// poolFake: the sim_pools row and its spend (costCapQueries,
// recordMessagesQueries, abortedTurnQueries' increment).
// ----------------------------------------------------------------------------

var (
	_ costCapQueries        = (*poolFake)(nil)
	_ recordMessagesQueries = (*poolFake)(nil)
)

type poolFake struct {
	// GetSimPool
	getPoolArgs   []int32
	getPoolReturn sqlcdb.SimPool
	getPoolErr    error

	// PauseSimPoolForCostCap
	pauseCostCapCalls []sqlcdb.PauseSimPoolForCostCapParams
	pauseCostCapRows  int64
	pauseCostCapErr   error

	// IncrementSimPoolLLMCost
	incrementCostCalls  []sqlcdb.IncrementSimPoolLLMCostParams
	incrementCostReturn pgtype.Numeric
	incrementCostErr    error

	// GetSimPoolRecordFullMessages: defaults to true (matches the
	// column default); set override to *false to exercise the gated path.
	getRecordFullMessagesArgs     []int32
	getRecordFullMessagesOverride *bool
	getRecordFullMessagesErr      error
}

func (s *poolFake) GetSimPool(_ context.Context, id int32) (sqlcdb.SimPool, error) {
	s.getPoolArgs = append(s.getPoolArgs, id)
	if s.getPoolErr != nil {
		return sqlcdb.SimPool{}, s.getPoolErr
	}
	return s.getPoolReturn, nil
}

func (s *poolFake) PauseSimPoolForCostCap(_ context.Context, arg sqlcdb.PauseSimPoolForCostCapParams) (int64, error) {
	s.pauseCostCapCalls = append(s.pauseCostCapCalls, arg)
	if s.pauseCostCapErr != nil {
		return 0, s.pauseCostCapErr
	}
	return s.pauseCostCapRows, nil
}

func (s *poolFake) IncrementSimPoolLLMCost(_ context.Context, arg sqlcdb.IncrementSimPoolLLMCostParams) (pgtype.Numeric, error) {
	s.incrementCostCalls = append(s.incrementCostCalls, arg)
	if s.incrementCostErr != nil {
		return pgtype.Numeric{}, s.incrementCostErr
	}
	return s.incrementCostReturn, nil
}

func (s *poolFake) GetSimPoolRecordFullMessages(_ context.Context, id int32) (bool, error) {
	s.getRecordFullMessagesArgs = append(s.getRecordFullMessagesArgs, id)
	if s.getRecordFullMessagesErr != nil {
		return false, s.getRecordFullMessagesErr
	}
	// Default true so tests don't have to set this — matches the
	// migration's column default.
	if s.getRecordFullMessagesOverride != nil {
		return *s.getRecordFullMessagesOverride, nil
	}
	return true, nil
}

// abortedTurnFake is the aborted-turn write's query group on its own.
type abortedTurnFake struct {
	*poolFake
	*turnRowsFake
}

var _ abortedTurnQueries = (*abortedTurnFake)(nil)

func newAbortedTurnFake() *abortedTurnFake {
	return &abortedTurnFake{poolFake: &poolFake{}, turnRowsFake: &turnRowsFake{}}
}

// ----------------------------------------------------------------------------
// rosterFake: sim_rosters and the add/drop transactions that move a
// player on or off one (rosterDropQueries, rosterProbeQueries and the
// roster half of the waiver, draft and daily-action groups).
// ----------------------------------------------------------------------------

var (
	_ rosterDropQueries  = (*rosterFake)(nil)
	_ rosterProbeQueries = (*rosterFake)(nil)
)

type rosterFake struct {
	// InsertSimRoster
	insertRosterCalls []sqlcdb.InsertSimRosterParams
	insertRosterErr   error

	// ExistsSimRosterPlayer — commit-time claimability probe. Keyed by
	// player_id; absent players default to false. existsRosterPlayerErr
	// scripts a DB error on the probe.
	existsRosterPlayerByPlayer map[int64]bool
	existsRosterPlayerArgs     []sqlcdb.ExistsSimRosterPlayerParams
	existsRosterPlayerErr      error

	// DeleteSimRosterRows — rows-affected drop. deleteRosterRowsByPlayer
	// maps player_id → rows deleted (default 1 when unset for a player that
	// IS present; tests set 0 to model a vanished drop player).
	deleteRosterRowsCalls    []sqlcdb.DeleteSimRosterRowsParams
	deleteRosterRowsByPlayer map[int64]int64
	deleteRosterRowsDefault  int64
	deleteRosterRowsErr      error

	// ListSimRosterByAgent (full roster — different from
	// ListSimActiveRosterByAgent, which collectQueriesFake serves).
	listFullRosterByAgent map[int32][]sqlcdb.SimRoster
	listFullRosterErr     error

	// InsertSimTransactionAdd / Drop
	insertAddCalls   []sqlcdb.InsertSimTransactionAddParams
	insertAddReturn  sqlcdb.SimTransaction
	insertAddErr     error
	insertDropCalls  []sqlcdb.InsertSimTransactionDropParams
	insertDropReturn sqlcdb.SimTransaction
	insertDropErr    error
}

// InsertSimRoster records the call and, when the test seeded a full
// roster (listFullRosterByAgent non-nil), appends the row so a later
// ListSimRosterByAgent in the same activity sees the add — mirroring
// read-your-writes inside the real transaction.
func (s *rosterFake) InsertSimRoster(_ context.Context, arg sqlcdb.InsertSimRosterParams) error {
	s.insertRosterCalls = append(s.insertRosterCalls, arg)
	if s.insertRosterErr != nil {
		return s.insertRosterErr
	}
	if s.listFullRosterByAgent != nil {
		s.listFullRosterByAgent[arg.AgentID] = append(s.listFullRosterByAgent[arg.AgentID], sqlcdb.SimRoster{
			PoolID: arg.PoolID, AgentID: arg.AgentID, PlayerID: arg.PlayerID, Slot: arg.Slot,
		})
	}
	return nil
}

func (s *rosterFake) ExistsSimRosterPlayer(_ context.Context, arg sqlcdb.ExistsSimRosterPlayerParams) (bool, error) {
	s.existsRosterPlayerArgs = append(s.existsRosterPlayerArgs, arg)
	if s.existsRosterPlayerErr != nil {
		return false, s.existsRosterPlayerErr
	}
	return s.existsRosterPlayerByPlayer[arg.PlayerID], nil
}

// DeleteSimRosterRows records the call. Precedence for the rows-affected
// result: scripted error → per-player override → the seeded full roster
// (row removed, 1 returned, so a later ListSimRosterByAgent in the same
// activity no longer sees it) → deleteRosterRowsDefault.
func (s *rosterFake) DeleteSimRosterRows(_ context.Context, arg sqlcdb.DeleteSimRosterRowsParams) (int64, error) {
	s.deleteRosterRowsCalls = append(s.deleteRosterRowsCalls, arg)
	if s.deleteRosterRowsErr != nil {
		return 0, s.deleteRosterRowsErr
	}
	if n, ok := s.deleteRosterRowsByPlayer[arg.PlayerID]; ok {
		return n, nil
	}
	if removed := s.removeFromFullRoster(arg.AgentID, arg.PlayerID); removed > 0 {
		return removed, nil
	}
	return s.deleteRosterRowsDefault, nil
}

// removeFromFullRoster drops (agent, player) from the seeded full roster
// and reports how many rows went away (0 when unseeded or absent).
func (s *rosterFake) removeFromFullRoster(agentID int32, playerID int64) int64 {
	rows, ok := s.listFullRosterByAgent[agentID]
	if !ok {
		return 0
	}
	remaining := slices.DeleteFunc(slices.Clone(rows), func(r sqlcdb.SimRoster) bool {
		return r.PlayerID == playerID
	})
	removed := int64(len(rows) - len(remaining))
	if removed > 0 {
		s.listFullRosterByAgent[agentID] = remaining
	}
	return removed
}

func (s *rosterFake) ListSimRosterByAgent(_ context.Context, arg sqlcdb.ListSimRosterByAgentParams) ([]sqlcdb.SimRoster, error) {
	if s.listFullRosterErr != nil {
		return nil, s.listFullRosterErr
	}
	return s.listFullRosterByAgent[arg.AgentID], nil
}

func (s *rosterFake) InsertSimTransactionAdd(_ context.Context, arg sqlcdb.InsertSimTransactionAddParams) (sqlcdb.SimTransaction, error) {
	s.insertAddCalls = append(s.insertAddCalls, arg)
	if s.insertAddErr != nil {
		return sqlcdb.SimTransaction{}, s.insertAddErr
	}
	return s.insertAddReturn, nil
}

func (s *rosterFake) InsertSimTransactionDrop(_ context.Context, arg sqlcdb.InsertSimTransactionDropParams) (sqlcdb.SimTransaction, error) {
	s.insertDropCalls = append(s.insertDropCalls, arg)
	if s.insertDropErr != nil {
		return sqlcdb.SimTransaction{}, s.insertDropErr
	}
	return s.insertDropReturn, nil
}

// ----------------------------------------------------------------------------
// agentFake: sim_agents rows (the pool's agent list, one agent, its
// notes).
// ----------------------------------------------------------------------------

type agentFake struct {
	// ListSimAgentsByPool
	listAgentsByPoolArgs   []int32
	listAgentsByPoolReturn []sqlcdb.SimAgent
	listAgentsByPoolErr    error

	// GetSimAgent: a bare {ID: id} row unless getSimAgentByID has one.
	getSimAgentByID map[int32]sqlcdb.SimAgent
	getSimAgentErr  error

	// UpdateSimAgentNotes
	updateAgentNotesCalls []sqlcdb.UpdateSimAgentNotesParams
	updateAgentNotesErr   error
}

func (s *agentFake) ListSimAgentsByPool(_ context.Context, poolID int32) ([]sqlcdb.SimAgent, error) {
	s.listAgentsByPoolArgs = append(s.listAgentsByPoolArgs, poolID)
	if s.listAgentsByPoolErr != nil {
		return nil, s.listAgentsByPoolErr
	}
	return s.listAgentsByPoolReturn, nil
}

func (s *agentFake) GetSimAgent(_ context.Context, id int32) (sqlcdb.SimAgent, error) {
	if s.getSimAgentErr != nil {
		return sqlcdb.SimAgent{}, s.getSimAgentErr
	}
	if a, ok := s.getSimAgentByID[id]; ok {
		return a, nil
	}
	return sqlcdb.SimAgent{ID: id}, nil
}

func (s *agentFake) UpdateSimAgentNotes(_ context.Context, arg sqlcdb.UpdateSimAgentNotesParams) error {
	s.updateAgentNotesCalls = append(s.updateAgentNotesCalls, arg)
	return s.updateAgentNotesErr
}

// ----------------------------------------------------------------------------
// playerFake: batched players lookups (playerLookupQueries).
// ----------------------------------------------------------------------------

var _ playerLookupQueries = (*playerFake)(nil)

type playerFake struct {
	getPlayerByID map[int64]sqlcdb.Player
	getPlayerErr  error

	// getPlayersByIDsCalls records each batch — tests assert the N+1
	// batching collapses to one call per activity invocation.
	getPlayersByIDsCalls [][]int64
}

// GetPlayersByIDs returns, like the real query, a row only for the ids
// the test put in getPlayerByID, and forces p.ID == id on each one —
// real GetPlayersByIDs rows always carry their own id column, but
// fixtures keyed by map key often leave ID zero-valued, and
// loadPlayersByIDs re-keys the batch result by p.ID.
func (s *playerFake) GetPlayersByIDs(_ context.Context, ids []int64) ([]sqlcdb.Player, error) {
	s.getPlayersByIDsCalls = append(s.getPlayersByIDsCalls, ids)
	if s.getPlayerErr != nil {
		return nil, s.getPlayerErr
	}
	out := make([]sqlcdb.Player, 0, len(ids))
	for _, id := range ids {
		p, ok := s.getPlayerByID[id]
		if !ok {
			continue
		}
		p.ID = id
		out = append(out, p)
	}
	return out, nil
}
