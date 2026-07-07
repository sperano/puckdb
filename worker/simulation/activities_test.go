package simulation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// ============================================================================
// Test fixtures shared across activity tests.
// ============================================================================

// stubSimQueries is the test mock for SimQueries. As more activities
// land, more methods get added here. Each method records the args it
// was called with and returns scripted results — pattern lifted from
// worker/nhl tests.
type stubSimQueries struct {
	// ListSimFreeAgentCandidates
	listFAArgs   []sqlcdb.ListSimFreeAgentCandidatesParams
	listFARows   []int64
	listFAErr    error
	listFACalled int

	// ListSimAgentTotalsByPool
	listTotalsArg    int32
	listTotalsRows   []sqlcdb.SimAgentTotal
	listTotalsErr    error
	listTotalsCalled int

	// UpsertSimStanding
	upsertStandingsCalls []sqlcdb.UpsertSimStandingParams
	upsertStandingErr    error

	// GetSimPool
	getPoolArgs   []int32
	getPoolReturn sqlcdb.SimPool
	getPoolErr    error

	// GetSimTransactionDraftPick
	getDraftPickArgs   []sqlcdb.GetSimTransactionDraftPickParams
	getDraftPickReturn sqlcdb.SimTransaction
	// getDraftPickErr lets tests script "row not found" via pgx.ErrNoRows
	// (the idempotency probe interprets ErrNoRows as "no prior pick" and
	// proceeds; any other error aborts the activity).
	getDraftPickErr error

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

	// InsertSimTransactionDraftPick
	insertDraftPickCalls  []sqlcdb.InsertSimTransactionDraftPickParams
	insertDraftPickReturn sqlcdb.SimTransaction
	insertDraftPickErr    error

	// InsertSimTransactionCostCapReached
	insertCostCapCalls  []sqlcdb.InsertSimTransactionCostCapReachedParams
	insertCostCapReturn sqlcdb.SimTransaction
	insertCostCapErr    error

	// IncrementSimPoolLLMCost
	incrementCostCalls  []sqlcdb.IncrementSimPoolLLMCostParams
	incrementCostReturn pgtype.Numeric
	incrementCostErr    error

	// UpdateSimPoolStatus
	updatePoolStatusCalls []sqlcdb.UpdateSimPoolStatusParams
	updatePoolStatusErr   error

	// ExistsSimDailyTurnMarker
	existsTxArgs   []sqlcdb.ExistsSimDailyTurnMarkerParams
	existsTxReturn bool
	existsTxErr    error

	// InsertSimTransactionDailyTurnDone
	insertDailyTurnDoneCalls []sqlcdb.InsertSimTransactionDailyTurnDoneParams
	insertDailyTurnDoneErr   error

	// DeleteSimRoster
	deleteRosterCalls []sqlcdb.DeleteSimRosterParams
	deleteRosterErr   error

	// UpdateSimRosterSlot
	updateRosterSlotCalls []sqlcdb.UpdateSimRosterSlotParams
	updateRosterSlotErr   error

	// UpdateSimAgentNotes
	updateAgentNotesCalls []sqlcdb.UpdateSimAgentNotesParams
	updateAgentNotesErr   error

	// InsertSimWaiverClaim
	insertWaiverCalls  []sqlcdb.InsertSimWaiverClaimParams
	insertWaiverReturn sqlcdb.SimWaiverClaim
	insertWaiverErr    error

	// InsertSimLineupMove
	insertLineupMoveCalls []sqlcdb.InsertSimLineupMoveParams
	insertLineupMoveErr   error

	// InsertSimTransactionAdd / Drop / Claim / LineupSet / Pass / Error
	insertAddCalls       []sqlcdb.InsertSimTransactionAddParams
	insertAddReturn      sqlcdb.SimTransaction
	insertAddErr         error
	insertDropCalls      []sqlcdb.InsertSimTransactionDropParams
	insertDropReturn     sqlcdb.SimTransaction
	insertDropErr        error
	insertClaimCalls     []sqlcdb.InsertSimTransactionClaimParams
	insertClaimReturn    sqlcdb.SimTransaction
	insertClaimErr       error
	insertLineupSetCalls []sqlcdb.InsertSimTransactionLineupSetParams
	// insertLineupSetReturn auto-populates the .ID by sequence so the
	// subsequent InsertSimLineupMove rows can reference it.
	insertLineupSetReturnID int32
	insertLineupSetErr      error
	insertPassCalls         []sqlcdb.InsertSimTransactionPassParams
	insertPassReturn        sqlcdb.SimTransaction
	insertPassErr           error
	insertErrorCalls        []sqlcdb.InsertSimTransactionErrorParams
	insertErrorReturn       sqlcdb.SimTransaction
	insertErrorErr          error

	// CollectDayStats inputs/outputs.
	listDayGamesArgs   []pgtype.Date
	listDayGamesReturn []sqlcdb.ListSimDayGamesRow
	listDayGamesErr    error

	// gameSkaterRows / gameGoalieRows are keyed by game_id so a single
	// stub can serve different responses for different games called in
	// the same activity invocation.
	gameSkaterRows map[int64][]sqlcdb.GetGameSkaterStatsByGameRow
	gameSkaterErr  error
	gameGoalieRows map[int64][]sqlcdb.GetGameGoalieStatsByGameRow
	gameGoalieErr  error

	listActiveRosterArgs []sqlcdb.ListSimActiveRosterByAgentParams
	// listActiveRosterByAgent: agent_id → roster rows.
	listActiveRosterRows map[int32][]sqlcdb.SimRoster
	listActiveRosterErr  error

	upsertDailyPlayerCalls   []sqlcdb.UpsertSimAgentDailyPlayerStatParams
	upsertDailyPlayerErr     error
	aggregateDailyStatsCalls []sqlcdb.AggregateSimAgentDailyStatsParams
	aggregateDailyStatsErr   error
	recomputeCountingCalls   []int32
	recomputeCountingErr     error
	recomputeGAACalls        []int32
	recomputeGAAErr          error

	// ProcessWaiversActivity inputs/outputs.
	listClaimsDueArgs      []sqlcdb.ListSimWaiverClaimsDueParams
	listClaimsDueRows      []sqlcdb.SimWaiverClaim
	listClaimsDueErr       error
	listPriorityArgs       []int32
	listPriorityRows       []sqlcdb.SimWaiverPriority
	listPriorityErr        error
	updatePriorityCalls    []sqlcdb.UpdateSimWaiverPriorityParams
	updatePriorityErr      error
	updateClaimStatusCalls []sqlcdb.UpdateSimWaiverClaimStatusParams
	updateClaimStatusErr   error

	// ListSimWaiverClaimsForDuePlayers — cross-day grouped claims. Falls
	// back to listClaimsDueRows when the dedicated rows aren't set so
	// existing tests that only populate listClaimsDueRows keep working.
	listClaimsForDuePlayersArgs []sqlcdb.ListSimWaiverClaimsForDuePlayersParams
	listClaimsForDuePlayersRows []sqlcdb.SimWaiverClaim
	listClaimsForDuePlayersErr  error

	// CancelSimWaiverClaimsForPlayer
	cancelClaimsCalls []sqlcdb.CancelSimWaiverClaimsForPlayerParams
	cancelClaimsErr   error

	// ListSimWaiverClaimsPendingByAgent — agent_id → pending claims.
	listPendingByAgentArgs []sqlcdb.ListSimWaiverClaimsPendingByAgentParams
	listPendingByAgentRows map[int32][]sqlcdb.SimWaiverClaim
	listPendingByAgentErr  error

	// LoadPoolStateActivity inputs/outputs.
	listAgentsByPoolArgs   []int32
	listAgentsByPoolReturn []sqlcdb.SimAgent
	listAgentsByPoolErr    error
	getSeasonArgs          []int32
	getSeasonReturn        sqlcdb.Season
	getSeasonErr           error

	// LoadDraftCandidatesActivity inputs/outputs.
	clubSkaterRows []sqlcdb.GetClubSkaterStatsBySeasonRow
	clubSkaterErr  error
	clubGoalieRows []sqlcdb.GetClubGoalieStatsBySeasonRow
	clubGoalieErr  error
	getPlayerByID  map[int64]sqlcdb.Player
	getPlayerErr   error

	// GetPlayersByIDs call recording — tests assert the N+1 batching
	// collapses to one call per activity invocation.
	getPlayersByIDsCalls [][]int64

	// BuildManageRosterContextActivity inputs/outputs.
	getSimAgentByID                 map[int32]sqlcdb.SimAgent
	getSimAgentErr                  error
	getSimStandingsLatestDateReturn pgtype.Date
	getSimStandingsLatestDateErr    error
	listSimStandingsByDateRows      []sqlcdb.SimStanding
	listSimStandingsByDateErr       error

	// ListSimRosterByAgent (full roster — different from
	// ListSimActiveRosterByAgent which already has its own stub).
	listFullRosterByAgent map[int32][]sqlcdb.SimRoster
	listFullRosterErr     error

	// ListSimPlayersOnWaivers
	listPlayersOnWaiversArgs   []sqlcdb.ListSimPlayersOnWaiversParams
	listPlayersOnWaiversReturn []sqlcdb.ListSimPlayersOnWaiversRow
	listPlayersOnWaiversErr    error

	// Turn telemetry capture — tests inspect these to assert that the
	// activity wrote the expected telemetry rows.
	insertTurnCalls  []sqlcdb.InsertSimAgentTurnParams
	insertTurnErr    error
	insertTurnNextID int32 // 0 = derive id from call count; >0 = return this and increment
	deleteTurnCalls  []sqlcdb.DeleteSimAgentTurnIdempotentParams
	deleteTurnErr    error

	insertTurnRoundBatches [][]sqlcdb.InsertSimAgentTurnRoundParams
	insertTurnRoundErr     error

	insertToolCallBatches [][]sqlcdb.InsertSimAgentToolCallParams
	insertToolCallErr     error

	insertTurnMessageBatches [][]sqlcdb.InsertSimAgentTurnMessageParams
	insertTurnMessageErr     error

	// GetSimPoolRecordFullMessages: defaults to true (matches the
	// column default); set override to *false to exercise the gated path.
	getRecordFullMessagesArgs     []int32
	getRecordFullMessagesOverride *bool
	getRecordFullMessagesErr      error
}

