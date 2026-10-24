package simulation

import (
	"context"

	"github.com/sperano/puckdb/internal/sqlcdb"
)

// waiverQueriesFake is ProcessWaivers' query group: the pool lock, the
// claims and the priority order over the shared pool, roster and agent
// fakes.
type waiverQueriesFake struct {
	*poolFake
	*rosterFake
	*agentFake

	lockPoolCalls []int32
	lockPoolErr   error
	// lockPoolHook, when set, runs as LockSimPool is called.
	lockPoolHook func()

	// ListSimWaiverClaimsForDuePlayers — cross-day grouped claims.
	listClaimsForDuePlayersArgs []sqlcdb.ListSimWaiverClaimsForDuePlayersParams
	listClaimsForDuePlayersRows []sqlcdb.SimWaiverClaim
	listClaimsForDuePlayersErr  error

	initPriorityCalls    []int32
	initPriorityInserted int64
	initPriorityErr      error
	listPriorityArgs     []int32
	listPriorityRows     []sqlcdb.SimWaiverPriority
	listPriorityErr      error
	updatePriorityCalls  []sqlcdb.UpdateSimWaiverPriorityParams
	updatePriorityErr    error

	resolveClaimCalls []sqlcdb.ResolveSimWaiverClaimParams
	resolveClaimErr   error
	// resolveClaimNotPending lists claim IDs the fake reports as already
	// resolved (0 rows affected).
	resolveClaimNotPending map[int32]bool

	cancelClaimsCalls []sqlcdb.CancelSimWaiverClaimsForPlayerParams
	cancelClaimsErr   error
}

var _ waiverQueries = (*waiverQueriesFake)(nil)

func newWaiverQueriesFake() *waiverQueriesFake {
	return &waiverQueriesFake{poolFake: &poolFake{}, rosterFake: &rosterFake{}, agentFake: &agentFake{}}
}

func (s *waiverQueriesFake) LockSimPool(_ context.Context, id int32) (int32, error) {
	s.lockPoolCalls = append(s.lockPoolCalls, id)
	if s.lockPoolHook != nil {
		s.lockPoolHook()
	}
	if s.lockPoolErr != nil {
		return 0, s.lockPoolErr
	}
	return id, nil
}

func (s *waiverQueriesFake) ListSimWaiverClaimsForDuePlayers(_ context.Context, arg sqlcdb.ListSimWaiverClaimsForDuePlayersParams) ([]sqlcdb.SimWaiverClaim, error) {
	s.listClaimsForDuePlayersArgs = append(s.listClaimsForDuePlayersArgs, arg)
	if s.listClaimsForDuePlayersErr != nil {
		return nil, s.listClaimsForDuePlayersErr
	}
	return s.listClaimsForDuePlayersRows, nil
}

func (s *waiverQueriesFake) InitSimWaiverPriority(_ context.Context, poolID int32) (int64, error) {
	s.initPriorityCalls = append(s.initPriorityCalls, poolID)
	if s.initPriorityErr != nil {
		return 0, s.initPriorityErr
	}
	return s.initPriorityInserted, nil
}

func (s *waiverQueriesFake) ListSimWaiverPriorityByPool(_ context.Context, poolID int32) ([]sqlcdb.SimWaiverPriority, error) {
	s.listPriorityArgs = append(s.listPriorityArgs, poolID)
	if s.listPriorityErr != nil {
		return nil, s.listPriorityErr
	}
	return s.listPriorityRows, nil
}

func (s *waiverQueriesFake) UpdateSimWaiverPriority(_ context.Context, arg sqlcdb.UpdateSimWaiverPriorityParams) error {
	s.updatePriorityCalls = append(s.updatePriorityCalls, arg)
	return s.updatePriorityErr
}

func (s *waiverQueriesFake) ResolveSimWaiverClaim(_ context.Context, arg sqlcdb.ResolveSimWaiverClaimParams) (int64, error) {
	s.resolveClaimCalls = append(s.resolveClaimCalls, arg)
	if s.resolveClaimErr != nil {
		return 0, s.resolveClaimErr
	}
	if s.resolveClaimNotPending[arg.ID] {
		return 0, nil
	}
	return 1, nil
}

func (s *waiverQueriesFake) CancelSimWaiverClaimsForPlayer(_ context.Context, arg sqlcdb.CancelSimWaiverClaimsForPlayerParams) error {
	s.cancelClaimsCalls = append(s.cancelClaimsCalls, arg)
	return s.cancelClaimsErr
}
