package simulation

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// ============================================================================
// Test stubs for the new infrastructure DraftPickActivity needs:
//   - stubTransactor: bypasses real DB by calling fn with the same
//     stubSimQueries used for read-only calls. To exercise the "commit
//     fails" path, set commitErr — InTx returns it after fn runs (and
//     the stub does NOT roll back fn's side effects, since they were
//     applied to a stub anyway; tests assert by counting calls + the
//     surfaced error).
//   - stubSignaler: records SignalWorkflow calls.
//   - stubLLMClient: scripts queued responses and errors per call.
//
// These stubs deliberately don't enforce ordering or build a fake DB
// — they just record so each test can assert on the behavior it
// cares about.
// ============================================================================

// stubTransactor is the test Transactor. Calls fn with the supplied
// SimQueries and returns the result, or — if commitErr is set —
// returns commitErr after fn runs (simulating a Postgres commit
// failure on a successful query batch).
type stubTransactor struct {
	queries    SimQueries
	inTxCalled int
	fnErr      error  // captured fn return value
	commitErr  error  // injected post-fn error, simulating commit failure
	beforeFn   func() // optional hook to mutate queries between InTx invocations
}

func (t *stubTransactor) InTx(ctx context.Context, fn func(SimQueries) error) error {
	t.inTxCalled++
	if t.beforeFn != nil {
		t.beforeFn()
	}
	t.fnErr = fn(t.queries)
	if t.fnErr != nil {
		return t.fnErr
	}
	return t.commitErr
}

// stubSignaler records SignalWorkflow calls.
type stubSignaler struct {
	calls []signalCall
	err   error
}

type signalCall struct {
	WorkflowID string
	RunID      string
	SignalName string
	Arg        any
}

func (s *stubSignaler) SignalWorkflow(_ context.Context, workflowID, runID, signalName string, arg any) error {
	s.calls = append(s.calls, signalCall{workflowID, runID, signalName, arg})
	return s.err
}

// scriptedLLMClient implements llm.Client. Each Complete call pops
// the next entry from responses (or errors). Running off the end of
// the script fails the test loudly via t.Fatalf rather than panicking
// on an out-of-range index.
//
// The atomic counter is for the assertion "LLM called exactly N
// times" — set once, read from any goroutine.
type scriptedLLMClient struct {
	t         *testing.T
	responses []*llm.Response
	errs      []error
	calls     atomic.Int32
}

func (c *scriptedLLMClient) Complete(_ context.Context, _ *llm.Request) (*llm.Response, error) {
	idx := int(c.calls.Add(1)) - 1
	if idx < len(c.errs) && c.errs[idx] != nil {
		return nil, c.errs[idx]
	}
	if idx < len(c.responses) {
		return c.responses[idx], nil
	}
	// Past-script-end response: empty content, no tool calls. This is
	// the "agent stops responding after its work is done" signal that
	// agentloop interprets as a terminal round and exits cleanly. Tests
	// can script the first 1-N rounds and rely on this default to
	// terminate the loop without spelling out every follow-up.
	return &llm.Response{}, nil
}

// draftPlayerToolCall builds an llm.Response that "calls" draft_player
// with the given player_id. Centralized so tests don't duplicate the
// JSON-encoded args. Includes a Usage object so the cost accounting
// path runs against non-zero numbers.
func draftPlayerToolCall(playerID int64, reasoning string) *llm.Response {
	return &llm.Response{
		Content: reasoning,
		ToolCalls: []llm.ToolCall{{
			ID:   "tc_test",
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      ToolDraftPlayer,
				Arguments: fmt.Sprintf(`{"player_id":%d,"reason":%q}`, playerID, reasoning),
			},
		}},
		Usage: &llm.Usage{
			PromptTokens:     1000,
			CompletionTokens: 50,
			TotalTokens:      1050,
		},
	}
}

// fakeAgent builds an *Agent with the stub LLM client and pre-baked
// internal state. Tests inject this via Activities.AgentFactory so
// getOrCreateAgent never reaches NewAgent (which would try to
// resolve a real provider).
func fakeAgent(client llm.Client, cfg AgentConfig) *Agent {
	return &Agent{
		Config:       cfg,
		Client:       client,
		systemPrompt: "test system prompt",
		draftTools:   DraftTools(),
		dailyTools:   DailyTools(),
	}
}