func (s *stubSimQueries) ListSimFreeAgentCandidates(_ context.Context, arg sqlcdb.ListSimFreeAgentCandidatesParams) ([]int64, error) {
	s.listFACalled++
	s.listFAArgs = append(s.listFAArgs, arg)
	if s.listFAErr != nil {
		return nil, s.listFAErr
	}
	return s.listFARows, nil
}

func (s *stubSimQueries) ListSimAgentTotalsByPool(_ context.Context, poolID int32) ([]sqlcdb.SimAgentTotal, error) {
	s.listTotalsCalled++
	s.listTotalsArg = poolID
	if s.listTotalsErr != nil {
		return nil, s.listTotalsErr
	}
	return s.listTotalsRows, nil
}

func (s *stubSimQueries) UpsertSimStanding(_ context.Context, arg sqlcdb.UpsertSimStandingParams) error {
	s.upsertStandingsCalls = append(s.upsertStandingsCalls, arg)
	if s.upsertStandingErr != nil {
		return s.upsertStandingErr
	}
	return nil
}

func (s *stubSimQueries) GetSimPool(_ context.Context, id int32) (sqlcdb.SimPool, error) {
	s.getPoolArgs = append(s.getPoolArgs, id)
	if s.getPoolErr != nil {
		return sqlcdb.SimPool{}, s.getPoolErr
	}
	return s.getPoolReturn, nil
}

func (s *stubSimQueries) GetSimTransactionDraftPick(_ context.Context, arg sqlcdb.GetSimTransactionDraftPickParams) (sqlcdb.SimTransaction, error) {
	s.getDraftPickArgs = append(s.getDraftPickArgs, arg)
	if s.getDraftPickErr != nil {
		return sqlcdb.SimTransaction{}, s.getDraftPickErr
	}
	return s.getDraftPickReturn, nil
}

func (s *stubSimQueries) InsertSimRoster(_ context.Context, arg sqlcdb.InsertSimRosterParams) error {
	s.insertRosterCalls = append(s.insertRosterCalls, arg)
	return s.insertRosterErr
}

func (s *stubSimQueries) ExistsSimRosterPlayer(_ context.Context, arg sqlcdb.ExistsSimRosterPlayerParams) (bool, error) {
	s.existsRosterPlayerArgs = append(s.existsRosterPlayerArgs, arg)
	if s.existsRosterPlayerErr != nil {
		return false, s.existsRosterPlayerErr
	}
	return s.existsRosterPlayerByPlayer[arg.PlayerID], nil
}

func (s *stubSimQueries) DeleteSimRosterRows(_ context.Context, arg sqlcdb.DeleteSimRosterRowsParams) (int64, error) {
	s.deleteRosterRowsCalls = append(s.deleteRosterRowsCalls, arg)
	if s.deleteRosterRowsErr != nil {
		return 0, s.deleteRosterRowsErr
	}
	if n, ok := s.deleteRosterRowsByPlayer[arg.PlayerID]; ok {
		return n, nil
	}
	return s.deleteRosterRowsDefault, nil
}

func (s *stubSimQueries) InsertSimTransactionDraftPick(_ context.Context, arg sqlcdb.InsertSimTransactionDraftPickParams) (sqlcdb.SimTransaction, error) {
	s.insertDraftPickCalls = append(s.insertDraftPickCalls, arg)
	if s.insertDraftPickErr != nil {
		return sqlcdb.SimTransaction{}, s.insertDraftPickErr
	}
	return s.insertDraftPickReturn, nil
}

func (s *stubSimQueries) InsertSimTransactionCostCapReached(_ context.Context, arg sqlcdb.InsertSimTransactionCostCapReachedParams) (sqlcdb.SimTransaction, error) {
	s.insertCostCapCalls = append(s.insertCostCapCalls, arg)
	if s.insertCostCapErr != nil {
		return sqlcdb.SimTransaction{}, s.insertCostCapErr
	}
	return s.insertCostCapReturn, nil
}

func (s *stubSimQueries) IncrementSimPoolLLMCost(_ context.Context, arg sqlcdb.IncrementSimPoolLLMCostParams) (pgtype.Numeric, error) {
	s.incrementCostCalls = append(s.incrementCostCalls, arg)
	if s.incrementCostErr != nil {
		return pgtype.Numeric{}, s.incrementCostErr
	}
	return s.incrementCostReturn, nil
}

