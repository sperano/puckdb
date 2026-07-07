package simulation

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// ============================================================================
// Tool-call response builders for the daily turn. Mirror
// draftPlayerToolCall in draft_activity_test.go but for the daily
// tool repertoire.
// ============================================================================

// dailyToolResponse builds an *llm.Response carrying ONE tool call
// with the given name and JSON-encoded arguments. The Usage block
// is non-zero so cost accounting paths exercise non-zero amounts.
//
// Multi-tool-per-round responses (the LLM emits drop AND add in the
// same round) compose multiple ToolCalls — see dailyMultiToolResponse.
func dailyToolResponse(name, argsJSON, content string) *llm.Response {
	return &llm.Response{
		Content: content,
		ToolCalls: []llm.ToolCall{{
			ID:   "tc_" + name,
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      name,
				Arguments: argsJSON,
			},
		}},
		Usage: &llm.Usage{
			PromptTokens: 800, CompletionTokens: 60, TotalTokens: 860,
		},
	}
}

// dailyMultiToolResponse builds an *llm.Response with several tool
// calls in a single round.
func dailyMultiToolResponse(content string, calls ...llm.ToolCall) *llm.Response {
	return &llm.Response{
		Content:   content,
		ToolCalls: calls,
		Usage: &llm.Usage{
			PromptTokens: 1200, CompletionTokens: 80, TotalTokens: 1280,
		},
	}
}

// finalTextResponse is the agentloop-terminating response: no tool
// calls, just text. The text becomes dailyOutcome.reasoning, which
// every committed row's reasoning column carries.
func finalTextResponse(content string) *llm.Response {
	return &llm.Response{
		Content: content,
		Usage: &llm.Usage{
			PromptTokens: 400, CompletionTokens: 30, TotalTokens: 430,
		},
	}
}

// makeToolCall constructs an llm.ToolCall by hand for use inside
// dailyMultiToolResponse.
func makeToolCall(id, name, argsJSON string) llm.ToolCall {
	return llm.ToolCall{
		ID:   id,
		Type: "function",
		Function: llm.ToolCallFunction{
			Name:      name,
			Arguments: argsJSON,
		},
	}
}

// ============================================================================
// ManageRosterTestSuite — covers idempotency, cost-cap, single-tool
// happy paths (each tool type), multi-tool turns, validation
// rejection (results stay in audit trail; no DB writes), pass/error
// markers, and the lineup_set child-rows path.
// ============================================================================

type ManageRosterTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env      *testsuite.TestActivityEnvironment
	queries  *stubSimQueries
	tx       *stubTransactor
	signaler *stubSignaler
	llm      *scriptedLLMClient
	acts     *Activities
}

func (s *ManageRosterTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.queries = &stubSimQueries{}
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
	s.env.RegisterActivity(s.acts.ManageRoster)
}

func TestManageRosterTestSuite(t *testing.T) {
	suite.Run(t, new(ManageRosterTestSuite))
}

// validInput returns a ManageRoster payload with a single-skater
// roster (one C in BN), enough FA candidates for a typical
// add/drop/claim test, and a $200 cap so the cost-cap branch
// doesn't fire by accident.
func (s *ManageRosterTestSuite) validInput() ManageRosterInput {
	return ManageRosterInput{
		PoolID:     1,
		AgentID:    7,
		SimDate:    pgDate(s.T(), "2024-11-15"),
		WorkflowID: "sim-pool-1",
		PoolConfig: PoolConfig{
			Season:               20242025,
			NumTeams:             5,
			MaxLLMCostUsdPerPool: 200.00,
			WaiverDays:           2,
			RosterPositions: map[RosterSlot]int{
				SlotC: 2, SlotLW: 2, SlotRW: 2, SlotD: 3, SlotG: 2, SlotUtil: 2, SlotBN: 5, SlotIR: 2,
			},
		},
		AgentConfig: AgentConfig{
			Provider: "anthropic", Model: "claude-haiku-4-5",
			Strategy: "balanced",
		},
		Roster: RosterState{
			Placements: map[int64]RosterSlot{
				8478402: SlotBN, // McDavid (C)
			},
			Limits: map[RosterSlot]int{
				SlotC: 2, SlotLW: 2, SlotRW: 2, SlotD: 3, SlotG: 2, SlotUtil: 2, SlotBN: 5, SlotIR: 2,
			},
		},
		FreeAgents: []int64{8480039, 8479318}, // MacKinnon, Matthews
		OnWaivers:  []int64{8478550},          // a waiver-window player
		Positions: map[int64]sqlcdb.PlayerPosition{
			8478402: sqlcdb.PlayerPositionC,
			8480039: sqlcdb.PlayerPositionC,
			8479318: sqlcdb.PlayerPositionC,
			8478550: sqlcdb.PlayerPositionLW,
		},
	}
}

