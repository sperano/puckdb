package simulation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sperano/puckdb/internal/sqlcdb"
)

// abortedTurnWriteTimeout bounds recordAbortedTurn's transaction. The
// write runs detached from the activity context, which is usually done
// by then, so this is the only bound on it; one pool-total increment
// and one turn row with its children take far less.
const abortedTurnWriteTimeout = 30 * time.Second

// recordAbortedTurn persists what an abandoned turn already cost: the
// sim_pools.total_llm_cost_usd increment and an errored sim_agent_turns
// row (rounds and tool calls included), in one transaction and nothing
// else. The fantasy-side writes (roster, draft pick, transactions,
// notes, team name) and the idempotency markers stay out, so a retry
// replays the turn from scratch; its turn row then replaces this one
// while the pool total keeps both attempts, as after any other retry.
//
// Callers use it when the activity context ended during the turn
// (StartToClose deadline, worker shutdown, cancellation) and when a
// draft turn ends without a pick. The provider billed every captured
// round either way; without this write the spend would never reach the
// pool total and the cost cap would undercount it.
//
// The write runs on context.WithoutCancel(ctx), bounded by
// abortedTurnWriteTimeout: on a done activity context PgxTransactor.InTx
// would fail at Begin. A turn without a captured round made no model
// call and writes nothing.
//
// t.Header is marked errored; a header not already errored gets
// ErrorKindLLMError and cause as its detail. recordAbortedTurn returns
// cause, joined with the write's error when the write fails, so callers
// return its result directly.
func (a *Activities) recordAbortedTurn(ctx context.Context, t TurnTelemetry, cause error) error {
	if len(t.Captures) == 0 {
		return cause
	}
	if t.Header.Status != TurnStatusErrored {
		t.Header.Status = TurnStatusErrored
		t.Header.ErrorKind = ErrorKindLLMError
		t.Header.ErrorDetail = cause.Error()
	}
	costNum, err := numericFromFloat(t.Header.Spend.CostUsd)
	if err != nil {
		return errors.Join(cause, fmt.Errorf("simulation: encode aborted turn cost: %w", err))
	}

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortedTurnWriteTimeout)
	defer cancel()
	err = a.Tx.InTx(writeCtx, func(q SimQueries) error {
		if _, err := q.IncrementSimPoolLLMCost(writeCtx, sqlcdb.IncrementSimPoolLLMCostParams{
			ID:              t.Header.PoolID,
			TotalLLMCostUSD: costNum,
		}); err != nil {
			return fmt.Errorf("increment llm cost: %w", err)
		}
		_, err := RecordTurnTelemetry(writeCtx, q, t)
		return err
	})
	if err != nil {
		return errors.Join(cause, fmt.Errorf("simulation: record aborted turn spend: %w", err))
	}
	return cause
}