func (s *stubSimQueries) UpdateSimPoolStatus(_ context.Context, arg sqlcdb.UpdateSimPoolStatusParams) error {
	s.updatePoolStatusCalls = append(s.updatePoolStatusCalls, arg)
	return s.updatePoolStatusErr
}

func (s *stubSimQueries) SetSimAgentDraftPosition(_ context.Context, _ sqlcdb.SetSimAgentDraftPositionParams) error {
	return nil
}

func (s *stubSimQueries) SetSimAgentTeamNameAndSummary(_ context.Context, _ sqlcdb.SetSimAgentTeamNameAndSummaryParams) error {
	return nil
}

// --- Turn telemetry stubs. Tests that care about telemetry rows inspect
// these slices; tests that don't (the existing majority) can ignore them.
func (s *stubSimQueries) DeleteSimAgentTurnIdempotent(_ context.Context, arg sqlcdb.DeleteSimAgentTurnIdempotentParams) error {
	s.deleteTurnCalls = append(s.deleteTurnCalls, arg)
	return s.deleteTurnErr
}

func (s *stubSimQueries) InsertSimAgentTurn(_ context.Context, arg sqlcdb.InsertSimAgentTurnParams) (int32, error) {
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

func (s *stubSimQueries) InsertSimAgentTurnRound(_ context.Context, arg []sqlcdb.InsertSimAgentTurnRoundParams) (int64, error) {
	s.insertTurnRoundBatches = append(s.insertTurnRoundBatches, arg)
	return int64(len(arg)), s.insertTurnRoundErr
}

func (s *stubSimQueries) InsertSimAgentToolCall(_ context.Context, arg []sqlcdb.InsertSimAgentToolCallParams) (int64, error) {
	s.insertToolCallBatches = append(s.insertToolCallBatches, arg)
	return int64(len(arg)), s.insertToolCallErr
}

func (s *stubSimQueries) InsertSimAgentTurnMessage(_ context.Context, arg []sqlcdb.InsertSimAgentTurnMessageParams) (int64, error) {
	s.insertTurnMessageBatches = append(s.insertTurnMessageBatches, arg)
	return int64(len(arg)), s.insertTurnMessageErr
}

func (s *stubSimQueries) GetSimPoolRecordFullMessages(_ context.Context, id int32) (bool, error) {
	s.getRecordFullMessagesArgs = append(s.getRecordFullMessagesArgs, id)
	if s.getRecordFullMessagesErr != nil {
		return false, s.getRecordFullMessagesErr
	}
	// Default true so existing tests don't have to set this — matches
	// the migration's column default.
	if s.getRecordFullMessagesOverride != nil {
		return *s.getRecordFullMessagesOverride, nil
	}
	return true, nil
}

func (s *stubSimQueries) ExistsSimDailyTurnMarker(_ context.Context, arg sqlcdb.ExistsSimDailyTurnMarkerParams) (bool, error) {
	s.existsTxArgs = append(s.existsTxArgs, arg)
	return s.existsTxReturn, s.existsTxErr
}

func (s *stubSimQueries) DeleteSimRoster(_ context.Context, arg sqlcdb.DeleteSimRosterParams) error {
	s.deleteRosterCalls = append(s.deleteRosterCalls, arg)
	return s.deleteRosterErr
}

func (s *stubSimQueries) UpdateSimRosterSlot(_ context.Context, arg sqlcdb.UpdateSimRosterSlotParams) error {
	s.updateRosterSlotCalls = append(s.updateRosterSlotCalls, arg)
	return s.updateRosterSlotErr
}

func (s *stubSimQueries) UpdateSimAgentNotes(_ context.Context, arg sqlcdb.UpdateSimAgentNotesParams) error {
	s.updateAgentNotesCalls = append(s.updateAgentNotesCalls, arg)
	return s.updateAgentNotesErr
}

func (s *stubSimQueries) InsertSimWaiverClaim(_ context.Context, arg sqlcdb.InsertSimWaiverClaimParams) (sqlcdb.SimWaiverClaim, error) {
	s.insertWaiverCalls = append(s.insertWaiverCalls, arg)
	if s.insertWaiverErr != nil {
		return sqlcdb.SimWaiverClaim{}, s.insertWaiverErr
	}
	return s.insertWaiverReturn, nil
}

func (s *stubSimQueries) InsertSimLineupMove(_ context.Context, arg sqlcdb.InsertSimLineupMoveParams) error {
	s.insertLineupMoveCalls = append(s.insertLineupMoveCalls, arg)
	return s.insertLineupMoveErr
}

func (s *stubSimQueries) InsertSimTransactionAdd(_ context.Context, arg sqlcdb.InsertSimTransactionAddParams) (sqlcdb.SimTransaction, error) {
	s.insertAddCalls = append(s.insertAddCalls, arg)
	if s.insertAddErr != nil {
		return sqlcdb.SimTransaction{}, s.insertAddErr
	}
	return s.insertAddReturn, nil
}

func (s *stubSimQueries) InsertSimTransactionDrop(_ context.Context, arg sqlcdb.InsertSimTransactionDropParams) (sqlcdb.SimTransaction, error) {
	s.insertDropCalls = append(s.insertDropCalls, arg)
	if s.insertDropErr != nil {
		return sqlcdb.SimTransaction{}, s.insertDropErr
	}
	return s.insertDropReturn, nil
}

func (s *stubSimQueries) InsertSimTransactionClaim(_ context.Context, arg sqlcdb.InsertSimTransactionClaimParams) (sqlcdb.SimTransaction, error) {
	s.insertClaimCalls = append(s.insertClaimCalls, arg)
	if s.insertClaimErr != nil {
		return sqlcdb.SimTransaction{}, s.insertClaimErr
	}
	return s.insertClaimReturn, nil
}

func (s *stubSimQueries) InsertSimTransactionLineupSet(_ context.Context, arg sqlcdb.InsertSimTransactionLineupSetParams) (sqlcdb.SimTransaction, error) {
	s.insertLineupSetCalls = append(s.insertLineupSetCalls, arg)
	if s.insertLineupSetErr != nil {
		return sqlcdb.SimTransaction{}, s.insertLineupSetErr
	}
	// Auto-increment a synthetic ID so child sim_lineup_moves rows can
	// reference distinct parents in tests that file multiple lineup_set
	// transactions (rare in practice but real for multi-round retries).
	s.insertLineupSetReturnID++
	return sqlcdb.SimTransaction{
		ID:      s.insertLineupSetReturnID,
		PoolID:  arg.PoolID,
		AgentID: arg.AgentID,
		Date:    arg.Date,
		Type:    string(TransactionTypeLineupSet),
	}, nil
}

func (s *stubSimQueries) InsertSimTransactionPass(_ context.Context, arg sqlcdb.InsertSimTransactionPassParams) (sqlcdb.SimTransaction, error) {
	s.insertPassCalls = append(s.insertPassCalls, arg)
	if s.insertPassErr != nil {
		return sqlcdb.SimTransaction{}, s.insertPassErr
	}
	return s.insertPassReturn, nil
}

func (s *stubSimQueries) InsertSimTransactionDailyTurnDone(_ context.Context, arg sqlcdb.InsertSimTransactionDailyTurnDoneParams) error {
	s.insertDailyTurnDoneCalls = append(s.insertDailyTurnDoneCalls, arg)
	return s.insertDailyTurnDoneErr
}

func (s *stubSimQueries) InsertSimTransactionError(_ context.Context, arg sqlcdb.InsertSimTransactionErrorParams) (sqlcdb.SimTransaction, error) {
	s.insertErrorCalls = append(s.insertErrorCalls, arg)
	if s.insertErrorErr != nil {
		return sqlcdb.SimTransaction{}, s.insertErrorErr
	}
	return s.insertErrorReturn, nil
}

func (s *stubSimQueries) ListSimDayGames(_ context.Context, gameDate pgtype.Date) ([]sqlcdb.ListSimDayGamesRow, error) {
	s.listDayGamesArgs = append(s.listDayGamesArgs, gameDate)
	if s.listDayGamesErr != nil {
		return nil, s.listDayGamesErr
	}
	return s.listDayGamesReturn, nil
}

func (s *stubSimQueries) GetGameSkaterStatsByGame(_ context.Context, gameID int64) ([]sqlcdb.GetGameSkaterStatsByGameRow, error) {
	if s.gameSkaterErr != nil {
		return nil, s.gameSkaterErr
	}
	return s.gameSkaterRows[gameID], nil
}

func (s *stubSimQueries) GetGameGoalieStatsByGame(_ context.Context, gameID int64) ([]sqlcdb.GetGameGoalieStatsByGameRow, error) {
	if s.gameGoalieErr != nil {
		return nil, s.gameGoalieErr
	}
	return s.gameGoalieRows[gameID], nil
}

func (s *stubSimQueries) ListSimActiveRosterByAgent(_ context.Context, arg sqlcdb.ListSimActiveRosterByAgentParams) ([]sqlcdb.SimRoster, error) {
	s.listActiveRosterArgs = append(s.listActiveRosterArgs, arg)
	if s.listActiveRosterErr != nil {
		return nil, s.listActiveRosterErr
	}
	return s.listActiveRosterRows[arg.AgentID], nil
}

func (s *stubSimQueries) UpsertSimAgentDailyPlayerStat(_ context.Context, arg sqlcdb.UpsertSimAgentDailyPlayerStatParams) error {
	s.upsertDailyPlayerCalls = append(s.upsertDailyPlayerCalls, arg)
	return s.upsertDailyPlayerErr
}

func (s *stubSimQueries) AggregateSimAgentDailyStats(_ context.Context, arg sqlcdb.AggregateSimAgentDailyStatsParams) error {
	s.aggregateDailyStatsCalls = append(s.aggregateDailyStatsCalls, arg)
	return s.aggregateDailyStatsErr
}

func (s *stubSimQueries) RecomputeSimAgentTotalsCounting(_ context.Context, poolID int32) error {
	s.recomputeCountingCalls = append(s.recomputeCountingCalls, poolID)
	return s.recomputeCountingErr
}

func (s *stubSimQueries) RecomputeSimAgentTotalsGAA(_ context.Context, poolID int32) error {
	s.recomputeGAACalls = append(s.recomputeGAACalls, poolID)
	return s.recomputeGAAErr
}

func (s *stubSimQueries) ListSimWaiverClaimsDue(_ context.Context, arg sqlcdb.ListSimWaiverClaimsDueParams) ([]sqlcdb.SimWaiverClaim, error) {
	s.listClaimsDueArgs = append(s.listClaimsDueArgs, arg)
	if s.listClaimsDueErr != nil {
		return nil, s.listClaimsDueErr
	}
	return s.listClaimsDueRows, nil
}

func (s *stubSimQueries) ListSimWaiverPriorityByPool(_ context.Context, poolID int32) ([]sqlcdb.SimWaiverPriority, error) {
	s.listPriorityArgs = append(s.listPriorityArgs, poolID)
	if s.listPriorityErr != nil {
		return nil, s.listPriorityErr
	}
	return s.listPriorityRows, nil
}

func (s *stubSimQueries) UpdateSimWaiverPriority(_ context.Context, arg sqlcdb.UpdateSimWaiverPriorityParams) error {
	s.updatePriorityCalls = append(s.updatePriorityCalls, arg)
	return s.updatePriorityErr
}

func (s *stubSimQueries) UpdateSimWaiverClaimStatus(_ context.Context, arg sqlcdb.UpdateSimWaiverClaimStatusParams) error {
	s.updateClaimStatusCalls = append(s.updateClaimStatusCalls, arg)
	return s.updateClaimStatusErr
}

func (s *stubSimQueries) ListSimWaiverClaimsForDuePlayers(_ context.Context, arg sqlcdb.ListSimWaiverClaimsForDuePlayersParams) ([]sqlcdb.SimWaiverClaim, error) {
	s.listClaimsForDuePlayersArgs = append(s.listClaimsForDuePlayersArgs, arg)
	if s.listClaimsForDuePlayersErr != nil {
		return nil, s.listClaimsForDuePlayersErr
	}
	if s.listClaimsForDuePlayersRows != nil {
		return s.listClaimsForDuePlayersRows, nil
	}
	// Fallback: existing tests populate only listClaimsDueRows. With no
	// cross-day claims, the grouped query returns the same set.
	return s.listClaimsDueRows, nil
}

func (s *stubSimQueries) CancelSimWaiverClaimsForPlayer(_ context.Context, arg sqlcdb.CancelSimWaiverClaimsForPlayerParams) error {
	s.cancelClaimsCalls = append(s.cancelClaimsCalls, arg)
	return s.cancelClaimsErr
}

func (s *stubSimQueries) ListSimWaiverClaimsPendingByAgent(_ context.Context, arg sqlcdb.ListSimWaiverClaimsPendingByAgentParams) ([]sqlcdb.SimWaiverClaim, error) {
	s.listPendingByAgentArgs = append(s.listPendingByAgentArgs, arg)
	if s.listPendingByAgentErr != nil {
		return nil, s.listPendingByAgentErr
	}
	return s.listPendingByAgentRows[arg.AgentID], nil
}

func (s *stubSimQueries) ListSimAgentsByPool(_ context.Context, poolID int32) ([]sqlcdb.SimAgent, error) {
	s.listAgentsByPoolArgs = append(s.listAgentsByPoolArgs, poolID)
	if s.listAgentsByPoolErr != nil {
		return nil, s.listAgentsByPoolErr
	}
	return s.listAgentsByPoolReturn, nil
}

func (s *stubSimQueries) GetSeason(_ context.Context, id int32) (sqlcdb.Season, error) {
	s.getSeasonArgs = append(s.getSeasonArgs, id)
	if s.getSeasonErr != nil {
		return sqlcdb.Season{}, s.getSeasonErr
	}
	return s.getSeasonReturn, nil
}

func (s *stubSimQueries) GetClubSkaterStatsBySeason(_ context.Context, _ sqlcdb.GetClubSkaterStatsBySeasonParams) ([]sqlcdb.GetClubSkaterStatsBySeasonRow, error) {
	if s.clubSkaterErr != nil {
		return nil, s.clubSkaterErr
	}
	return s.clubSkaterRows, nil
}

func (s *stubSimQueries) GetClubGoalieStatsBySeason(_ context.Context, _ sqlcdb.GetClubGoalieStatsBySeasonParams) ([]sqlcdb.GetClubGoalieStatsBySeasonRow, error) {
	if s.clubGoalieErr != nil {
		return nil, s.clubGoalieErr
	}
	return s.clubGoalieRows, nil
}

// GetPlayersByIDs is the batched stand-in for the old per-ID GetPlayer.
// Mirrors GetPlayer's per-id fallback (a bare {ID: id} row when the
// test hasn't pre-populated getPlayerByID) so existing tests that only
// set getPlayerByID keep working unchanged. Forces p.ID == id on the
// returned row regardless of what's stored in the fixture map — real
// GetPlayersByIDs rows always carry their own id column, but existing
// getPlayerByID fixtures were written for a lookup keyed purely by map
// key (GetPlayer never examined p.ID), so plenty of them leave ID
// zero-valued. loadPlayersByIDs re-keys the batch result by p.ID, so
// this needs to hold for the batching to route rows correctly.
func (s *stubSimQueries) GetPlayersByIDs(_ context.Context, ids []int64) ([]sqlcdb.Player, error) {
	s.getPlayersByIDsCalls = append(s.getPlayersByIDsCalls, ids)
	if s.getPlayerErr != nil {
		return nil, s.getPlayerErr
	}
	out := make([]sqlcdb.Player, 0, len(ids))
	for _, id := range ids {
		p := s.getPlayerByID[id] // zero value when absent, matching GetPlayer's {ID: id} fallback
		p.ID = id
		out = append(out, p)
	}
	return out, nil
}

func (s *stubSimQueries) GetSimAgent(_ context.Context, id int32) (sqlcdb.SimAgent, error) {
	if s.getSimAgentErr != nil {
		return sqlcdb.SimAgent{}, s.getSimAgentErr
	}
	if a, ok := s.getSimAgentByID[id]; ok {
		return a, nil
	}
	return sqlcdb.SimAgent{ID: id}, nil
}

func (s *stubSimQueries) GetSimStandingsLatestDate(_ context.Context, _ int32) (pgtype.Date, error) {
	if s.getSimStandingsLatestDateErr != nil {
		return pgtype.Date{}, s.getSimStandingsLatestDateErr
	}
	return s.getSimStandingsLatestDateReturn, nil
}

func (s *stubSimQueries) ListSimStandingsByDate(_ context.Context, _ sqlcdb.ListSimStandingsByDateParams) ([]sqlcdb.SimStanding, error) {
	if s.listSimStandingsByDateErr != nil {
		return nil, s.listSimStandingsByDateErr
	}
	return s.listSimStandingsByDateRows, nil
}

func (s *stubSimQueries) ListSimRosterByAgent(_ context.Context, arg sqlcdb.ListSimRosterByAgentParams) ([]sqlcdb.SimRoster, error) {
	if s.listFullRosterErr != nil {
		return nil, s.listFullRosterErr
	}
	return s.listFullRosterByAgent[arg.AgentID], nil
}

func (s *stubSimQueries) ListSimPlayersOnWaivers(_ context.Context, arg sqlcdb.ListSimPlayersOnWaiversParams) ([]sqlcdb.ListSimPlayersOnWaiversRow, error) {
	s.listPlayersOnWaiversArgs = append(s.listPlayersOnWaiversArgs, arg)
	if s.listPlayersOnWaiversErr != nil {
		return nil, s.listPlayersOnWaiversErr
	}
	return s.listPlayersOnWaiversReturn, nil
}

// pgDate constructs a pgtype.Date from a "2006-01-02" string —
// avoids manual struct-field gymnastics in every test case.
func pgDate(t require.TestingT, s string) pgtype.Date {
	tm, err := time.Parse("2006-01-02", s)
	require.NoError(t, err)
	return pgtype.Date{Time: tm, Valid: true}
}

// ============================================================================
// ActivitiesTestSuite — uses Temporal's TestActivityEnvironment so
// activity.GetLogger(ctx) works (it panics outside an activity ctx).
// ============================================================================

type ActivitiesTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env     *testsuite.TestActivityEnvironment
	queries *stubSimQueries
	acts    *Activities
}