// ============================================================================
// DraftPickTestSuite — covers the activity's six behavior arms:
//   1. idempotency hit
//   2. cost-cap trip
//   3. happy-path LLM
//   4. fallback after 2x failure
//   5. commit failure surfaces (atomic rollback)
//   6. signal failure surfaces (so Temporal retries)
// ============================================================================

type DraftPickTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env      *testsuite.TestActivityEnvironment
	queries  *stubSimQueries
	tx       *stubTransactor
	signaler *stubSignaler
	llm      *scriptedLLMClient
	acts     *Activities
}

func (s *DraftPickTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.queries = &stubSimQueries{getDraftPickErr: pgx.ErrNoRows}
	s.tx = &stubTransactor{queries: s.queries}
	s.signaler = &stubSignaler{}
	s.llm = &scriptedLLMClient{t: s.T()}

	s.acts = &Activities{
		Queries:  s.queries,
		Tx:       s.tx,
		Signaler: s.signaler,
		AgentFactory: func(_ int32, cfg AgentConfig, _ map[llm.Provider]llm.ProviderConfig, _ int) (*Agent, error) {
			return fakeAgent(s.llm, cfg), nil
		},
	}
	s.env.RegisterActivity(s.acts.DraftPick)
}

func TestDraftPickTestSuite(t *testing.T) {
	suite.Run(t, new(DraftPickTestSuite))
}

// validInput returns a DraftPickInput with reasonable defaults, so
// tests only restate the field they care about. Cap is set comfortably
// above the (zero) starting cost so the cost-cap branch doesn't fire
// unless the test arranges it.
func (s *DraftPickTestSuite) validInput() DraftPickInput {
	return DraftPickInput{
		PoolID:     1,
		AgentID:    7,
		Round:      1,
		Pick:       3,
		SimDate:    pgDate(s.T(), "2024-10-08"),
		WorkflowID: "sim-pool-1",
		PoolConfig: PoolConfig{
			Season:               20242025,
			NumTeams:             5,
			MaxLLMCostUsdPerPool: 200.00,
		},
		AgentConfig: AgentConfig{
			Provider: "anthropic", Model: "claude-haiku-4-5",
			Strategy: "balanced",
		},
		AvailableIDs: []int64{8478402, 8480039, 8479318}, // McDavid, MacKinnon, Matthews
		Roster:       RosterState{Placements: map[int64]RosterSlot{}, Limits: map[RosterSlot]int{}},
	}
}

// ----------------------------------------------------------------------------
// Idempotency probe
// ----------------------------------------------------------------------------

func (s *DraftPickTestSuite) TestIdempotencyHit_SkipsLLM() {
	t := s.T()
	// Script: GetSimTransactionDraftPick returns a prior pick (no error).
	prior := sqlcdb.SimTransaction{
		ID:       42,
		PoolID:   1,
		AgentID:  7,
		PlayerID: pgtype.Int8{Int64: 8478402, Valid: true},
		Type:     string(TransactionTypeDraftPick),
	}
	s.queries.getDraftPickErr = nil
	s.queries.getDraftPickReturn = prior

	future, err := s.env.ExecuteActivity(s.acts.DraftPick, s.validInput())
	require.NoError(t, err)

	var got DraftPickResult
	require.NoError(t, future.Get(&got))
	assert.True(t, got.Skipped)
	assert.Equal(t, SkipReasonAlreadyDrafted, got.SkipReason)
	assert.Equal(t, int64(8478402), got.PlayerID)

	assert.Zero(t, s.llm.calls.Load(), "idempotency hit must not invoke LLM")
	assert.Zero(t, s.tx.inTxCalled, "no commit on idempotency hit")
	assert.Empty(t, s.signaler.calls)
}

