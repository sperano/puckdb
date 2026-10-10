package simulation

import (
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toolCallRows flattens every sim_agent_tool_calls batch the turn wrote.
func (s *ManageRosterTestSuite) toolCallRows() []sqlcdb.InsertSimAgentToolCallParams {
	var rows []sqlcdb.InsertSimAgentToolCallParams
	for _, b := range s.queries.insertToolCallBatches {
		rows = append(rows, b...)
	}
	return rows
}

// An add that lost the commit-time recheck never joined the roster, so a
// later drop_player of that player in the same turn is rejected too. It
// must not fail the turn (removeAndLogDrop would find no row) nor log a
// drop row, which would put a player another agent owns on waivers.
func (s *ManageRosterTestSuite) TestCommitConflict_DropOfRejectedAddIsRejected() {
	t := s.T()
	in := s.validInput()
	s.queries.existsRosterPlayerByPlayer = map[int64]bool{8480039: true}
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolAddPlayer, `{"player_id":8480039}`, ""),
		dailyToolResponse(ToolDropPlayer, `{"player_id":8480039}`, ""),
		finalTextResponse("..."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, in)
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.False(t, got.Errored)

	assert.Empty(t, s.queries.deleteRosterRowsCalls)
	assert.Empty(t, s.queries.insertDropCalls)
	require.Len(t, s.queries.insertDailyTurnDoneCalls, 1)
	rows := s.toolCallRows()
	require.Len(t, rows, 2)
	assert.Equal(t, string(ToolCallOutcomeCommitRejected), rows[1].Outcome)
	assert.Contains(t, rows[1].FailureReason.String, "drop_player(8480039)")
}

// An add replacement whose drop_player_id names a rejected add is
// rejected as well: its capacity check counted that drop.
func (s *ManageRosterTestSuite) TestCommitConflict_ReplacementDroppingRejectedAddIsRejected() {
	t := s.T()
	in := s.validInput()
	s.queries.existsRosterPlayerByPlayer = map[int64]bool{8480039: true}
	s.llm.responses = []*llm.Response{
		dailyToolResponse(ToolAddPlayer, `{"player_id":8480039}`, ""),
		dailyToolResponse(ToolAddPlayer, `{"player_id":8479318,"drop_player_id":8480039}`, ""),
		finalTextResponse("..."),
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, in)
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))
	assert.False(t, got.Errored)

	assert.Empty(t, s.queries.deleteRosterRowsCalls)
	assert.Empty(t, s.queries.insertAddCalls)
	rows := s.toolCallRows()
	require.Len(t, rows, 2)
	assert.Equal(t, string(ToolCallOutcomeCommitRejected), rows[1].Outcome)
	assert.Contains(t, rows[1].FailureReason.String, "drop_player_id 8480039 was never added")
}