func (s *ActivitiesTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.queries = &stubSimQueries{}
	s.acts = &Activities{Queries: s.queries}
	s.env.RegisterActivity(s.acts.BuildFreeAgentPool)
	s.env.RegisterActivity(s.acts.UpdateStandings)
}

func TestActivitiesTestSuite(t *testing.T) {
	suite.Run(t, new(ActivitiesTestSuite))
}

// ============================================================================
// BuildFreeAgentPool
// ============================================================================

func (s *ActivitiesTestSuite) TestBuildFreeAgentPool_PassesParamsToQuery() {
	s.queries.listFARows = []int64{1, 2, 3}

	in := BuildFreeAgentPoolInput{
		PoolID: 7, Season: 20242025,
		SimDate:    pgDate(s.T(), "2025-11-15"),
		WaiverDays: 2,
	}
	future, err := s.env.ExecuteActivity(s.acts.BuildFreeAgentPool, in)
	require.NoError(s.T(), err)

	var got BuildFreeAgentPoolResult
	require.NoError(s.T(), future.Get(&got))
	assert.Equal(s.T(), []int64{1, 2, 3}, got.PlayerIDs)

	require.Equal(s.T(), 1, s.queries.listFACalled)
	require.Len(s.T(), s.queries.listFAArgs, 1)
	args := s.queries.listFAArgs[0]
	assert.Equal(s.T(), int32(7), args.PoolID)
	assert.Equal(s.T(), int32(20242025), args.Season)
	assert.Equal(s.T(), int32(2), args.Column4,
		"waiver_days must thread through to the Column4 sqlc param")
	assert.Equal(s.T(), "2025-11-15", args.GameDate.Time.Format("2006-01-02"))
}

