package simulation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sperano/puckdb/internal/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// abortedTurnWriteTimeout bounds recordAbortedTurn's transaction. The
// write runs detached from the activity context, which is usually done
// by then, so this is the only bound on it; one pool-total increment
// and one turn row with its children take far less.
const abortedTurnWriteTimeout = 30 * time.Second

// abortIfDone is the check an activity runs between its model turn and
// its commit: while ctx is live it returns nil and the commit goes
// ahead; once ctx has ended (StartToClose deadline, worker shutdown,
// cancellation) it records t as an aborted turn and returns the error
// the activity fails with. turn names the turn in that error.
func (a *Activities) abortIfDone(ctx context.Context, t TurnTelemetry, turn string) error {
	ctxErr := ctx.Err()
	if ctxErr == nil {
		return nil
	}
	return a.recordAbortedTurn(ctx, t, fmt.Errorf("simulation: %s aborted: %w", turn, ctxErr))
}

// recordAbortedTurn persists what an abandoned turn already cost: the
// sim_pools.total_llm_cost_usd increment and an errored sim_agent_turns
// row (rounds and tool calls included, accepted-but-uncommitted ones as
// well), in one transaction and nothing else. The fantasy-side writes
// (roster, draft pick, transactions, notes, team name) and the
// idempotency markers stay out, so a retry replays the turn from
// scratch; its turn row then replaces this one while the pool total
// keeps both attempts, as after any other retry.
//
// The write runs on context.WithoutCancel(ctx), bounded by
// abortedTurnWriteTimeout: on a done activity context PgxTransactor.InTx
// would fail at Begin. A turn without a captured round made no model
// call and writes nothing. It covers a context that ended before the
// commit started; one ending inside the commit transaction still loses
// the spend, since the commit may have landed and a second write could
// count it twice.
//
// t.Header is marked errored; a header not already errored gets
// ErrorKindLLMError and cause as its detail. recordAbortedTurn returns
// cause, joined with the write's error when the write fails, so callers
// return its result directly. Temporal drops that error after a
// deadline, so a failed write is also logged.
func (a *Activities) recordAbortedTurn(ctx context.Context, t TurnTelemetry, cause error) error {
	if len(t.Captures) == 0 {
		return cause
	}
	if t.Header.Status != TurnStatusErrored {
		t.Header.Status = TurnStatusErrored
		t.Header.ErrorKind = ErrorKindLLMError
		t.Header.ErrorDetail = cause.Error()
	}
	err := a.writeAbortedTurn(ctx, t)
	if err == nil {
		return cause
	}
	err = fmt.Errorf("simulation: record aborted turn spend: %w", err)
	if activity.IsActivity(ctx) {
		activity.GetLogger(ctx).Error("aborted turn spend not recorded",
			"pool_id", t.Header.PoolID, "agent_id", t.Header.AgentID,
			"cost_usd", t.Header.Spend.CostUsd, "err", err)
	}
	return errors.Join(cause, err)
}

// writeAbortedTurn is recordAbortedTurn's transaction.
func (a *Activities) writeAbortedTurn(ctx context.Context, t TurnTelemetry) error {
	costNum, err := numericFromFloat(t.Header.Spend.CostUsd)
	if err != nil {
		return fmt.Errorf("encode cost: %w", err)
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortedTurnWriteTimeout)
	defer cancel()
	return a.Tx.InTx(writeCtx, func(q SimQueries) error {
		if _, err := q.IncrementSimPoolLLMCost(writeCtx, sqlcdb.IncrementSimPoolLLMCostParams{
			ID:              t.Header.PoolID,
			TotalLLMCostUSD: costNum,
		}); err != nil {
			return fmt.Errorf("increment llm cost: %w", err)
		}
		_, err := RecordTurnTelemetry(writeCtx, q, t)
		return err
	})
}
