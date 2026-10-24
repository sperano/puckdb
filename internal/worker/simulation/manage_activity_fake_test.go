package simulation

import (
	"context"

	"github.com/sperano/puckdb/internal/sqlcdb"
)

// manageQueriesFake is ManageRoster's query group: the day marker, the
// daily action rows and the no-action markers over the shared pool,
// roster, agent and turn fakes.
type manageQueriesFake struct {
	*poolFake
	*rosterFake
	*agentFake
	*turnRowsFake

	// ExistsSimDailyTurnMarker
	existsTxArgs   []sqlcdb.ExistsSimDailyTurnMarkerParams
	existsTxReturn bool
	existsTxErr    error

	// InsertSimTransactionDailyTurnDone
	insertDailyTurnDoneCalls []sqlcdb.InsertSimTransactionDailyTurnDoneParams
	insertDailyTurnDoneErr   error

	// UpdateSimRosterSlot
	updateRosterSlotCalls []sqlcdb.UpdateSimRosterSlotParams
	updateRosterSlotErr   error

	// InsertSimWaiverClaim
	insertWaiverCalls  []sqlcdb.InsertSimWaiverClaimParams
	insertWaiverReturn sqlcdb.SimWaiverClaim
	insertWaiverErr    error

	// InsertSimLineupMove
	insertLineupMoveCalls []sqlcdb.InsertSimLineupMoveParams
	insertLineupMoveErr   error

	// InsertSimTransactionClaim / LineupSet / Pass / Error
	insertClaimCalls     []sqlcdb.InsertSimTransactionClaimParams
	insertClaimReturn    sqlcdb.SimTransaction
	insertClaimErr       error
	insertLineupSetCalls []sqlcdb.InsertSimTransactionLineupSetParams
	// insertLineupSetReturnID auto-populates the .ID by sequence so the
	// subsequent InsertSimLineupMove rows can reference it.
	insertLineupSetReturnID int32
	insertLineupSetErr      error
	insertPassCalls         []sqlcdb.InsertSimTransactionPassParams
	insertPassReturn        sqlcdb.SimTransaction
	insertPassErr           error
	insertErrorCalls        []sqlcdb.InsertSimTransactionErrorParams
	insertErrorReturn       sqlcdb.SimTransaction
	insertErrorErr          error
}

var _ manageRosterQueries = (*manageQueriesFake)(nil)

func newManageQueriesFake() *manageQueriesFake {
	return &manageQueriesFake{
		poolFake:     &poolFake{},
		rosterFake:   &rosterFake{},
		agentFake:    &agentFake{},
		turnRowsFake: &turnRowsFake{},
	}
}

func (s *manageQueriesFake) ExistsSimDailyTurnMarker(_ context.Context, arg sqlcdb.ExistsSimDailyTurnMarkerParams) (bool, error) {
	s.existsTxArgs = append(s.existsTxArgs, arg)
	return s.existsTxReturn, s.existsTxErr
}

func (s *manageQueriesFake) InsertSimTransactionDailyTurnDone(_ context.Context, arg sqlcdb.InsertSimTransactionDailyTurnDoneParams) error {
	s.insertDailyTurnDoneCalls = append(s.insertDailyTurnDoneCalls, arg)
	return s.insertDailyTurnDoneErr
}

func (s *manageQueriesFake) UpdateSimRosterSlot(_ context.Context, arg sqlcdb.UpdateSimRosterSlotParams) error {
	s.updateRosterSlotCalls = append(s.updateRosterSlotCalls, arg)
	return s.updateRosterSlotErr
}

func (s *manageQueriesFake) InsertSimWaiverClaim(_ context.Context, arg sqlcdb.InsertSimWaiverClaimParams) (sqlcdb.SimWaiverClaim, error) {
	s.insertWaiverCalls = append(s.insertWaiverCalls, arg)
	if s.insertWaiverErr != nil {
		return sqlcdb.SimWaiverClaim{}, s.insertWaiverErr
	}
	return s.insertWaiverReturn, nil
}

func (s *manageQueriesFake) InsertSimLineupMove(_ context.Context, arg sqlcdb.InsertSimLineupMoveParams) error {
	s.insertLineupMoveCalls = append(s.insertLineupMoveCalls, arg)
	return s.insertLineupMoveErr
}

func (s *manageQueriesFake) InsertSimTransactionClaim(_ context.Context, arg sqlcdb.InsertSimTransactionClaimParams) (sqlcdb.SimTransaction, error) {
	s.insertClaimCalls = append(s.insertClaimCalls, arg)
	if s.insertClaimErr != nil {
		return sqlcdb.SimTransaction{}, s.insertClaimErr
	}
	return s.insertClaimReturn, nil
}

func (s *manageQueriesFake) InsertSimTransactionLineupSet(_ context.Context, arg sqlcdb.InsertSimTransactionLineupSetParams) (sqlcdb.SimTransaction, error) {
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

func (s *manageQueriesFake) InsertSimTransactionPass(_ context.Context, arg sqlcdb.InsertSimTransactionPassParams) (sqlcdb.SimTransaction, error) {
	s.insertPassCalls = append(s.insertPassCalls, arg)
	if s.insertPassErr != nil {
		return sqlcdb.SimTransaction{}, s.insertPassErr
	}
	return s.insertPassReturn, nil
}

func (s *manageQueriesFake) InsertSimTransactionError(_ context.Context, arg sqlcdb.InsertSimTransactionErrorParams) (sqlcdb.SimTransaction, error) {
	s.insertErrorCalls = append(s.insertErrorCalls, arg)
	if s.insertErrorErr != nil {
		return sqlcdb.SimTransaction{}, s.insertErrorErr
	}
	return s.insertErrorReturn, nil
}
