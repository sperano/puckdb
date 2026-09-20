package simulation

import (
	"errors"
	"fmt"
	"testing"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// ----------------------------------------------------------------------------
// PickTeamNameActivity — cost accounting + retry safety around the one
// paid LLM call of Phase 0.
//
// Every test here is about a dollar: the turn's spend must reach
// sim_pools.total_llm_cost_usd exactly once, and nothing that can fail
// may cause a SECOND paid call for the same name.
// ----------------------------------------------------------------------------

type PickTeamNameTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env     *testsuite.TestActivityEnvironment
	queries *stubSimQueries
	tx      *stubTransactor
	llm     *scriptedLLMClient
	acts    *Activities
}

func (s *PickTeamNameTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.queries = &stubSimQueries{}
	s.tx = &stubTransactor{queries: s.queries}
	s.llm = &scriptedLLMClient{t: s.T()}
	s.acts = &Activities{
		Queries: s.queries,
		Tx:      s.tx,
		AgentFactory: func(_ int32, cfg AgentConfig, _ map[llm.Provider]llm.ProviderConfig, _ int) (*Agent, error) {
			return fakeAgent(s.llm, cfg), nil
		},
	}
	s.env.RegisterActivity(s.acts.PickTeamName)
}

func TestPickTeamNameTestSuite(t *testing.T) {
	suite.Run(t, new(PickTeamNameTestSuite))
}

// teamNameInput is the shared happy-path payload. The model is a real
// priced entry so the cost accounting path runs against non-zero
// numbers rather than a silent $0.
func (s *PickTeamNameTestSuite) teamNameInput() PickTeamNameInput {
	return PickTeamNameInput{
		PoolID:  1,
		AgentID: 7,
		AgentConfig: AgentConfig{
			Provider: "anthropic",
			Model:    "claude-haiku-4-5",
			Strategy: "balanced",
		},
	}
}

// setTeamNameToolCall builds a scripted set_team_name response. Usage
// is deliberately large enough that the cost survives the 6-decimal
// numeric encoding in numericFromFloat.
func setTeamNameToolCall(name, summary string) *llm.Response {
	return &llm.Response{
		Content: "Naming my team " + name,
		ToolCalls: []llm.ToolCall{{
			ID:   "tc_teamname",
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      ToolSetTeamName,
				Arguments: fmt.Sprintf(`{"name":%q,"summary":%q}`, name, summary),
			},
		}},
		Usage: &llm.Usage{PromptTokens: 1000, CompletionTokens: 50, TotalTokens: 1050},
	}
}

// A successful turn must increment the DURABLE pool cost exactly once,
// in the same tx as the team_name write. Without the increment the
// team-name spend is invisible to the cost cap and is lost outright
// when the workflow reloads TotalLLMCostUsd after a ContinueAsNew.
func (s *PickTeamNameTestSuite) TestSuccess_IncrementsPoolCostOnceInSameTx() {
	t := s.T()
	s.llm.responses = []*llm.Response{setTeamNameToolCall("Rink Rats", "balanced")}

	future, err := s.env.ExecuteActivity(s.acts.PickTeamName, s.teamNameInput())
	require.NoError(t, err)
	var got PickTeamNameResult
	require.NoError(t, future.Get(&got))
	assert.Equal(t, "Rink Rats", got.Name)
	assert.Greater(t, got.CostUsd, 0.0, "priced model + non-zero usage must cost something")

	require.Len(t, s.queries.incrementCostCalls, 1, "durable pool cost incremented exactly once")
	assert.Equal(t, int32(1), s.queries.incrementCostCalls[0].ID)
	costFloat, err := NumericToFloat(s.queries.incrementCostCalls[0].TotalLLMCostUSD)
	require.NoError(t, err)
	assert.InDelta(t, got.CostUsd, costFloat, 1e-9, "the increment matches the reported cost")

	require.Len(t, s.queries.setTeamNameCalls, 1)
	assert.Equal(t, int32(7), s.queries.setTeamNameCalls[0].ID)
	assert.Equal(t, "Rink Rats", s.queries.setTeamNameCalls[0].TeamName)
	assert.Equal(t, "balanced", s.queries.setTeamNameCalls[0].StrategySummary)

	// This only pins that exactly one InTx ran — the stub records calls
	// against a shared queries value regardless of tx boundaries, so it
	// can't itself prove the three writes below landed atomically.
	assert.Equal(t, 1, s.tx.inTxCalled, "exactly one InTx call for the turn")
	require.Len(t, s.queries.insertTurnCalls, 1)
	assert.Equal(t, string(TurnStatusOK), s.queries.insertTurnCalls[0].Status)
}

// Committed-result probe: the turn committed but the activity response
// was lost (worker crash between commit and completion report). The
// retry must return the saved name WITHOUT paying for a second call,
// and must not re-count the cost that the committed attempt already
// added to sim_pools.
func (s *PickTeamNameTestSuite) TestCommittedNameProbe_SkipsLLMAndCost() {
	t := s.T()
	s.queries.getSimAgentByID = map[int32]sqlcdb.SimAgent{
		7: {ID: 7, TeamName: "Rink Rats", StrategySummary: "balanced"},
	}

	future, err := s.env.ExecuteActivity(s.acts.PickTeamName, s.teamNameInput())
	require.NoError(t, err)
	var got PickTeamNameResult
	require.NoError(t, future.Get(&got))
	assert.Equal(t, "Rink Rats", got.Name, "saved name returned as-is")
	assert.Zero(t, got.CostUsd, "the committed attempt's cost is already durably counted")

	assert.Zero(t, s.llm.calls.Load(), "committed-result hit must not invoke the LLM")
	assert.Empty(t, s.queries.incrementCostCalls, "no second increment for an already-counted turn")
	assert.Zero(t, s.tx.inTxCalled, "nothing to commit on the probe path")
	assert.Empty(t, s.queries.getRecordFullMessagesArgs, "probe short-circuits before any other read")
}