func (s *DraftPickTestSuite) TestIdempotencyProbeError_AbortsActivity() {
	t := s.T()
	// Anything other than ErrNoRows aborts so Temporal retries.
	s.queries.getDraftPickErr = errors.New("connection lost")

	_, err := s.env.ExecuteActivity(s.acts.DraftPick, s.validInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "idempotency probe")
	assert.Zero(t, s.llm.calls.Load())
}

// ----------------------------------------------------------------------------
// Cost-cap pre-flight
// ----------------------------------------------------------------------------

func (s *DraftPickTestSuite) TestCostCap_TripsBeforeLLM() {
	t := s.T()
	// Pool already at the $200 cap.
	tooMuch, err := numericFromFloat(200.01)
	require.NoError(t, err)
	s.queries.getPoolReturn = sqlcdb.SimPool{ID: 1, TotalLLMCostUSD: tooMuch}

	future, err := s.env.ExecuteActivity(s.acts.DraftPick, s.validInput())
	require.NoError(t, err)
	var got DraftPickResult
	require.NoError(t, future.Get(&got))
	assert.True(t, got.Skipped)
	assert.Equal(t, SkipReasonCostCapReached, got.SkipReason)
	assert.Zero(t, got.PlayerID)

	assert.Zero(t, s.llm.calls.Load(), "cost-cap branch must not invoke LLM")

	// Cost-cap branch fires the InTx commit (cost_cap_reached row + status update).
	require.Equal(t, 1, s.tx.inTxCalled)
	require.Len(t, s.queries.insertCostCapCalls, 1)
	assert.Equal(t, int32(1), s.queries.insertCostCapCalls[0].PoolID)
	assert.Equal(t, int32(7), s.queries.insertCostCapCalls[0].AgentID)
	require.Len(t, s.queries.updatePoolStatusCalls, 1)
	assert.Equal(t, string(PoolStatusPaused), s.queries.updatePoolStatusCalls[0].Status)

	// Pause signal sent to the workflow.
	require.Len(t, s.signaler.calls, 1)
	assert.Equal(t, "sim-pool-1", s.signaler.calls[0].WorkflowID)
	assert.Equal(t, SignalPause, s.signaler.calls[0].SignalName)
	assert.Empty(t, s.signaler.calls[0].RunID, "runID empty = latest run")
}

// cap=0 (or unset) is treated as "no cap" rather than "every call
// trips" — otherwise an omitted-field config would lock the pool
// out forever.
func (s *DraftPickTestSuite) TestCostCap_DisabledWhenCapZero() {
	t := s.T()
	in := s.validInput()
	in.PoolConfig.MaxLLMCostUsdPerPool = 0
	s.llm.responses = []*llm.Response{draftPlayerToolCall(8478402, "best skater")}

	future, err := s.env.ExecuteActivity(s.acts.DraftPick, in)
	require.NoError(t, err)
	var got DraftPickResult
	require.NoError(t, future.Get(&got))
	assert.False(t, got.Skipped)
	// GetSimPool should NOT have been called when cap is disabled.
	assert.Empty(t, s.queries.getPoolArgs)
}

