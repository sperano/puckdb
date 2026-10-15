package simulation

import (
	"context"
	"errors"
	"testing"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
)

// cancelingLLMClient answers one billed round and ends the activity
// context while doing so, the way a StartToClose deadline or a worker
// shutdown lands after the provider has charged for a round.
type cancelingLLMClient struct {
	resp   *llm.Response
	cancel context.CancelFunc
}

func (c *cancelingLLMClient) Complete(context.Context, *llm.Request) (*llm.Response, error) {
	c.cancel()
	return c.resp, nil
}

// cancelDuringTurn makes every agent built by acts answer resp once and
// then end the activity context of env.
func cancelDuringTurn(t *testing.T, env *testsuite.TestActivityEnvironment, acts *Activities, resp *llm.Response) {
	t.Helper()
	activityCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	env.SetWorkerOptions(worker.Options{BackgroundActivityContext: activityCtx})
	client := &cancelingLLMClient{resp: resp, cancel: cancel}
	acts.AgentFactory = func(_ int32, cfg AgentConfig, _ map[llm.Provider]llm.ProviderConfig, _ int) (*Agent, error) {
		return fakeAgent(client, cfg), nil
	}
}

// requireAbortedTurnSpend checks the write recordAbortedTurn makes: one
// transaction, on a live context, adding the billed round to the pool
// total and recording an errored turn row priced the same.
func requireAbortedTurnSpend(t *testing.T, q *stubSimQueries, tx *stubTransactor, wantKind ErrorKind) {
	t.Helper()
	require.Equal(t, 1, tx.inTxCalled, "one transaction for the aborted turn")
	assert.NoError(t, tx.ctxErrs[0], "the write must not run on the done activity context")
	assert.True(t, tx.deadlines[0], "the detached write is still bounded")

	require.Len(t, q.incrementCostCalls, 1)
	poolCost, err := NumericToFloat(q.incrementCostCalls[0].TotalLLMCostUSD)
	require.NoError(t, err)
	assert.Greater(t, poolCost, 0.0, "the billed round reaches the pool total")

	assert.Empty(t, q.deleteTurnCalls, "an aborted write never deletes a turn")
	assert.Empty(t, q.insertTurnCalls)
	require.Len(t, q.insertTurnIfAbsentCalls, 1)
	turn := q.insertTurnIfAbsentCalls[0]
	assert.Equal(t, string(TurnStatusErrored), turn.Status)
	assert.Equal(t, string(wantKind), turn.ErrorKind.String)
	assert.Equal(t, int32(1), turn.Rounds)
	turnCost, err := NumericToFloat(turn.CostUSD)
	require.NoError(t, err)
	assert.InDelta(t, poolCost, turnCost, costDelta, "turn row and pool total carry one spend")
}

// requireAcceptedToolCalls checks that the aborted turn's row records
// the tool calls the executor accepted before the context ended, each at
// its own (round, sequence): two at one coordinate would violate the
// sim_agent_tool_calls primary key and roll the spend back.
func requireAcceptedToolCalls(t *testing.T, q *stubSimQueries, wantTools ...string) {
	t.Helper()
	require.Len(t, q.insertToolCallBatches, 1)
	calls := q.insertToolCallBatches[0]
	require.Len(t, calls, len(wantTools))
	for i, call := range calls {
		assert.Equal(t, wantTools[i], call.ToolName)
		assert.Equal(t, string(ToolCallOutcomeAccepted), call.Outcome, "%s was accepted in memory", call.ToolName)
		assert.Equal(t, int32(0), call.RoundIndex)
		assert.Equal(t, int32(i), call.Sequence)
		assert.False(t, call.AppliedTransactionID.Valid, "nothing was applied")
	}
}

// draftNotesThenPick is one draft round that updates the notes and then
// drafts playerID: two accepted tool calls in round 0.
func draftNotesThenPick(playerID int64) *llm.Response {
	resp := draftPlayerToolCall(playerID, "best available")
	notes := llm.ToolCall{
		ID:       "tc_notes",
		Type:     "function",
		Function: llm.ToolCallFunction{Name: ToolUpdateNotes, Arguments: `{"notes":"take centers early"}`},
	}
	resp.ToolCalls = append([]llm.ToolCall{notes}, resp.ToolCalls...)
	return resp
}

// A draft turn cut off after a billed round adds that round to the pool
// total but commits nothing of the pick it had accepted in memory: the
// retry drafts from scratch.
func (s *DraftPickTestSuite) TestAbortedTurn_RecordsSpendWithoutPick() {
	t := s.T()
	cancelDuringTurn(t, s.env, s.acts, draftNotesThenPick(8478402))

	_, err := s.env.ExecuteActivity(s.acts.DraftPick, s.validInput())
	require.ErrorContains(t, err, "draft turn aborted")

	requireAbortedTurnSpend(t, s.queries, s.tx, ErrorKindLLMError)
	requireAcceptedToolCalls(t, s.queries, ToolUpdateNotes, ToolDraftPlayer)
	assert.Empty(t, s.queries.insertRosterCalls)
	assert.Empty(t, s.queries.insertDraftPickCalls)
	assert.Empty(t, s.queries.updateAgentNotesCalls)
}