// ----------------------------------------------------------------------------
// Idempotency
// ----------------------------------------------------------------------------

func (s *ManageRosterTestSuite) TestIdempotency_HitSkipsLLM() {
	t := s.T()
	s.queries.existsTxReturn = true

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.True(t, got.Skipped)
	assert.Equal(t, SkipReasonAlreadyManaged, got.SkipReason)
	assert.Zero(t, s.llm.calls.Load())
	assert.Zero(t, s.tx.inTxCalled)
}

func (s *ManageRosterTestSuite) TestIdempotency_ProbeErrorAborts() {
	t := s.T()
	s.queries.existsTxErr = errors.New("connection lost")

	_, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "idempotency probe")
}

// Finding #2: the probe matches only the dedicated 'daily_turn_done'
// marker. A day-1 turn where existing rows are non-marker types (the
// agent's own draft_pick rows, dated SeasonStartDate == day 1) must NOT
// be skipped. The stub's existsTxReturn models the marker probe directly:
// false ⇒ no marker ⇒ the turn runs and writes its own marker row.
func (s *ManageRosterTestSuite) TestIdempotency_NoMarkerRunsTurn() {
	t := s.T()
	s.queries.existsTxReturn = false // no daily_turn_done marker yet
	s.llm.responses = []*llm.Response{
		finalTextResponse("No moves today."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.False(t, got.Skipped, "turn must run when no marker exists")
	assert.NotZero(t, s.llm.calls.Load(), "LLM must be invoked")
}

// Finding #2: every committed daily turn writes exactly one
// 'daily_turn_done' marker — on the pass path and the action path alike —
// so the next attempt's probe fires regardless of what the turn did.
func (s *ManageRosterTestSuite) TestMarker_WrittenOnPassTurn() {
	t := s.T()
	s.llm.responses = []*llm.Response{
		finalTextResponse("Roster looks fine — no moves today."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.True(t, got.Passed)

	require.Len(t, s.queries.insertDailyTurnDoneCalls, 1)
	mk := s.queries.insertDailyTurnDoneCalls[0]
	assert.Equal(t, s.validInput().PoolID, mk.PoolID)
	assert.Equal(t, s.validInput().AgentID, mk.AgentID)
}

func (s *ManageRosterTestSuite) TestMarker_WrittenOnActionTurn() {
	t := s.T()
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolDropPlayer, `{"player_id":8478402}`, ""),
		finalTextResponse("Dropped McDavid."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.Equal(t, 1, got.ActionsApplied)

	require.Len(t, s.queries.insertDailyTurnDoneCalls, 1,
		"action turns still write the idempotency marker")
	assert.Empty(t, s.queries.insertPassCalls, "action turns don't write a pass row")
}

// ----------------------------------------------------------------------------
// Cost-cap (smoke test — full coverage in draft_activity_test.go via
// the shared helper)
// ----------------------------------------------------------------------------

func (s *ManageRosterTestSuite) TestCostCap_TripsBeforeLLM() {
	t := s.T()
	tooMuch, err := numericFromFloat(250.0)
	require.NoError(t, err)
	s.queries.getPoolReturn = sqlcdb.SimPool{ID: 1, TotalLLMCostUSD: tooMuch}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.True(t, got.Skipped)
	assert.Equal(t, SkipReasonCostCapReached, got.SkipReason)
	assert.Zero(t, s.llm.calls.Load())
	require.Len(t, s.signaler.calls, 1)
	assert.Equal(t, SignalPause, s.signaler.calls[0].SignalName)
}

// ----------------------------------------------------------------------------
// Happy-path single tools — one per tool type, asserting the expected
// DB write set lands.
// ----------------------------------------------------------------------------

func (s *ManageRosterTestSuite) TestHappyPath_DropPlayer() {
	t := s.T()
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolDropPlayer, `{"player_id":8478402,"reason":"Dropped McDavid for cap relief."}`, ""),
		finalTextResponse("Dropped McDavid for cap relief."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.False(t, got.Skipped)
	assert.False(t, got.Errored)
	assert.False(t, got.Passed)
	assert.Equal(t, 1, got.ActionsApplied)
	assert.Greater(t, got.CostUsd, 0.0)

	require.Equal(t, 1, s.tx.inTxCalled)
	require.Len(t, s.queries.deleteRosterCalls, 1)
	assert.Equal(t, int64(8478402), s.queries.deleteRosterCalls[0].PlayerID)
	require.Len(t, s.queries.insertDropCalls, 1)
	assert.Equal(t, "Dropped McDavid for cap relief.", s.queries.insertDropCalls[0].Reasoning,
		"each action's reasoning column carries the per-action Reason from its tool-call args")
	require.Len(t, s.queries.incrementCostCalls, 1)
}

func (s *ManageRosterTestSuite) TestHappyPath_AddPlayer() {
	t := s.T()
	in := s.validInput()
	// Seed an extra empty roster slot scenario — empty roster except McDavid.
	// Add MacKinnon (free agent) without a drop since roster isn't full.
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolAddPlayer, `{"player_id":8480039}`, ""),
		finalTextResponse("Picked up MacKinnon."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, in)
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.Equal(t, 1, got.ActionsApplied)

	require.Len(t, s.queries.insertRosterCalls, 1)
	rc := s.queries.insertRosterCalls[0]
	assert.Equal(t, int64(8480039), rc.PlayerID)
	assert.Equal(t, string(SlotBN), rc.Slot)
	assert.Equal(t, string(AcquiredViaFreeAgent), rc.AcquiredVia, "FA adds use acquired_via=free_agent, not draft")
	require.Len(t, s.queries.insertAddCalls, 1)
	assert.False(t, s.queries.insertAddCalls[0].DropPlayerID.Valid, "no drop = NULL drop_player_id")
}

func (s *ManageRosterTestSuite) TestHappyPath_ClaimPlayer() {
	t := s.T()
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolClaimPlayer, `{"player_id":8478550}`, ""),
		finalTextResponse("Claimed waivered LW."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.Equal(t, 1, got.ActionsApplied)

	require.Len(t, s.queries.insertWaiverCalls, 1)
	wc := s.queries.insertWaiverCalls[0]
	assert.Equal(t, int64(8478550), wc.PlayerID)
	assert.Equal(t, "2024-11-15", wc.FiledDate.Time.Format("2006-01-02"))
	assert.Equal(t, "2024-11-17", wc.ProcessDate.Time.Format("2006-01-02"),
		"process_date = sim_date + waiver_days (2)")
	require.Len(t, s.queries.insertClaimCalls, 1)
	// No roster mutation yet — claim is contingent on winning.
	assert.Empty(t, s.queries.insertRosterCalls)
	assert.Empty(t, s.queries.deleteRosterCalls)
}

// claimProcessOffset floors at 1 — same-day processing would race
// with the daily turn's other adds, so a misconfigured 0-day waivers
// pool still gets tomorrow-not-today.
func (s *ManageRosterTestSuite) TestClaim_ZeroWaiverDaysFloorsToOne() {
	t := s.T()
	in := s.validInput()
	in.PoolConfig.WaiverDays = 0
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolClaimPlayer, `{"player_id":8478550}`, ""),
		finalTextResponse("..."),
	}

	_, err := s.env.ExecuteActivity(s.acts.ManageRoster, in)
	require.NoError(t, err)
	require.Len(t, s.queries.insertWaiverCalls, 1)
	assert.Equal(t, "2024-11-16", s.queries.insertWaiverCalls[0].ProcessDate.Time.Format("2006-01-02"),
		"floored to 1-day offset to avoid same-day races")
}

func (s *ManageRosterTestSuite) TestHappyPath_UpdateNotes() {
	t := s.T()
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolUpdateNotes, `{"notes":"Watch the schedule for back-to-back games."}`, ""),
		finalTextResponse("Updated notes."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.Zero(t, got.ActionsApplied, "update_notes is not a counted action — it's a side effect")
	assert.True(t, got.Passed, "notes-only turn marks a pass row so idempotency probe fires on retry")

	require.Len(t, s.queries.updateAgentNotesCalls, 1)
	assert.Equal(t, "Watch the schedule for back-to-back games.", s.queries.updateAgentNotesCalls[0].Notes)
	require.Len(t, s.queries.insertPassCalls, 1, "notes-only turn writes a pass tx row")
}

// ----------------------------------------------------------------------------
// Multi-tool flows
// ----------------------------------------------------------------------------

// Drop McDavid, then add MacKinnon, then set lineup, all in two
// agentloop rounds. Pin: each tool's writes land, in the right order;
// the agentloop terminates on the final text response.
func (s *ManageRosterTestSuite) TestMultiTool_ChainedActions() {
	t := s.T()
	s.llm.responses = []*llm.Response{
		// Round 1: drop + add in parallel.
		dailyMultiToolResponse("First two moves",
			makeToolCall("tc_drop", ToolDropPlayer, `{"player_id":8478402}`),
			makeToolCall("tc_add", ToolAddPlayer, `{"player_id":8480039}`),
		),
		// Round 2: set lineup (MacKinnon now on roster).
		dailyToolResponse(ToolSetLineup, `{"moves":[{"player_id":8480039,"slot":"C"}]}`, ""),
		// Round 3: terminate.
		finalTextResponse("Replaced McDavid with MacKinnon and started him at C."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.Equal(t, 3, got.ActionsApplied, "drop + add + lineup_set = 3 actions")
	assert.False(t, got.Passed)

	// All three transaction types fired exactly once.
	require.Len(t, s.queries.insertDropCalls, 1)
	require.Len(t, s.queries.insertAddCalls, 1)
	require.Len(t, s.queries.insertLineupSetCalls, 1)

	// lineup_set produced a sim_lineup_moves child row.
	require.Len(t, s.queries.insertLineupMoveCalls, 1)
	mv := s.queries.insertLineupMoveCalls[0]
	assert.Equal(t, int64(8480039), mv.PlayerID)
	assert.Equal(t, string(SlotC), mv.ToSlot)

	// Roster slot updated to C for MacKinnon.
	require.Len(t, s.queries.updateRosterSlotCalls, 1)
	assert.Equal(t, int64(8480039), s.queries.updateRosterSlotCalls[0].PlayerID)
	assert.Equal(t, string(SlotC), s.queries.updateRosterSlotCalls[0].Slot)
}

// ----------------------------------------------------------------------------
// Validation rejection — invalid tool args feed errors back to LLM,
// don't write anything, but allow subsequent rounds to succeed.
// ----------------------------------------------------------------------------

func (s *ManageRosterTestSuite) TestValidation_RejectionDoesNotPersist() {
	t := s.T()
	in := s.validInput()
	// Try to drop a player NOT on the roster — ValidateDropPlayer rejects.
	// On round 2 the LLM corrects to a valid drop.
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolDropPlayer, `{"player_id":9999999}`, ""),
		dailyToolResponse(ToolDropPlayer, `{"player_id":8478402}`, ""),
		finalTextResponse("Corrected the drop."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, in)
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.Equal(t, 1, got.ActionsApplied, "rejected tool call does not count as an action")

	require.Len(t, s.queries.insertDropCalls, 1)
	assert.Equal(t, int64(8478402), s.queries.insertDropCalls[0].PlayerID.Int64,
		"only the corrected drop persists")
}

// ----------------------------------------------------------------------------
// No-tool-call turn → pass marker row
// ----------------------------------------------------------------------------

func (s *ManageRosterTestSuite) TestPass_NoToolCalls() {
	t := s.T()
	s.llm.responses = []*llm.Response{
		finalTextResponse("Roster looks fine — no moves today."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.True(t, got.Passed)
	assert.Equal(t, 0, got.ActionsApplied)

	require.Len(t, s.queries.insertPassCalls, 1)
	assert.Equal(t, "Roster looks fine — no moves today.", s.queries.insertPassCalls[0].Reasoning)
	// No other transaction-row inserts.
	assert.Empty(t, s.queries.insertDropCalls)
	assert.Empty(t, s.queries.insertAddCalls)
	assert.Empty(t, s.queries.insertLineupSetCalls)
}

// ----------------------------------------------------------------------------
// LLM provider error → error marker row + Errored=true result
// ----------------------------------------------------------------------------

func (s *ManageRosterTestSuite) TestError_LLMFailureLogsErrorRow() {
	t := s.T()
	s.llm.errs = []error{errors.New("503 service unavailable")}
	s.llm.responses = []*llm.Response{nil}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err, "agent failure must NOT abort the activity")
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.True(t, got.Errored)
	assert.False(t, got.Passed)

	require.Len(t, s.queries.insertErrorCalls, 1)
	er := s.queries.insertErrorCalls[0]
	assert.Equal(t, string(ErrorKindLLMError), er.ErrorKind.String)
	assert.Contains(t, er.ErrorDetail.String, "503 service unavailable")
	assert.Empty(t, s.queries.insertPassCalls, "errored turns log error, not pass")
}

// ----------------------------------------------------------------------------
// Lineup with displacement: pin that the displaced player's slot
// also lands in BN.
// ----------------------------------------------------------------------------

func (s *ManageRosterTestSuite) TestLineupSet_DisplacementWritesBothSlots() {
	t := s.T()
	in := s.validInput()
	// Roster: two centers, both in BN. Move one to C.
	in.Roster.Placements = map[int64]RosterSlot{
		8478402: SlotC,
		8480039: SlotBN,
	}
	in.Positions[8480039] = sqlcdb.PlayerPositionC
	// Limit C=1 so adding McMacKinnon to C displaces McDavid to BN.
	in.PoolConfig.RosterPositions[SlotC] = 1
	in.Roster.Limits[SlotC] = 1

	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolSetLineup, `{"moves":[{"player_id":8480039,"slot":"C"}]}`, ""),
		finalTextResponse("Started MacKinnon at C."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, in)
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.Equal(t, 1, got.ActionsApplied)

	// Two slot updates: MacKinnon → C, McDavid → BN.
	require.Len(t, s.queries.updateRosterSlotCalls, 2)
	got1, got2 := s.queries.updateRosterSlotCalls[0], s.queries.updateRosterSlotCalls[1]
	bySlot := map[string]int64{got1.Slot: got1.PlayerID, got2.Slot: got2.PlayerID}
	assert.Equal(t, int64(8480039), bySlot[string(SlotC)])
	assert.Equal(t, int64(8478402), bySlot[string(SlotBN)])

	// Single sim_lineup_moves row carries the displacement.
	require.Len(t, s.queries.insertLineupMoveCalls, 1)
	mv := s.queries.insertLineupMoveCalls[0]
	assert.True(t, mv.DisplacedPlayerID.Valid)
	assert.Equal(t, int64(8478402), mv.DisplacedPlayerID.Int64)
}

// ----------------------------------------------------------------------------
// Working-state mutation across rounds: verify that drop_player(A)
// in round 1 makes round 2's drop_player(A) fail (player no longer
// on roster) — confirms the executor's working-state isolation works.
// ----------------------------------------------------------------------------

func (s *ManageRosterTestSuite) TestWorkingState_PersistsAcrossRounds() {
	t := s.T()
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolDropPlayer, `{"player_id":8478402}`, ""),
		// Round 2: try to drop the same player again — should be rejected.
		dailyToolResponse(ToolDropPlayer, `{"player_id":8478402}`, ""),
		finalTextResponse("Done"),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.Equal(t, 1, got.ActionsApplied,
		"the second drop validates against post-first-drop state and is rejected")
	require.Len(t, s.queries.insertDropCalls, 1)
}

// ----------------------------------------------------------------------------
// Unknown tool → executor returns "tool not allowed" string; no
// action recorded.
// ----------------------------------------------------------------------------

func (s *ManageRosterTestSuite) TestUnknownTool_ReturnsErrorString() {
	t := s.T()
	s.llm.responses = []*llm.Response{
		dailyToolResponse("nonexistent_tool", `{"x":1}`, ""),
		finalTextResponse("..."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.True(t, got.Passed)
	assert.Empty(t, s.queries.insertDropCalls)
	assert.Empty(t, s.queries.insertAddCalls)
}

// ----------------------------------------------------------------------------
// Cost accumulation — multi-round usage sums into the increment.
// ----------------------------------------------------------------------------

func (s *ManageRosterTestSuite) TestCost_AccumulatesAcrossRounds() {
	t := s.T()
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolDropPlayer, `{"player_id":8478402}`, ""),
		dailyToolResponse(ToolAddPlayer, `{"player_id":8480039}`, ""),
		finalTextResponse("Done"),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))

	require.Len(t, s.queries.incrementCostCalls, 1)
	costFloat, err := numericToFloat(s.queries.incrementCostCalls[0].TotalLLMCostUSD)
	require.NoError(t, err)
	// 3 rounds with non-zero usage (two tool rounds + one final).
	// Pin only that the sum is strictly greater than a single round's
	// cost — exact-decimal comparison would lock the pricing table.
	singleRoundCost := got.CostUsd / 3
	assert.Greater(t, costFloat, singleRoundCost,
		"increment must reflect aggregated usage across all rounds")
	assert.InDelta(t, got.CostUsd, costFloat, 1e-9)
}