func (s *ActivitiesTestSuite) TestBuildFreeAgentPool_PropagatesQueryError() {
	// Temporal's testsuite returns the activity error directly through
	// ExecuteActivity's second return — NOT through future.Get. Pin
	// the error path here so a future "swallow underlying error in
	// the activity layer" change is loud.
	s.queries.listFAErr = errors.New("connection lost")

	_, err := s.env.ExecuteActivity(s.acts.BuildFreeAgentPool, BuildFreeAgentPoolInput{PoolID: 1})
	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "list free agent candidates")
	assert.Contains(s.T(), err.Error(), "connection lost")
}

func (s *ActivitiesTestSuite) TestBuildFreeAgentPool_EmptyResultIsValid() {
	// Empty FA pool is a real state (early-season, before many
	// players have played their first game) — pin that the activity
	// returns an empty result without error rather than treating
	// "no rows" as a problem.
	s.queries.listFARows = []int64{}

	future, err := s.env.ExecuteActivity(s.acts.BuildFreeAgentPool, BuildFreeAgentPoolInput{PoolID: 1})
	require.NoError(s.T(), err)

	var got BuildFreeAgentPoolResult
	require.NoError(s.T(), future.Get(&got))
	assert.Empty(s.T(), got.PlayerIDs)
}

// Sanity check on the Activity struct's zero state — a freshly
// constructed Activities with only Queries populated must not panic
// on read-only activities. The other fields (ProviderConfigs) are
// optional for activities that don't need an LLM client.
func (s *ActivitiesTestSuite) TestActivities_ZeroProviderConfigs_OKForReadOnlyActivity() {
	s.queries.listFARows = []int64{42}
	// s.acts already has nil ProviderConfigs from SetupTest — confirm
	// that BuildFreeAgentPool doesn't dereference it.
	require.Nil(s.T(), s.acts.ProviderConfigs)

	future, err := s.env.ExecuteActivity(s.acts.BuildFreeAgentPool, BuildFreeAgentPoolInput{PoolID: 1})
	require.NoError(s.T(), err)
	var got BuildFreeAgentPoolResult
	assert.NoError(s.T(), future.Get(&got))
}

