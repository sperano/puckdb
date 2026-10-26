package simulation

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/pgconv"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// markerConflict is the error PostgreSQL returns when a marker insert
// finds the row an overlapping attempt committed.
func markerConflict(index string) error {
	return &pgconn.PgError{Code: pgUniqueViolation, ConstraintName: index}
}

func TestIsMarkerConflict(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"marker violation", markerConflict(draftPickMarkerIndex), true},
		{"wrapped", fmt.Errorf("insert draft_pick tx: %w", markerConflict(draftPickMarkerIndex)), true},
		{"other marker", markerConflict(dailyTurnMarkerIndex), false},
		{"other unique index", markerConflict("sim_rosters_pool_id_player_id_key"), false},
		{"other code", &pgconn.PgError{Code: "23503", ConstraintName: draftPickMarkerIndex}, false},
		{"not a PostgreSQL error", errors.New(draftPickMarkerIndex), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, isMarkerConflict(tc.err, draftPickMarkerIndex), tc.name)
	}
}

// requireOnlySpendRecorded checks that a losing attempt wrote nothing of
// its turn but its spend: one increment of costUsd and a turn row only
// if absent, never the delete-then-insert that would replace the
// winner's turn.
func requireOnlySpendRecorded(t *testing.T, pool *poolFake, turns *turnRowsFake, costUsd float64) {
	t.Helper()
	require.Len(t, pool.incrementCostCalls, 1, "the losing attempt's spend counts once")
	got, err := pgconv.NumericToFloat(pool.incrementCostCalls[0].TotalLLMCostUSD)
	require.NoError(t, err)
	assert.InDelta(t, costUsd, got, 1e-9)
	assert.Empty(t, turns.deleteTurnCalls, "must not delete the winner's turn")
	assert.Empty(t, turns.insertTurnCalls, "must not write a turn of its own")
	assert.Len(t, turns.insertTurnIfAbsentCalls, 1)
}

// The overlapping attempt commits the slot while this one is in its
// commit: the marker insert fails before the roster row, and the result
// is the committed pick with this attempt's spend.
func (s *DraftPickTestSuite) TestMarkerConflict_ReturnsCommittedPick() {
	t := s.T()
	const committedPlayer = 8480039
	s.llm.responses = []*llm.Response{draftPlayerToolCall(8478402, "McDavid")}
	s.queries.turnTaken = true
	s.tx.beforeFn = func() {
		s.queries.getDraftPickErr = nil
		s.queries.getDraftPickReturn = sqlcdb.SimTransaction{
			ID:       99,
			PlayerID: pgtype.Int8{Int64: committedPlayer, Valid: true},
			Type:     string(TransactionTypeDraftPick),
		}
		s.queries.insertDraftPickErr = markerConflict(draftPickMarkerIndex)
	}

	future, err := s.env.ExecuteActivity(s.acts.DraftPick, s.validInput())
	require.NoError(t, err)
	var got DraftPickResult
	require.NoError(t, future.Get(&got))

	assert.True(t, got.Skipped)
	assert.Equal(t, SkipReasonAlreadyDrafted, got.SkipReason)
	assert.Equal(t, int64(committedPlayer), got.PlayerID)
	assert.Greater(t, got.CostUsd, 0.0, "the model call was billed")
	assert.Empty(t, s.queries.insertRosterCalls, "the marker goes first")
	assert.Len(t, s.queries.getDraftPickArgs, 2, "the probe reads the committed pick")
	requireOnlySpendRecorded(t, s.queries.poolFake, s.queries.turnRowsFake, got.CostUsd)
}

// A marker conflict with no committed pick to report fails the activity
// (after counting the spend), so the retry reads the slot again.
func (s *DraftPickTestSuite) TestMarkerConflict_WithoutCommittedPickFails() {
	t := s.T()
	s.llm.responses = []*llm.Response{draftPlayerToolCall(8478402, "McDavid")}
	s.queries.insertDraftPickErr = markerConflict(draftPickMarkerIndex)

	future, err := s.env.ExecuteActivity(s.acts.DraftPick, s.validInput())
	require.Error(t, err)
	assert.Nil(t, future)
	assert.Contains(t, err.Error(), "marker conflict without a committed pick")
	assert.Len(t, s.queries.incrementCostCalls, 1)
}

// Any other unique violation in the commit is still an error: only the
// marker index means the turn is done.
func (s *DraftPickTestSuite) TestCommit_OtherUniqueViolationSurfaces() {
	t := s.T()
	s.llm.responses = []*llm.Response{draftPlayerToolCall(8478402, "McDavid")}
	s.queries.insertRosterErr = markerConflict("sim_rosters_pool_id_player_id_key")

	_, err := s.env.ExecuteActivity(s.acts.DraftPick, s.validInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert roster")
	assert.Empty(t, s.queries.incrementCostCalls)
	assert.Len(t, s.queries.getDraftPickArgs, 1)
}

// The overlapping attempt commits the day while this one is in its
// commit: the marker insert fails before any action or pass row, and the
// result is the idempotency hit's with this attempt's spend.
func (s *ManageRosterTestSuite) TestMarkerConflict_SkipsAlreadyManaged() {
	t := s.T()
	s.llm.responses = []*llm.Response{{
		Content: "Nothing to do today.",
		Usage:   &llm.Usage{PromptTokens: 1000, CompletionTokens: 50, TotalTokens: 1050},
	}}
	s.queries.turnTaken = true
	s.tx.beforeFn = func() {
		s.queries.insertDailyTurnDoneErr = markerConflict(dailyTurnMarkerIndex)
	}

	future, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.NoError(t, err)
	var got ManageRosterResult
	require.NoError(t, future.Get(&got))

	assert.True(t, got.Skipped)
	assert.Equal(t, SkipReasonAlreadyManaged, got.SkipReason)
	assert.Greater(t, got.CostUsd, 0.0, "the model call was billed")
	assert.Len(t, s.queries.insertDailyTurnDoneCalls, 1)
	assert.Empty(t, s.queries.insertPassCalls, "the marker goes first")
	assert.Empty(t, s.queries.insertErrorCalls)
	requireOnlySpendRecorded(t, s.queries.poolFake, s.queries.turnRowsFake, got.CostUsd)
}

// The day marker is the commit's first write.
func (s *ManageRosterTestSuite) TestCommit_DayMarkerFailureStopsBeforeOtherWrites() {
	t := s.T()
	s.llm.responses = []*llm.Response{{Content: "Nothing to do today."}}
	s.queries.insertDailyTurnDoneErr = errors.New("connection reset")

	_, err := s.env.ExecuteActivity(s.acts.ManageRoster, s.validInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert daily turn marker")
	assert.Empty(t, s.queries.insertPassCalls)
	assert.Empty(t, s.queries.incrementCostCalls)
	assert.Empty(t, s.queries.deleteTurnCalls)
}