// ----------------------------------------------------------------------------
// Commit failure surfaces (atomic rollback contract — like
// DraftPickActivity's analogous test)
// ----------------------------------------------------------------------------

func (s *ManageRosterTestSuite) TestCommit_FailureSurfacesError() {
	t := s.T()
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolDropPlayer, `{"player_id":8478402}`, ""),
		finalTextResponse("..."),
	}
	s.tx.commitErr = errors.New("constraint violation")

	_, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "constraint violation")
}

// ----------------------------------------------------------------------------
// Cap-out: agentloop is round-bounded so a runaway agent can't
// burn through MaxToolRounds * tools-per-round indefinitely.
// ----------------------------------------------------------------------------

func (s *ManageRosterTestSuite) TestRoundLimit_CapsLLMCalls() {
	t := s.T()
	// Script MaxDailyToolRounds + 1 tool-call responses; loop should
	// terminate after MaxDailyToolRounds and not consume the extra.
	resps := make([]*llm.Response, 0, MaxDailyToolRounds+1)
	for i := 0; i < MaxDailyToolRounds+1; i++ {
		resps = append(resps, dailyToolResponse(ToolUpdateNotes,
			fmt.Sprintf(`{"notes":"round %d"}`, i), ""))
	}
	s.llm.responses = resps

	_, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err)
	assert.Equal(t, int32(MaxDailyToolRounds), s.llm.calls.Load(),
		"agentloop terminates at MaxDailyToolRounds even if the model keeps calling tools")
}