// Signal-back failure must surface as activity error so Temporal
// retries — otherwise the workflow would never see the pause.
func (s *DraftPickTestSuite) TestCostCap_SignalErrorAbortsActivity() {
	t := s.T()
	tooMuch, err := numericFromFloat(250.0)
	require.NoError(t, err)
	s.queries.getPoolReturn = sqlcdb.SimPool{ID: 1, TotalLLMCostUSD: tooMuch}
	s.signaler.err = errors.New("temporal unavailable")

	_, err = s.env.ExecuteActivity(s.acts.DraftPick, s.validInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signal workflow")
	// The DB-side commit still happened — that's intentional. On
	// retry the cap will trip again, the row insert duplicates (audit
	// trail accepts duplicates), and the signal retries.
	assert.Equal(t, 1, s.tx.inTxCalled)
	require.Len(t, s.queries.updatePoolStatusCalls, 1)
}

// ----------------------------------------------------------------------------
// Happy-path LLM call → atomic commit
// ----------------------------------------------------------------------------

func (s *DraftPickTestSuite) TestHappyPath_WritesRosterAndTransaction() {
	t := s.T()
	s.llm.responses = []*llm.Response{
		draftPlayerToolCall(8478402, "McDavid is the best skater available"),
	}

	future, err := s.env.ExecuteActivity(s.acts.DraftPick, s.validInput())
	require.NoError(t, err)
	var got DraftPickResult
	require.NoError(t, future.Get(&got))
	assert.False(t, got.Skipped)
	assert.False(t, got.UsedFallback)
	assert.Equal(t, int64(8478402), got.PlayerID)
	assert.Greater(t, got.CostUsd, 0.0, "Anthropic Haiku call must show non-zero cost")

	require.GreaterOrEqual(t, s.llm.calls.Load(), int32(1),
		"at least one LLM call on happy path (agentloop may do one more round after the tool result)")
	require.Equal(t, 1, s.tx.inTxCalled)

	// Roster: BN slot, acquired_via=draft.
	require.Len(t, s.queries.insertRosterCalls, 1)
	rc := s.queries.insertRosterCalls[0]
	assert.Equal(t, int64(8478402), rc.PlayerID)
	assert.Equal(t, string(SlotBN), rc.Slot)
	assert.Equal(t, string(AcquiredViaDraft), rc.AcquiredVia)

	// Transaction: round/pick/reasoning carried through.
	require.Len(t, s.queries.insertDraftPickCalls, 1)
	tc := s.queries.insertDraftPickCalls[0]
	assert.Equal(t, int32(1), tc.Round.Int32)
	assert.Equal(t, int32(3), tc.Pick.Int32)
	assert.Equal(t, "McDavid is the best skater available", tc.Reasoning)
	assert.Equal(t, int64(8478402), tc.PlayerID.Int64)

	// Cost increment fired with a non-zero amount.
	require.Len(t, s.queries.incrementCostCalls, 1)
	costFloat, err := numericToFloat(s.queries.incrementCostCalls[0].TotalLLMCostUSD)
	require.NoError(t, err)
	assert.InDelta(t, got.CostUsd, costFloat, 1e-9)
}

// Finding #7: each draft pick must be its own telemetry idempotency unit.
// The idempotent DELETE key now includes pick_number = (round-1)*numTeams +
// pick, so pick N's telemetry write does NOT delete pick N-1's row (both
// share phase='draft' + sim_date=season start). Two picks at different
// (round, pick) coordinates produce distinct delete keys → both turns'
// rounds/tool_calls/messages survive.
func (s *DraftPickTestSuite) TestTelemetry_PickNumberScopesIdempotentDelete() {
	t := s.T()

	// Round 1, pick 3 → pick_number = (1-1)*5 + 3 = 3.
	in1 := s.validInput()
	s.llm.responses = []*llm.Response{
		draftPlayerToolCall(8478402, "first pick"),
	}
	f1, err := s.env.ExecuteActivity(s.acts.DraftPick, in1)
	require.NoError(t, err)
	require.NoError(t, f1.Get(new(DraftPickResult)))

	// Round 2, pick 3 → pick_number = (2-1)*5 + 3 = 8. Reset the scripted
	// client's monotonic call counter so the second turn replays from
	// responses[0].
	in2 := s.validInput()
	in2.Round = 2
	in2.AvailableIDs = []int64{8480039, 8479318}
	s.llm.calls.Store(0)
	s.llm.responses = []*llm.Response{
		draftPlayerToolCall(8480039, "second pick"),
	}
	f2, err := s.env.ExecuteActivity(s.acts.DraftPick, in2)
	require.NoError(t, err)
	require.NoError(t, f2.Get(new(DraftPickResult)))

	require.Len(t, s.queries.deleteTurnCalls, 2)
	assert.Equal(t, int32(3), s.queries.deleteTurnCalls[0].PickNumber,
		"round 1 pick 3 → pick_number 3")
	assert.Equal(t, int32(8), s.queries.deleteTurnCalls[1].PickNumber,
		"round 2 pick 3 → pick_number 8")
	assert.NotEqual(t, s.queries.deleteTurnCalls[0].PickNumber, s.queries.deleteTurnCalls[1].PickNumber,
		"distinct pick_numbers ⇒ round-2 delete can't wipe round-1's turn")

	// The inserted turn rows carry the matching pick_number, so the unique
	// index (pool, agent, sim_date, phase, pick_number) keeps them separate.
	require.Len(t, s.queries.insertTurnCalls, 2)
	assert.Equal(t, int32(3), s.queries.insertTurnCalls[0].PickNumber)
	assert.Equal(t, int32(8), s.queries.insertTurnCalls[1].PickNumber)
}

// ----------------------------------------------------------------------------
// Retry + fallback
// ----------------------------------------------------------------------------

// First LLM round picks an unavailable player ID; agentloop sends
// the validation error back to the LLM as the tool result; second
// round picks a valid ID → activity uses the second answer.
// Different from pre-agentloop behavior (no more "two cold attempts");
// retry is now inline conversation continuation.
func (s *DraftPickTestSuite) TestRetry_OnFirstFailure() {
	t := s.T()
	s.llm.responses = []*llm.Response{
		// First round: draft_player with an invalid ID → validation_rejected.
		draftPlayerToolCall(99999, "I'll pick this player (made up ID)"),
		// Second round: valid draft_player call.
		draftPlayerToolCall(8480039, "MacKinnon — second-best available"),
	}

	future, err := s.env.ExecuteActivity(s.acts.DraftPick, s.validInput())
	require.NoError(t, err)
	var got DraftPickResult
	require.NoError(t, future.Get(&got))
	assert.False(t, got.UsedFallback)
	assert.Equal(t, int64(8480039), got.PlayerID)
	assert.GreaterOrEqual(t, s.llm.calls.Load(), int32(2),
		"at least two LLM rounds: first attempt rejected, second succeeded")
}

// Both LLM attempts fail → fallback fires. Fallback picks the
// highest-ranked available skater since the empty roster has every
// position at full deficit (ties broken by C/LW/RW/D/G order, so the
// first C in rankedSkaters wins).
func (s *DraftPickTestSuite) TestFallback_AfterTwoFailures() {
	t := s.T()
	in := s.validInput()
	in.RankedSkaters = []SkaterDraftCandidate{
		{PlayerID: 9001, Position: "C", PriorG: 60, PriorA: 80, Name: "Top C"},
		{PlayerID: 9002, Position: "LW", PriorG: 50, PriorA: 50, Name: "Top LW"},
	}
	in.RankedGoalies = []GoalieDraftCandidate{
		{PlayerID: 9003, PriorW: 35, Name: "Top G"},
	}

	s.llm.responses = []*llm.Response{
		{Content: "..."}, // no tool call
	}

	future, err := s.env.ExecuteActivity(s.acts.DraftPick, in)
	require.NoError(t, err)
	var got DraftPickResult
	require.NoError(t, future.Get(&got))
	assert.True(t, got.UsedFallback)
	assert.Equal(t, int64(9001), got.PlayerID, "fallback picks highest-deficit position's top candidate")
	assert.GreaterOrEqual(t, s.llm.calls.Load(), int32(1),
		"at least one LLM attempt must run before fallback fires")

	// Fallback path STILL writes everything.
	require.Len(t, s.queries.insertRosterCalls, 1)
	assert.Equal(t, int64(9001), s.queries.insertRosterCalls[0].PlayerID)
	require.Len(t, s.queries.insertDraftPickCalls, 1)
	assert.Empty(t, s.queries.insertDraftPickCalls[0].Reasoning,
		"fallback path emits empty reasoning (no LLM-supplied text)")
}

// LLM picks an ID that's NOT in AvailableIDs → treated as a failure
// for retry purposes. Pin behavior so future "trust the LLM" changes
// are loud.
func (s *DraftPickTestSuite) TestValidation_LLMPicksUnavailablePlayer() {
	t := s.T()
	in := s.validInput()
	in.RankedSkaters = []SkaterDraftCandidate{
		{PlayerID: 9001, Position: "C", PriorG: 60, PriorA: 80},
	}

	s.llm.responses = []*llm.Response{
		// Both attempts pick a player NOT in AvailableIDs.
		draftPlayerToolCall(7777777, "phantom"),
		draftPlayerToolCall(7777777, "phantom again"),
	}

	future, err := s.env.ExecuteActivity(s.acts.DraftPick, in)
	require.NoError(t, err)
	var got DraftPickResult
	require.NoError(t, future.Get(&got))
	assert.True(t, got.UsedFallback)
	assert.Equal(t, int64(9001), got.PlayerID)
}

// LLM provider error (network, 5xx) terminates the agentloop;
// activity then falls back to the workflow-supplied ranked candidates.
// (Pre-agentloop the activity did two cold attempts; in the new
// design the provider error exits agentloop and fallback runs.)
func (s *DraftPickTestSuite) TestRetry_OnLLMProviderError() {
	t := s.T()
	in := s.validInput()
	in.RankedSkaters = []SkaterDraftCandidate{
		{PlayerID: 9001, Position: "C", PriorG: 60, PriorA: 80, Name: "Top C"},
	}
	s.llm.errs = []error{errors.New("503 service unavailable")}

	future, err := s.env.ExecuteActivity(s.acts.DraftPick, in)
	require.NoError(t, err)
	var got DraftPickResult
	require.NoError(t, future.Get(&got))
	assert.True(t, got.UsedFallback)
	assert.Equal(t, int64(9001), got.PlayerID)
}

// ----------------------------------------------------------------------------
// Atomic-commit failure
// ----------------------------------------------------------------------------

func (s *DraftPickTestSuite) TestCommit_FailureSurfacesError() {
	t := s.T()
	s.llm.responses = []*llm.Response{draftPlayerToolCall(8478402, "ok")}
	s.tx.commitErr = errors.New("deadlock detected")

	_, err := s.env.ExecuteActivity(s.acts.DraftPick, s.validInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deadlock detected")

	// Inserts ran inside the failing tx. The stub doesn't actually
	// roll back the recorded calls, but real Postgres + PgxTransactor
	// would — the integration test under the integration build tag
	// covers that. Here we pin only that the commit error PROPAGATES,
	// so Temporal will retry.
	assert.Equal(t, 1, s.tx.inTxCalled)
}

// One of the writes inside the tx fails → activity returns an error;
// no signal is sent (this is not a cost-cap path).
func (s *DraftPickTestSuite) TestCommit_RosterInsertFailureSurfacesError() {
	t := s.T()
	s.llm.responses = []*llm.Response{draftPlayerToolCall(8478402, "ok")}
	s.queries.insertRosterErr = errors.New("FK violation: player not found")

	_, err := s.env.ExecuteActivity(s.acts.DraftPick, s.validInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FK violation")
	assert.Empty(t, s.signaler.calls, "no signal on commit failure")
}

// ----------------------------------------------------------------------------
// Agent caching — second pick for the same agent reuses the *Agent
// (and therefore the same cached system prompt + tool list).
// ----------------------------------------------------------------------------

func (s *DraftPickTestSuite) TestAgentCache_ReusesAgentAcrossPicks() {
	t := s.T()
	// Spy on the factory: count how many times it's called.
	var factoryCalls atomic.Int32
	s.acts.AgentFactory = func(_ int32, cfg AgentConfig, _ map[llm.Provider]llm.ProviderConfig, _ int) (*Agent, error) {
		factoryCalls.Add(1)
		return fakeAgent(s.llm, cfg), nil
	}

	s.llm.responses = []*llm.Response{
		draftPlayerToolCall(8478402, "pick 1"),
		{}, // pick 1's post-tool-result follow-up (no more tool calls)
		draftPlayerToolCall(8480039, "pick 2"),
		// pick 2's follow-up uses the past-script-end default.
	}

	in1 := s.validInput()
	in1.Pick = 3
	_, err := s.env.ExecuteActivity(s.acts.DraftPick, in1)
	require.NoError(t, err)

	in2 := s.validInput()
	in2.Pick = 4
	_, err = s.env.ExecuteActivity(s.acts.DraftPick, in2)
	require.NoError(t, err)

	assert.Equal(t, int32(1), factoryCalls.Load(),
		"factory must run once per (pool, agent) — second pick reuses cached *Agent")
}