// ============================================================================
// UpdateStandings
// ============================================================================

// numericFromInt is a tiny test helper for building pgtype.Numeric
// row values without dragging strconv into every test case.
func numericFromInt(t require.TestingT, v int) pgtype.Numeric {
	n, err := numericFromFloat(float64(v))
	require.NoError(t, err)
	return n
}

// totalsRow is a test-only helper for SimAgentTotal construction.
// goalieGA / goalieTOI are 0 unless the row is for the GAA category.
func totalsRow(t require.TestingT, agentID int32, cat string, value int) sqlcdb.SimAgentTotal {
	return sqlcdb.SimAgentTotal{
		PoolID:   1,
		AgentID:  agentID,
		Category: cat,
		Value:    numericFromInt(t, value),
	}
}

func (s *ActivitiesTestSuite) TestUpdateStandings_RanksAndUpsertsAllCategories() {
	// 3 agents, single counting category G. Values 50, 30, 10 →
	// ranks 1, 2, 3 → roto points 3, 2, 1 in a 3-team pool.
	t := s.T()
	s.queries.listTotalsRows = []sqlcdb.SimAgentTotal{
		totalsRow(t, 1, string(CategoryG), 50),
		totalsRow(t, 2, string(CategoryG), 30),
		totalsRow(t, 3, string(CategoryG), 10),
	}

	in := UpdateStandingsInput{PoolID: 1, SimDate: pgDate(t, "2025-11-15")}
	future, err := s.env.ExecuteActivity(s.acts.UpdateStandings, in)
	require.NoError(t, err)

	var got UpdateStandingsResult
	require.NoError(t, future.Get(&got))
	assert.Equal(t, 3, got.StandingsWritten)
	require.Equal(t, 1, s.queries.listTotalsCalled)
	assert.Equal(t, int32(1), s.queries.listTotalsArg)
	require.Len(t, s.queries.upsertStandingsCalls, 3)

	// Verify each upsert carried the right (agent, category, roto_points).
	// Build a lookup keyed by agent_id since map iteration in
	// UpdateStandings can write rows in any order.
	byAgent := map[int32]sqlcdb.UpsertSimStandingParams{}
	for _, c := range s.queries.upsertStandingsCalls {
		assert.Equal(t, int32(1), c.PoolID)
		assert.Equal(t, "G", c.Category)
		byAgent[c.AgentID] = c
	}
	require.Contains(t, byAgent, int32(1))
	require.Contains(t, byAgent, int32(2))
	require.Contains(t, byAgent, int32(3))

	// Spot-check the top scorer's roto_points (should be 3.0 in a 3-team pool).
	rp1, _ := byAgent[1].RotoPoints.Float64Value()
	assert.InDelta(t, 3.0, rp1.Float64, 1e-6, "rank 1 in a 3-team pool earns 3 roto points")

	rp3, _ := byAgent[3].RotoPoints.Float64Value()
	assert.InDelta(t, 1.0, rp3.Float64, 1e-6, "rank 3 (last) earns 1 roto point")
}

// Empty totals — day 1 of the sim, before any games — must be a
// no-op success rather than an error. Pin: no upserts called.
func (s *ActivitiesTestSuite) TestUpdateStandings_EmptyTotalsIsNoOp() {
	s.queries.listTotalsRows = nil

	future, err := s.env.ExecuteActivity(s.acts.UpdateStandings, UpdateStandingsInput{PoolID: 1})
	require.NoError(s.T(), err)
	var got UpdateStandingsResult
	require.NoError(s.T(), future.Get(&got))
	assert.Equal(s.T(), 0, got.StandingsWritten)
	assert.Empty(s.T(), s.queries.upsertStandingsCalls)
}

func (s *ActivitiesTestSuite) TestUpdateStandings_PropagatesListError() {
	s.queries.listTotalsErr = errors.New("query timeout")

	_, err := s.env.ExecuteActivity(s.acts.UpdateStandings, UpdateStandingsInput{PoolID: 1})
	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "list agent totals")
	assert.Contains(s.T(), err.Error(), "query timeout")
	assert.Empty(s.T(), s.queries.upsertStandingsCalls, "no upserts must fire when list fails")
}