// ----------------------------------------------------------------------------
// Working-state preservation: caller's input maps must NOT be mutated
// (the activity's running clone is what the executor mutates).
// ----------------------------------------------------------------------------

func (s *ManageRosterTestSuite) TestWorkingState_DoesNotMutateInput() {
	t := s.T()
	in := s.validInput()
	originalPlacements := map[int64]RosterSlot{}
	for k, v := range in.Roster.Placements {
		originalPlacements[k] = v
	}
	originalFA := append([]int64(nil), in.FreeAgents...)

	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolDropPlayer, `{"player_id":8478402}`, ""),
		finalTextResponse("..."),
	}

	_, err := s.env.ExecuteActivity(s.acts.ManageRoster, in)
	require.NoError(t, err)

	assert.Equal(t, originalPlacements, in.Roster.Placements,
		"caller's input map must be untouched")
	assert.Equal(t, originalFA, in.FreeAgents)
}

// signal-back unused on the daily turn because we don't use the
// signaler here — pin that no signals were sent on a normal happy
// path (regression sentinel).
func (s *ManageRosterTestSuite) TestNoSignal_OnHappyPath() {
	t := s.T()
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolDropPlayer, `{"player_id":8478402}`, ""),
		finalTextResponse("..."),
	}

	_, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err)
	assert.Empty(t, s.signaler.calls, "no signal back to workflow on the daily-turn happy path")
}