// A draft turn that ends without any pick (the fallback finds no
// draftable player) still paid for its rounds.
func (s *DraftPickTestSuite) TestNoDraftablePlayer_RecordsSpendWithoutPick() {
	t := s.T()
	s.llm.responses = []*llm.Response{{
		Content: "no pick",
		Usage:   &llm.Usage{PromptTokens: 1000, CompletionTokens: 50, TotalTokens: 1050},
	}}

	_, err := s.env.ExecuteActivity(s.acts.DraftPick, s.validInput())
	require.ErrorContains(t, err, "no draftable player")

	requireAbortedTurnSpend(t, s.queries, s.tx, ErrorKindToolUseFailure)
	assert.Empty(t, s.queries.insertRosterCalls)
	assert.Empty(t, s.queries.insertDraftPickCalls)
}

// A daily turn cut off after a billed round adds that round to the pool
// total but leaves its accepted actions, notes and the day marker
// unwritten, so the retry runs the day again.
func (s *ManageRosterTestSuite) TestAbortedTurn_RecordsSpendWithoutActions() {
	t := s.T()
	cancelDuringTurn(t, s.env, s.acts, dailyToolResponse(ToolUpdateNotes, `{"notes":"watch the waiver wire"}`, ""))

	_, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.ErrorContains(t, err, "daily turn aborted")

	requireAbortedTurnSpend(t, s.queries, s.tx, ErrorKindLLMError)
	requireAcceptedToolCalls(t, s.queries, ToolUpdateNotes)
	assert.Empty(t, s.queries.updateAgentNotesCalls)
	assert.Empty(t, s.queries.insertDailyTurnDoneCalls, "no marker: the retry must run the day")
	assert.Empty(t, s.queries.insertPassCalls)
	assert.Empty(t, s.queries.insertErrorCalls)
}

// A team-name turn cut off after a billed round adds that round to the
// pool total and never writes the name it had accepted: a written name
// would be adopted by the retry as already chosen.
func (s *PickTeamNameTestSuite) TestAbortedTurn_RecordsSpendWithoutName() {
	t := s.T()
	cancelDuringTurn(t, s.env, s.acts, setTeamNameToolCall("Rink Rats", "balanced"))

	_, err := s.env.ExecuteActivity(s.acts.PickTeamName, s.teamNameInput())
	require.ErrorContains(t, err, "team-name turn aborted")

	requireAbortedTurnSpend(t, s.queries, s.tx, ErrorKindLLMError)
	requireAcceptedToolCalls(t, s.queries, ToolSetTeamName)
	assert.Empty(t, s.queries.setTeamNameCalls)
}

// A turn without a captured round made no model call: nothing to write.
func TestRecordAbortedTurnSkipsTurnWithoutRounds(t *testing.T) {
	tx := &stubTransactor{queries: &stubSimQueries{}}
	acts := &Activities{Tx: tx}
	cause := errors.New("build agent")

	err := acts.recordAbortedTurn(context.Background(), TurnTelemetry{}, cause)

	assert.Same(t, cause, err)
	assert.Zero(t, tx.inTxCalled)
}

// A failed write keeps the turn's own error visible next to it.
func TestRecordAbortedTurnJoinsWriteFailure(t *testing.T) {
	q := &stubSimQueries{insertTurnIfAbsentErr: errors.New("insert turn")}
	tx := &stubTransactor{queries: q}
	acts := &Activities{Tx: tx}
	cause := errors.New("turn aborted")

	err := acts.recordAbortedTurn(context.Background(), TurnTelemetry{
		Header:   TurnHeader{PoolID: 1, Phase: TurnPhaseTeamName},
		Captures: []roundCapture{capture(llm.Usage{PromptTokens: 1})},
	}, cause)

	require.ErrorIs(t, err, cause)
	assert.ErrorContains(t, err, "record aborted turn spend")
	assert.ErrorContains(t, err, "insert turn")
}

// A late write from a stale attempt, landing after the retry committed
// its turn, still adds its spend but leaves the retry's turn row alone.
func TestRecordAbortedTurnKeepsNewerTurn(t *testing.T) {
	q := &stubSimQueries{turnTaken: true}
	tx := &stubTransactor{queries: q}
	acts := &Activities{Tx: tx}
	cause := errors.New("turn aborted")

	err := acts.recordAbortedTurn(context.Background(), TurnTelemetry{
		Header:    TurnHeader{PoolID: 1, Phase: TurnPhaseTeamName, Spend: TurnSpend{CostUsd: 0.25}},
		Captures:  []roundCapture{capture(llm.Usage{PromptTokens: 1})},
		ToolCalls: []ToolCallCapture{{ToolName: ToolSetTeamName, Outcome: ToolCallOutcomeAccepted}},
	}, cause)

	assert.Same(t, cause, err, "a taken coordinate is not a write failure")
	require.Len(t, q.incrementCostCalls, 1, "the stale attempt's spend still counts")
	assert.Len(t, q.insertTurnIfAbsentCalls, 1)
	assert.Empty(t, q.deleteTurnCalls)
	assert.Empty(t, q.insertTurnRoundBatches, "no children without the turn row")
	assert.Empty(t, q.insertToolCallBatches)
}