func (s *ActivitiesTestSuite) TestUpdateStandings_PropagatesUpsertError() {
	t := s.T()
	s.queries.listTotalsRows = []sqlcdb.SimAgentTotal{
		totalsRow(t, 1, string(CategoryG), 10),
	}
	s.queries.upsertStandingErr = errors.New("constraint violation")

	_, err := s.env.ExecuteActivity(s.acts.UpdateStandings, UpdateStandingsInput{PoolID: 1, SimDate: pgDate(t, "2025-11-15")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upsert standing")
	assert.Contains(t, err.Error(), "constraint violation")
}

// GAA category needs the goalie components (GA, TOI) carried through
// the conversion from sqlc rows to scoring engine input. Pin that
// path so a future "drop the int4 fields" change breaks loudly.
func (s *ActivitiesTestSuite) TestUpdateStandings_GAARoutesThroughGoalieComponents() {
	t := s.T()
	// Two agents tied on raw GAA value but with different goalie
	// components. Agent 1: 60 GA / 18000 TOI = 12 GAA. Agent 2: zero TOI.
	// Per PLAN.md, zero-TOI agent gets the worst rank in GAA.
	s.queries.listTotalsRows = []sqlcdb.SimAgentTotal{
		{
			PoolID: 1, AgentID: 1, Category: string(CategoryGAA),
			Value:            numericFromInt(t, 0), // value column unused for GAA
			GoalieGA:         pgtype.Int4{Int32: 60, Valid: true},
			GoalieTOISeconds: pgtype.Int4{Int32: 18000, Valid: true},
		},
		{
			PoolID: 1, AgentID: 2, Category: string(CategoryGAA),
			Value:            numericFromInt(t, 0),
			GoalieGA:         pgtype.Int4{Int32: 0, Valid: true},
			GoalieTOISeconds: pgtype.Int4{Int32: 0, Valid: true}, // zero TOI
		},
	}

	in := UpdateStandingsInput{PoolID: 1, SimDate: pgDate(t, "2025-11-15")}
	_, err := s.env.ExecuteActivity(s.acts.UpdateStandings, in)
	require.NoError(t, err)
	require.Len(t, s.queries.upsertStandingsCalls, 2)

	byAgent := map[int32]sqlcdb.UpsertSimStandingParams{}
	for _, c := range s.queries.upsertStandingsCalls {
		byAgent[c.AgentID] = c
	}
	rp1, _ := byAgent[1].RotoPoints.Float64Value()
	rp2, _ := byAgent[2].RotoPoints.Float64Value()
	assert.Greater(t, rp1.Float64, rp2.Float64, "non-zero-TOI agent must outrank zero-TOI agent in GAA")
}

// ============================================================================
// BuildManageRosterContext — standings rank inversion + DB-error propagation
// + FA position pre-population (Findings #9, #10).
// ============================================================================

type BuildContextTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env     *testsuite.TestActivityEnvironment
	queries *stubSimQueries
	acts    *Activities
}

func (s *BuildContextTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.queries = &stubSimQueries{}
	s.acts = &Activities{Queries: s.queries}
	s.env.RegisterActivity(s.acts.BuildManageRosterContext)
}

func TestBuildContextTestSuite(t *testing.T) {
	suite.Run(t, new(BuildContextTestSuite))
}

// minimalContextInput is a BuildManageRosterContextInput with no FA/waiver
// players and no standings, suitable as a base for focused tests.
func (s *BuildContextTestSuite) minimalInput() BuildManageRosterContextInput {
	return BuildManageRosterContextInput{
		PoolID:  1,
		AgentID: 7,
		SimDate: pgDate(s.T(), "2025-01-10"),
		PoolConfig: PoolConfig{
			Season:     20242025,
			NumTeams:   6,
			WaiverDays: 2,
			RosterPositions: map[RosterSlot]int{
				SlotC: 2, SlotLW: 2, SlotRW: 2, SlotD: 3, SlotG: 2,
				SlotUtil: 1, SlotBN: 6, SlotIR: 3,
			},
		},
	}
}

// seedStandingRow builds a SimStanding for testing.
func seedStandingRow(agentID int32, cat string, value, rotoPts float64) (sqlcdb.SimStanding, error) {
	v, err := numericFromFloat(value)
	if err != nil {
		return sqlcdb.SimStanding{}, err
	}
	rp, err := numericFromFloat(rotoPts)
	if err != nil {
		return sqlcdb.SimStanding{}, err
	}
	return sqlcdb.SimStanding{AgentID: agentID, Category: cat, Value: v, RotoPoints: rp}, nil
}

// Finding #10 — standings rank must be 1 for the leader. In a 6-team pool
// the category leader earns 6 roto points (the max); rank must be 1, not 6.
func (s *BuildContextTestSuite) TestStandings_RankOneIsLeader() {
	t := s.T()
	// 6 agents, category G, roto points descending (agent 1 leads).
	s.queries.getSimStandingsLatestDateReturn = pgDate(t, "2025-01-10")
	var err error
	s.queries.listSimStandingsByDateRows, err = buildStandingRows(t,
		[]struct {
			agent int32
			pts   float64
		}{
			{1, 6}, {2, 5}, {3, 4}, {4, 3}, {5, 2}, {6, 1},
		},
	)
	require.NoError(t, err)
	s.queries.listAgentsByPoolReturn = []sqlcdb.SimAgent{
		{ID: 1, TeamName: "Sonnet"}, {ID: 2, TeamName: "Haiku"},
		{ID: 3, TeamName: "Opus"}, {ID: 4, TeamName: "Flash"},
		{ID: 5, TeamName: "Gemma"}, {ID: 6, TeamName: "Llama"},
	}

	in := s.minimalInput()
	in.AgentID = 1
	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	require.Len(t, got.DailyPrompt.Standings, 6, "all 6 agents must appear in standings")
	byAgent := make(map[int32]StandingRow, len(got.DailyPrompt.Standings))
	for _, row := range got.DailyPrompt.Standings {
		for _, a := range s.queries.listAgentsByPoolReturn {
			if displayName(a) == row.Agent {
				byAgent[a.ID] = row
			}
		}
	}

	assert.Equal(t, float64(1), byAgent[1].G.Rank,
		"category leader (6 roto pts) must have rank 1, not 6")
	assert.Equal(t, float64(6), byAgent[6].G.Rank,
		"last place (1 roto pt) must have rank 6")
}

// Finding #10 — tie: two agents with the same roto points get the same rank
// and both rank above the agent with fewer points.
func (s *BuildContextTestSuite) TestStandings_TiedRank() {
	t := s.T()
	// 3 agents. Agents 1 and 2 tied at 2.5 pts each (2-way tie at 2nd).
	// Agent 3 leads with 3 pts. Expected: agent 3 → rank 1; agents 1,2 → rank 2.
	s.queries.getSimStandingsLatestDateReturn = pgDate(t, "2025-01-10")
	var err error
	s.queries.listSimStandingsByDateRows, err = buildStandingRows(t,
		[]struct {
			agent int32
			pts   float64
		}{
			{3, 3}, {1, 2.5}, {2, 2.5},
		},
	)
	require.NoError(t, err)
	s.queries.listAgentsByPoolReturn = []sqlcdb.SimAgent{
		{ID: 1, TeamName: "A"}, {ID: 2, TeamName: "B"}, {ID: 3, TeamName: "C"},
	}

	in := s.minimalInput()
	in.PoolConfig.NumTeams = 3
	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	byAgent := make(map[int32]StandingRow, len(got.DailyPrompt.Standings))
	for _, row := range got.DailyPrompt.Standings {
		for _, a := range s.queries.listAgentsByPoolReturn {
			if displayName(a) == row.Agent {
				byAgent[a.ID] = row
			}
		}
	}
	assert.Equal(t, float64(1), byAgent[3].G.Rank, "leader must be rank 1")
	assert.Equal(t, float64(2), byAgent[1].G.Rank, "tied agents must share rank 2")
	assert.Equal(t, float64(2), byAgent[2].G.Rank, "tied agents must share rank 2")
}

// Finding #10 — Value is populated with the raw category stat total.
func (s *BuildContextTestSuite) TestStandings_ValueIsPopulated() {
	t := s.T()
	// Agent 1: 42 goals total (Value), earns 2 roto_points.
	// Agent 2: 10 goals total (Value), earns 1 roto_point.
	row1, err := seedStandingRow(1, string(CategoryG), 42, 2)
	require.NoError(t, err)
	row2, err := seedStandingRow(2, string(CategoryG), 10, 1)
	require.NoError(t, err)

	s.queries.getSimStandingsLatestDateReturn = pgDate(t, "2025-01-10")
	s.queries.listSimStandingsByDateRows = []sqlcdb.SimStanding{row1, row2}
	s.queries.listAgentsByPoolReturn = []sqlcdb.SimAgent{
		{ID: 1, TeamName: "Alpha"}, {ID: 2, TeamName: "Beta"},
	}

	in := s.minimalInput()
	in.PoolConfig.NumTeams = 2
	future, execErr := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, execErr)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	byAgent := make(map[int32]StandingRow, len(got.DailyPrompt.Standings))
	for _, row := range got.DailyPrompt.Standings {
		for _, a := range s.queries.listAgentsByPoolReturn {
			if displayName(a) == row.Agent {
				byAgent[a.ID] = row
			}
		}
	}

	// Value should carry the raw stat total (42 goals for agent 1).
	assert.Equal(t, float64(42), byAgent[1].G.Value,
		"Value must hold the raw category stat total, not roto_points")
	assert.Equal(t, float64(10), byAgent[2].G.Value)
}

// Finding #10 — a transient DB error from GetSimStandingsLatestDate must
// propagate as an activity error, not be silently swallowed as "no standings".
func (s *BuildContextTestSuite) TestStandings_DBErrorPropagates() {
	t := s.T()
	s.queries.getSimStandingsLatestDateErr = errors.New("connection reset by peer")

	_, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, s.minimalInput())
	require.Error(t, err, "transient DB error must surface, not be treated as empty standings")
	assert.Contains(t, err.Error(), "connection reset by peer")
}

// Finding #10 — pgx.ErrNoRows from GetSimStandingsLatestDate means "no
// standings yet" and must not surface as an error (empty slice returned).
func (s *BuildContextTestSuite) TestStandings_NoRowsIsEmpty() {
	t := s.T()
	s.queries.getSimStandingsLatestDateErr = pgx.ErrNoRows

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, s.minimalInput())
	require.NoError(t, err, "pgx.ErrNoRows means no standings yet — must not error")
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))
	assert.Empty(t, got.DailyPrompt.Standings, "no standings yet → empty slice")
}