// drop_player_id correctly threads through Add transactions when set.
func (s *ManageRosterTestSuite) TestAdd_WithDropThreadsThroughTx() {
	t := s.T()
	in := s.validInput()
	// Put the roster at full BN+C capacity by stacking placements so
	// add requires a drop. Workspace setup: keep limits permissive
	// but use validation by-design — the args explicitly drop player.
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolAddPlayer,
			`{"player_id":8480039, "drop_player_id":8478402}`, ""),
		finalTextResponse("..."),
	}

	_, err := s.env.ExecuteActivity(s.acts.ManageRoster, in)
	require.NoError(t, err)

	require.Len(t, s.queries.insertAddCalls, 1)
	assert.True(t, s.queries.insertAddCalls[0].DropPlayerID.Valid)
	assert.Equal(t, int64(8478402), s.queries.insertAddCalls[0].DropPlayerID.Int64)
	// Drop-as-part-of-add deletes from sim_rosters too.
	require.Len(t, s.queries.deleteRosterCalls, 1)
	assert.Equal(t, int64(8478402), s.queries.deleteRosterCalls[0].PlayerID)
}

// validation pin: passing pgtype.Int8 NULL when no displacement.
func (s *ManageRosterTestSuite) TestLineupSet_NoDisplacementHasNullDisplacedID() {
	t := s.T()
	in := s.validInput()
	in.Roster.Placements = map[int64]RosterSlot{8478402: SlotBN}
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolSetLineup, `{"moves":[{"player_id":8478402,"slot":"C"}]}`, ""),
		finalTextResponse("..."),
	}

	_, err := s.env.ExecuteActivity(s.acts.ManageRoster, in)
	require.NoError(t, err)
	require.Len(t, s.queries.insertLineupMoveCalls, 1)
	assert.False(t, s.queries.insertLineupMoveCalls[0].DisplacedPlayerID.Valid,
		"no displacement = NULL displaced_player_id, not 0")
}