// The record_full_messages read is a PRE-flight: when it fails the
// activity must error with zero LLM invocations. Reading it after the
// call (the old shape) made a transient DB error bill the pool twice —
// once for the failed attempt, once for the Temporal retry.
func (s *PickTeamNameTestSuite) TestRecordFullMessagesReadFails_MakesNoLLMCall() {
	t := s.T()
	s.queries.getRecordFullMessagesErr = errors.New("connection reset by peer")
	s.llm.responses = []*llm.Response{setTeamNameToolCall("Rink Rats", "balanced")}

	_, err := s.env.ExecuteActivity(s.acts.PickTeamName, s.teamNameInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "record_full_messages")

	assert.Zero(t, s.llm.calls.Load(), "pre-flight failure must not pay for an LLM call")
	assert.Empty(t, s.queries.incrementCostCalls)
	assert.Zero(t, s.tx.inTxCalled)
}

// An errored turn that consumed tokens is real spend: it increments the
// pool cost in its own persist tx. It must NOT write team_name, so the
// next attempt's probe misses and the turn genuinely re-runs.
func (s *PickTeamNameTestSuite) TestErroredTurn_StillIncrementsPoolCost() {
	t := s.T()
	// Prose reply, no tool call: the agent never commits a name, but
	// the provider still billed for the round.
	s.llm.responses = []*llm.Response{{
		Content: "I would rather not choose a name.",
		Usage:   &llm.Usage{PromptTokens: 1000, CompletionTokens: 50, TotalTokens: 1050},
	}}

	_, err := s.env.ExecuteActivity(s.acts.PickTeamName, s.teamNameInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no valid name")

	require.Len(t, s.queries.incrementCostCalls, 1, "a paid-but-failed attempt is still real spend")
	costFloat, cerr := NumericToFloat(s.queries.incrementCostCalls[0].TotalLLMCostUSD)
	require.NoError(t, cerr)
	assert.Greater(t, costFloat, 0.0)

	assert.Empty(t, s.queries.setTeamNameCalls, "no name committed → the retry's probe must miss")
	require.Len(t, s.queries.insertTurnCalls, 1)
	assert.Equal(t, string(TurnStatusErrored), s.queries.insertTurnCalls[0].Status)
}

// A round-0 set_team_name call lands and is accepted by the collector, but
// the round-1 client.Complete call that follows the tool result fails —
// agentloop.Run returns a non-nil error alongside a partial Result carrying
// round-0's usage (see agentloop.Run's "round %d" error paths). turn.name is
// non-empty (the tool call was accepted) even though the loop itself failed.
//
// This pins the persist gate at header.Status == TurnStatusOK rather than
// turn.name != "": a regression to gating on the name alone would let this
// half-finished turn commit a name for a turn Temporal considers failed and
// will retry.
func (s *PickTeamNameTestSuite) TestPartialTurn_AcceptedNameButLoopErrors_DoesNotCommit() {
	t := s.T()
	s.llm.responses = []*llm.Response{setTeamNameToolCall("Rink Rats", "balanced")}
	s.llm.errs = []error{nil, errors.New("provider 500")}

	_, err := s.env.ExecuteActivity(s.acts.PickTeamName, s.teamNameInput())
	require.Error(t, err)

	assert.Empty(t, s.queries.setTeamNameCalls, "accepted tool call must not commit when the loop itself errored")

	require.Len(t, s.queries.incrementCostCalls, 1, "round-0 usage is still real spend")
	costFloat, cerr := NumericToFloat(s.queries.incrementCostCalls[0].TotalLLMCostUSD)
	require.NoError(t, cerr)
	assert.Greater(t, costFloat, 0.0)

	require.Len(t, s.queries.insertTurnCalls, 1)
	assert.Equal(t, string(TurnStatusErrored), s.queries.insertTurnCalls[0].Status)
}

// A successful turn whose team_name write fails inside the commit tx must
// surface the error — mirrors draft_activity_test.go's
// TestCommit_FailureSurfacesError. Exercises stubSimQueries.setTeamNameErr,
// which fails the mid-tx SetSimAgentTeamNameAndSummary write.
func (s *PickTeamNameTestSuite) TestCommit_TeamNameWriteFailureSurfacesError() {
	t := s.T()
	s.llm.responses = []*llm.Response{setTeamNameToolCall("Rink Rats", "balanced")}
	s.queries.setTeamNameErr = errors.New("unique constraint violation")

	_, err := s.env.ExecuteActivity(s.acts.PickTeamName, s.teamNameInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unique constraint violation")
}

// A successful turn whose durable cost increment fails inside the commit tx
// must also surface the error: a name committed without its cost landing
// would silently undercount the pool's spend against the cost cap.
func (s *PickTeamNameTestSuite) TestCommit_CostIncrementFailureSurfacesError() {
	t := s.T()
	s.llm.responses = []*llm.Response{setTeamNameToolCall("Rink Rats", "balanced")}
	s.queries.incrementCostErr = errors.New("deadlock detected")

	_, err := s.env.ExecuteActivity(s.acts.PickTeamName, s.teamNameInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deadlock detected")
}