// Finding #9 — BuildManageRosterContext pre-populates positions for FA
// players so they can be slotted in the same turn they are added.
func (s *BuildContextTestSuite) TestContext_FAPositionsPrePopulated() {
	t := s.T()
	const faPlayerID = int64(8480039)

	// Roster is empty; FA pool has one player (MacKinnon, C position).
	in := s.minimalInput()
	in.FreeAgents = []int64{faPlayerID}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		faPlayerID: {
			ID: faPlayerID, FirstName: "Nathan", LastName: "MacKinnon",
			Position: sqlcdb.NullPlayerPosition{
				PlayerPosition: sqlcdb.PlayerPositionC, Valid: true,
			},
		},
	}

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	pos, found := got.Positions[faPlayerID]
	require.True(t, found, "FA player's position must be in the returned Positions map")
	assert.Equal(t, sqlcdb.PlayerPositionC, pos,
		"pre-populated position must match the player's NHL position")
}

// Pending waiver claims must surface in the daily prompt with player names
// and resolution dates so the agent doesn't attempt a duplicate claim.
func (s *BuildContextTestSuite) TestContext_PendingClaimsInDailyPrompt() {
	t := s.T()
	in := s.minimalInput()
	s.queries.listPendingByAgentRows = map[int32][]sqlcdb.SimWaiverClaim{
		in.AgentID: {{
			ID: 42, PoolID: in.PoolID, AgentID: in.AgentID, PlayerID: 100,
			DropPlayerID: pgtype.Int8{Int64: 200, Valid: true},
			ProcessDate:  pgDate(t, "2025-01-12"),
			Status:       string(WaiverClaimStatusPending),
		}},
	}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		100: {ID: 100, FirstName: "Claimed", LastName: "Winger"},
		200: {ID: 200, FirstName: "Dropped", LastName: "Goalie"},
	}

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	require.Len(t, got.DailyPrompt.PendingClaims, 1)
	claim := got.DailyPrompt.PendingClaims[0]
	assert.Equal(t, "Claimed Winger", claim.Player)
	assert.Equal(t, int64(100), claim.ID)
	assert.Equal(t, "2025-01-12", claim.ResolvesOn)
	assert.Equal(t, "Dropped Goalie", claim.DropPlayer)
	assert.Equal(t, []int64{100}, got.PendingClaims,
		"working-state pending set must stay populated alongside the prompt rows")
}

// buildStandingRows is a test helper that converts a (agent, rotoPoints) list
// into SimStanding rows for the G category, using value=0 as a placeholder
// (value is not exercised by rank tests).
func buildStandingRows(t require.TestingT, entries []struct {
	agent int32
	pts   float64
}) ([]sqlcdb.SimStanding, error) {
	rows := make([]sqlcdb.SimStanding, 0, len(entries))
	for _, e := range entries {
		r, err := seedStandingRow(e.agent, string(CategoryG), 0, e.pts)
		if err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// ============================================================================
// Low-1: AgentIDs zero-pads agents absent from sim_agent_totals so the
// ranking pool size always equals the full roster, not just the agents
// that have stats so far.
// ============================================================================

// Agent 3 has no totals row yet (e.g., all their players had days off).
// With AgentIDs=[1,2,3], agent 3 must be padded with a zero-valued
// row so that the pool size stays 3 and agents 1/2 each earn roto
// points from a three-agent ranking, not a two-agent one.
//
// Higher-is-better (Goals): values 50, 30 → ranks 1, 2 (of 3) → roto
// points 3.0, 2.0 for agents 1 and 2; zero-pad agent 3 earns 1.0.
func (s *ActivitiesTestSuite) TestUpdateStandings_AgentIDsZeroPadsAbsentAgents_HigherIsBetter() {
	t := s.T()
	s.queries.listTotalsRows = []sqlcdb.SimAgentTotal{
		totalsRow(t, 1, string(CategoryG), 50),
		totalsRow(t, 2, string(CategoryG), 30),
		// agent 3 has no stats row
	}
	in := UpdateStandingsInput{
		PoolID:   1,
		SimDate:  pgDate(t, "2025-11-15"),
		AgentIDs: []int32{1, 2, 3},
	}

	future, err := s.env.ExecuteActivity(s.acts.UpdateStandings, in)
	require.NoError(t, err)
	var got UpdateStandingsResult
	require.NoError(t, future.Get(&got))

	// All three agents get a standings row.
	require.Len(t, s.queries.upsertStandingsCalls, 3)
	byAgent := map[int32]sqlcdb.UpsertSimStandingParams{}
	for _, c := range s.queries.upsertStandingsCalls {
		byAgent[c.AgentID] = c
	}
	require.Contains(t, byAgent, int32(3), "absent agent must still receive a standings upsert")

	// Points awarded from a 3-team pool, not a 2-team pool.
	rp1, _ := byAgent[1].RotoPoints.Float64Value()
	rp2, _ := byAgent[2].RotoPoints.Float64Value()
	rp3, _ := byAgent[3].RotoPoints.Float64Value()
	assert.InDelta(t, 3.0, rp1.Float64, 1e-6, "rank 1 of 3 earns 3 roto points")
	assert.InDelta(t, 2.0, rp2.Float64, 1e-6, "rank 2 of 3 earns 2 roto points")
	assert.InDelta(t, 1.0, rp3.Float64, 1e-6, "zero-padded rank 3 of 3 earns 1 roto point")
}

// Lower-is-better (GA): a zero-stat agent has zero goals-against,
// which is the best possible value, so they rank first (or tied first)
// — not last. Verify that the padded zero row earns the top roto points.
func (s *ActivitiesTestSuite) TestUpdateStandings_AgentIDsZeroPadsAbsentAgents_LowerIsBetter() {
	t := s.T()
	// CategoryGA is lower-is-better (fewer goals against = better).
	// Agents 1 and 2 have GA values; agent 3 has no totals row yet.
	// After padding, agent 3's zero GA ranks best and earns the most points.
	s.queries.listTotalsRows = []sqlcdb.SimAgentTotal{
		totalsRow(t, 1, string(CategoryGA), 30),
		totalsRow(t, 2, string(CategoryGA), 20),
		// agent 3 absent → zero GA = best
	}
	in := UpdateStandingsInput{
		PoolID:   1,
		SimDate:  pgDate(t, "2025-11-15"),
		AgentIDs: []int32{1, 2, 3},
	}

	future, err := s.env.ExecuteActivity(s.acts.UpdateStandings, in)
	require.NoError(t, err)
	var got UpdateStandingsResult
	require.NoError(t, future.Get(&got))

	require.Len(t, s.queries.upsertStandingsCalls, 3)
	byAgent := map[int32]sqlcdb.UpsertSimStandingParams{}
	for _, c := range s.queries.upsertStandingsCalls {
		byAgent[c.AgentID] = c
	}
	require.Contains(t, byAgent, int32(3), "absent agent must receive a standings upsert")

	rp3, _ := byAgent[3].RotoPoints.Float64Value()
	rp1, _ := byAgent[1].RotoPoints.Float64Value()
	assert.Greater(t, rp3.Float64, rp1.Float64,
		"zero-GA agent (lower-is-better) must outrank the agent with the highest GA value")
}