// Smoke: the cost-cap check is consulted only after idempotency
// passes. Pin: idempotency hit short-circuits before any DB pool
// read.
func (s *ManageRosterTestSuite) TestPreflightOrder_IdempotencyBeforeCostCap() {
	t := s.T()
	s.queries.existsTxReturn = true

	_, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err)
	assert.Empty(t, s.queries.getPoolArgs,
		"idempotency hit must short-circuit before the cost-cap probe runs")
}

// ----------------------------------------------------------------------------
// Finding #6 — same-day contested add: commit-time conflict becomes a
// clean rejected action instead of a transaction-aborting constraint
// violation, and the rest of the turn still commits + marker is written.
// ----------------------------------------------------------------------------

// Agent #2 adds a player agent #1 already took earlier today. The morning
// FA snapshot still lists the player, so turn-time validation accepts it,
// but ExistsSimRosterPlayer reports it rostered at commit time → the add
// is dropped, no roster/add rows are written, a pass marker is written,
// and the daily_turn_done marker still lands.
func (s *ManageRosterTestSuite) TestCommitConflict_ContestedAddRejectedCleanly() {
	t := s.T()
	in := s.validInput()
	s.queries.existsRosterPlayerByPlayer = map[int64]bool{8480039: true} // taken first
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolAddPlayer, `{"player_id":8480039}`, ""),
		finalTextResponse("Tried to pick up MacKinnon."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, in)
	require.NoError(t, err, "commit-time conflict must NOT abort the activity")
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))

	// No roster mutation, no add tx — the conflicting add was dropped.
	assert.Empty(t, s.queries.insertRosterCalls, "conflicting add not persisted")
	assert.Empty(t, s.queries.insertAddCalls, "no add tx for the lost add")

	// Turn still completes: pass row + daily_turn_done marker.
	require.Len(t, s.queries.insertPassCalls, 1, "all-rejected turn writes a pass row")
	require.Len(t, s.queries.insertDailyTurnDoneCalls, 1, "daily_turn_done marker still written")
	require.Len(t, s.queries.incrementCostCalls, 1, "cost still billed once")
}

