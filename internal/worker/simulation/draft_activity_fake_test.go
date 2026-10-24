package simulation

import (
	"context"

	"github.com/sperano/puckdb/internal/sqlcdb"
)

// draftQueriesFake is DraftPick's query group: the idempotency probe
// and the draft_pick row over the shared pool, roster, agent and turn
// fakes.
type draftQueriesFake struct {
	*poolFake
	*rosterFake
	*agentFake
	*turnRowsFake

	// GetSimTransactionDraftPick
	getDraftPickArgs   []sqlcdb.GetSimTransactionDraftPickParams
	getDraftPickReturn sqlcdb.SimTransaction
	// getDraftPickErr lets tests script "row not found" via pgx.ErrNoRows
	// (the idempotency probe interprets ErrNoRows as "no prior pick" and
	// proceeds; any other error aborts the activity).
	getDraftPickErr error

	// InsertSimTransactionDraftPick
	insertDraftPickCalls  []sqlcdb.InsertSimTransactionDraftPickParams
	insertDraftPickReturn sqlcdb.SimTransaction
	insertDraftPickErr    error
}

var _ draftPickQueries = (*draftQueriesFake)(nil)

func newDraftQueriesFake() *draftQueriesFake {
	return &draftQueriesFake{
		poolFake:     &poolFake{},
		rosterFake:   &rosterFake{},
		agentFake:    &agentFake{},
		turnRowsFake: &turnRowsFake{},
	}
}

func (s *draftQueriesFake) GetSimTransactionDraftPick(_ context.Context, arg sqlcdb.GetSimTransactionDraftPickParams) (sqlcdb.SimTransaction, error) {
	s.getDraftPickArgs = append(s.getDraftPickArgs, arg)
	if s.getDraftPickErr != nil {
		return sqlcdb.SimTransaction{}, s.getDraftPickErr
	}
	return s.getDraftPickReturn, nil
}

func (s *draftQueriesFake) InsertSimTransactionDraftPick(_ context.Context, arg sqlcdb.InsertSimTransactionDraftPickParams) (sqlcdb.SimTransaction, error) {
	s.insertDraftPickCalls = append(s.insertDraftPickCalls, arg)
	if s.insertDraftPickErr != nil {
		return sqlcdb.SimTransaction{}, s.insertDraftPickErr
	}
	return s.insertDraftPickReturn, nil
}
