package simulation

import (
	"encoding/json"
	"errors"

	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// DraftPick transcript recording — the record_full_messages flag must be
// read BEFORE the LLM runs, and when it is on the full agentloop
// transcript (draft prompt + every assistant/tool exchange) must land in
// sim_agent_turn_messages inside the same tx as the draft_pick row.
// ============================================================================

// recordedDraftTranscript returns the single message batch the commit
// wrote, failing loudly if there wasn't exactly one.
func (s *DraftPickTestSuite) recordedDraftTranscript() []sqlcdb.InsertSimAgentTurnMessageParams {
	t := s.T()
	t.Helper()
	require.Len(t, s.queries.insertTurnMessageBatches, 1, "exactly one transcript batch per committed pick")
	return s.queries.insertTurnMessageBatches[0]
}

// assertTranscriptRoles pins the role sequence of a recorded transcript
// and that ordinals are dense from 0 — the replay UI orders on ordinal.
func (s *DraftPickTestSuite) assertTranscriptRoles(rows []sqlcdb.InsertSimAgentTurnMessageParams, want ...string) {
	t := s.T()
	t.Helper()
	require.Len(t, rows, len(want))
	for i, row := range rows {
		assert.Equal(t, want[i], row.Role, "ordinal %d role", i)
		assert.Equal(t, int32(i), row.Ordinal, "ordinals must be dense from 0")
	}
}

func (s *DraftPickTestSuite) setRecordFullMessages(on bool) {
	s.queries.getRecordFullMessagesOverride = &on
}

// The record_full_messages read is a PRE-flight: when it fails the
// activity must error with zero LLM invocations and no commit. Reading
// it at commit time (the old shape) made a transient DB error discard a
// paid-for pick and re-bill it on the Temporal retry.
func (s *DraftPickTestSuite) TestRecordFullMessagesReadFails_MakesNoLLMCall() {
	t := s.T()
	s.queries.getRecordFullMessagesErr = errors.New("connection reset by peer")
	s.llm.responses = []*llm.Response{draftPlayerToolCall(8478402, "McDavid")}

	_, err := s.env.ExecuteActivity(s.acts.DraftPick, s.validInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "record_full_messages")

	assert.Zero(t, s.llm.calls.Load(), "pre-flight failure must not pay for an LLM call")
	assert.Zero(t, s.tx.inTxCalled, "no commit tx when the pre-LLM flag read fails")
	assert.Empty(t, s.queries.insertDraftPickCalls)
	assert.Empty(t, s.queries.incrementCostCalls)
}

// Flag on, clean single-round pick: the transcript is the two-message
// draft prompt plus the assistant's draft_player call and its tool
// result, written in the same InTx block as the roster/draft_pick rows.
func (s *DraftPickTestSuite) TestRecordFullMessages_EnabledPersistsDraftTranscript() {
	t := s.T()
	s.setRecordFullMessages(true)
	in := s.validInput()
	in.DraftPrompt.DraftInfo = DraftInfo{Round: 1, Pick: 3, OverallPick: 3, SnakeDirection: "forward"}
	s.llm.responses = []*llm.Response{draftPlayerToolCall(8478402, "McDavid is the best skater available")}

	future, err := s.env.ExecuteActivity(s.acts.DraftPick, in)
	require.NoError(t, err)
	require.NoError(t, future.Get(new(DraftPickResult)))

	require.Equal(t, 1, s.tx.inTxCalled, "transcript must ride the single atomic commit")
	require.Len(t, s.queries.insertDraftPickCalls, 1)

	rows := s.recordedDraftTranscript()
	s.assertTranscriptRoles(rows, "system", "user", "assistant", "tool")

	assert.Equal(t, "test system prompt", rows[0].Content)
	assert.Contains(t, rows[1].Content, `"snake_direction":"forward"`, "user turn carries the rendered draft prompt")

	var assistantCalls []llm.ToolCall
	require.NoError(t, json.Unmarshal(rows[2].ToolCalls, &assistantCalls))
	require.Len(t, assistantCalls, 1, "assistant turn carries the draft_player call")
	assert.Equal(t, ToolDraftPlayer, assistantCalls[0].Function.Name)
	assert.Contains(t, assistantCalls[0].Function.Arguments, "8478402")

	assert.Equal(t, "ok: player drafted", rows[3].Content)
	assert.Equal(t, "tc_test", rows[3].ToolCallID.String)
	assert.True(t, rows[3].ToolCallID.Valid)
}

// Flag off: the turn header, rounds and tool calls still land but no
// sim_agent_turn_messages rows are written.
func (s *DraftPickTestSuite) TestRecordFullMessages_DisabledStoresNoTranscript() {
	t := s.T()
	s.setRecordFullMessages(false)
	s.llm.responses = []*llm.Response{draftPlayerToolCall(8478402, "McDavid")}

	future, err := s.env.ExecuteActivity(s.acts.DraftPick, s.validInput())
	require.NoError(t, err)
	require.NoError(t, future.Get(new(DraftPickResult)))

	require.Len(t, s.queries.insertTurnCalls, 1, "turn header still recorded")
	assert.NotEmpty(t, s.queries.insertToolCallBatches, "tool-call audit still recorded")
	assert.Empty(t, s.queries.insertTurnMessageBatches, "no transcript rows when the flag is off")
}

// A rejected draft_player (unavailable ID) followed by an accepted one:
// both exchanges are part of the transcript, including the validation
// error the executor fed back to the model.
func (s *DraftPickTestSuite) TestRecordFullMessages_RejectedToolCallStaysInTranscript() {
	t := s.T()
	s.setRecordFullMessages(true)
	s.llm.responses = []*llm.Response{
		draftPlayerToolCall(99999, "made-up ID"),
		draftPlayerToolCall(8480039, "MacKinnon"),
	}

	future, err := s.env.ExecuteActivity(s.acts.DraftPick, s.validInput())
	require.NoError(t, err)
	var got DraftPickResult
	require.NoError(t, future.Get(&got))
	assert.Equal(t, int64(8480039), got.PlayerID)

	rows := s.recordedDraftTranscript()
	s.assertTranscriptRoles(rows, "system", "user", "assistant", "tool", "assistant", "tool")
	assert.Contains(t, rows[3].Content, "not available", "rejection fed back to the model is recorded")
	assert.Equal(t, "ok: player drafted", rows[5].Content)
}

// Provider error on the first round → fallback pick. The loop still
// returns the partial history, so the recorded transcript is the draft
// prompt the model was sent (system + user), even though no assistant
// turn ever arrived. The fallback's synthetic tool-call capture is a
// sim_agent_tool_calls row, not a message.
func (s *DraftPickTestSuite) TestRecordFullMessages_FallbackPersistsPromptOnly() {
	t := s.T()
	s.setRecordFullMessages(true)
	in := s.validInput()
	in.RankedSkaters = []SkaterDraftCandidate{
		{PlayerID: 9001, Position: "C", PriorG: 60, PriorA: 80, Name: "Top C"},
	}
	s.llm.errs = []error{errors.New("503 service unavailable")}

	future, err := s.env.ExecuteActivity(s.acts.DraftPick, in)
	require.NoError(t, err)
	var got DraftPickResult
	require.NoError(t, future.Get(&got))
	require.True(t, got.UsedFallback)

	rows := s.recordedDraftTranscript()
	s.assertTranscriptRoles(rows, "system", "user")
}

// A cost-cap trip short-circuits before the flag read, mirroring the
// idempotency probe: nothing that only the commit path needs is touched.
func (s *DraftPickTestSuite) TestRecordFullMessages_NotReadOnCostCapTrip() {
	t := s.T()
	tooMuch, err := numericFromFloat(200.01)
	require.NoError(t, err)
	s.queries.getPoolReturn = sqlcdb.SimPool{ID: 1, TotalLLMCostUSD: tooMuch}

	future, err := s.env.ExecuteActivity(s.acts.DraftPick, s.validInput())
	require.NoError(t, err)
	require.NoError(t, future.Get(new(DraftPickResult)))

	assert.Empty(t, s.queries.getRecordFullMessagesArgs, "cost-cap branch never reaches the flag read")
}