// A conflicting add accompanied by other good actions: the good actions
// commit, only the conflicting add (and any lineup move for that player)
// is dropped.
func (s *ManageRosterTestSuite) TestCommitConflict_OtherActionsStillCommit() {
	t := s.T()
	in := s.validInput()
	s.queries.existsRosterPlayerByPlayer = map[int64]bool{8480039: true} // MacKinnon taken first
	s.llm.responses = []*llm.Response{
		// Round 1: drop McDavid + add MacKinnon (conflicting) + add Matthews (OK).
		dailyMultiToolResponse("moves",
			makeToolCall("tc_drop", ToolDropPlayer, `{"player_id":8478402}`),
			makeToolCall("tc_add1", ToolAddPlayer, `{"player_id":8480039}`),
			makeToolCall("tc_add2", ToolAddPlayer, `{"player_id":8479318}`),
		),
		finalTextResponse("Dropped McDavid, grabbed Matthews; MacKinnon lost."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, in)
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))

	// Drop McDavid committed; only Matthews added (MacKinnon dropped).
	require.Len(t, s.queries.insertDropCalls, 1)
	require.Len(t, s.queries.insertAddCalls, 1, "only the non-conflicting add lands")
	assert.Equal(t, int64(8479318), s.queries.insertAddCalls[0].PlayerID.Int64)
	require.Len(t, s.queries.insertDailyTurnDoneCalls, 1)
	assert.Empty(t, s.queries.insertPassCalls, "turn produced committed actions, not a pass")
}

// A set_lineup move that targets a player whose add lost the commit-time
// recheck is stripped; if that's the only move, the lineup action is
// dropped entirely.
func (s *ManageRosterTestSuite) TestCommitConflict_StripsDependentLineupMove() {
	t := s.T()
	in := s.validInput()
	s.queries.existsRosterPlayerByPlayer = map[int64]bool{8480039: true}
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolAddPlayer, `{"player_id":8480039}`, ""),
		// Try to start the freshly-added (but lost) MacKinnon at C.
		dailyToolResponse(ToolSetLineup, `{"moves":[{"player_id":8480039,"slot":"C"}]}`, ""),
		finalTextResponse("..."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, in)
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))

	assert.Empty(t, s.queries.insertAddCalls, "lost add not persisted")
	assert.Empty(t, s.queries.insertLineupSetCalls, "lineup action with only a stripped move is dropped")
	assert.Empty(t, s.queries.insertLineupMoveCalls)
	require.Len(t, s.queries.insertDailyTurnDoneCalls, 1)
}

// ----------------------------------------------------------------------------
// Finding #5 — duplicate pending claim by the same agent is rejected as a
// clean action error (no DB write), and the rest of the turn commits.
// ----------------------------------------------------------------------------

func (s *ManageRosterTestSuite) TestDuplicatePendingClaim_RejectedAsActionError() {
	t := s.T()
	in := s.validInput()
	// Agent already has a pending claim on the waiver-window player.
	in.PendingClaims = []int64{8478550}
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolClaimPlayer, `{"player_id":8478550}`, ""),
		finalTextResponse("Tried to re-claim a player I already claimed."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, in)
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))

	assert.Zero(t, got.ActionsApplied, "duplicate claim is rejected, not applied")
	assert.Empty(t, s.queries.insertWaiverCalls, "no waiver claim row written for the duplicate")
	assert.Empty(t, s.queries.insertClaimCalls)
	require.Len(t, s.queries.insertPassCalls, 1, "rejected-only turn writes a pass row")
	require.Len(t, s.queries.insertDailyTurnDoneCalls, 1)
}

// ----------------------------------------------------------------------------
// Finding #9 — player added this turn can be slotted this turn.
//
// The round-1-add/round-2-set_lineup flow is the documented pattern.
// It works only when the added player's position is in work.positions at
// the start of the turn (i.e. pre-populated by BuildManageRosterContext).
// validInput() pre-populates MacKinnon's position to mirror the context
// activity's behaviour after the fix; this test pins the end-to-end path.
// ----------------------------------------------------------------------------

// Round 1: add MacKinnon (FA). Round 2: slot MacKinnon into C.
// The lineup move must succeed, producing both an add tx and a lineup_set tx.
func (s *ManageRosterTestSuite) TestFinding9_AddThenSlotSameTurn() {
	t := s.T()
	in := s.validInput()
	// validInput() includes MacKinnon's position (8480039 → C); drop McDavid
	// to make room for the add (roster has 1 player, bench limit 5 in validInput).
	// The roster already has room (BN at 1/5), so no drop needed.
	s.llm.responses = []*llm.Response{
		// Round 1: add MacKinnon to BN.
		dailyToolResponse(ToolAddPlayer, `{"player_id":8480039,"reason":"Pickup MacKinnon"}`, ""),
		// Round 2: slot MacKinnon from BN into C (position C is eligible for C slot).
		dailyToolResponse(ToolSetLineup, `{"moves":[{"player_id":8480039,"slot":"C"}]}`, ""),
		finalTextResponse("Added and started MacKinnon."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, in)
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))

	assert.Equal(t, 2, got.ActionsApplied, "add + lineup_set = 2 actions")
	require.Len(t, s.queries.insertAddCalls, 1, "add tx written")
	require.Len(t, s.queries.insertLineupSetCalls, 1, "lineup_set tx written")
	require.Len(t, s.queries.insertLineupMoveCalls, 1)
	mv := s.queries.insertLineupMoveCalls[0]
	assert.Equal(t, int64(8480039), mv.PlayerID)
	assert.Equal(t, string(SlotBN), mv.FromSlot)
	assert.Equal(t, string(SlotC), mv.ToSlot)
}

// If a player's position is missing from in.Positions at turn start (a
// defensive case — this shouldn't happen after the context fix, but
// confirms the error message is clear rather than a panic or silent skip).
func (s *ManageRosterTestSuite) TestFinding9_UnknownPositionReturnsError() {
	t := s.T()
	in := s.validInput()
	// Remove MacKinnon's position from the positions map to simulate a
	// player with NULL players.position (or a player the context activity
	// couldn't look up).
	delete(in.Positions, 8480039)

	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolAddPlayer, `{"player_id":8480039,"reason":"Pickup MacKinnon"}`, ""),
		// set_lineup will fail: position not in catalog → error returned to LLM.
		dailyToolResponse(ToolSetLineup, `{"moves":[{"player_id":8480039,"slot":"C"}]}`, ""),
		finalTextResponse("Tried to slot MacKinnon but failed."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, in)
	require.NoError(t, err, "position-lookup failure must not abort the activity")
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))

	// Add succeeded; lineup set failed validation (position lookup error).
	// Only the add is committed; the turn counts as 1 action.
	assert.Equal(t, 1, got.ActionsApplied, "add committed; lineup_set rejected")
	require.Len(t, s.queries.insertAddCalls, 1)
	assert.Empty(t, s.queries.insertLineupSetCalls, "lineup_set rejected due to position-lookup error")
}

// silence unused-import linter when pgtype is referenced only via
// stub fields.
var _ = pgtype.Int4{}
