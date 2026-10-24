package simulation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/pgconv"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// abortedTurnWriteTimeout bounds recordAbortedTurn's transaction. The
// write runs detached from the activity context, which is usually done
// by then, so this is the only bound on it; one pool-total increment
// and one turn row with its children take far less.
const abortedTurnWriteTimeout = 30 * time.Second

// abortedTurnQueries is the aborted-turn write: the pool spend increment
// and a turn row that never replaces one a newer attempt committed.
type abortedTurnQueries interface {
	IncrementSimPoolLLMCost(ctx context.Context, arg sqlcdb.IncrementSimPoolLLMCostParams) (pgtype.Numeric, error)
	InsertSimAgentTurnIfAbsent(ctx context.Context, arg sqlcdb.InsertSimAgentTurnIfAbsentParams) (int32, error)
	turnChildQueries
}

// abortedTurnTx narrows an LLM-driven activity's transactor to the
// aborted-turn write, so the abandoned turn's spend lands through the
// same Transactor as the activity's commit.
func abortedTurnTx[Q abortedTurnQueries](tx Transactor[Q]) Transactor[abortedTurnQueries] {
	return narrowTx(tx, func(q Q) abortedTurnQueries { return q })
}

// abortIfDone is the check an activity runs between its model turn and
// its commit: while ctx is live it returns nil and the commit goes
// ahead; once ctx has ended (StartToClose deadline, worker shutdown,
// cancellation) it records t as an aborted turn and returns the error
// the activity fails with. turn names the turn in that error; tx is the
// activity's own transactor, narrowed to the aborted-turn write.
func abortIfDone(ctx context.Context, tx Transactor[abortedTurnQueries], t TurnTelemetry, turn string) error {
	ctxErr := ctx.Err()
	if ctxErr == nil {
		return nil
	}
	return recordAbortedTurn(ctx, tx, t, fmt.Errorf("simulation: %s aborted: %w", turn, ctxErr))
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
// The turn row is written only while its coordinate is free: Temporal
// may start the retry before this detached write lands, and a late
// write must not replace the turn the retry committed (with its
// applied-transaction links) by an errored one. The spend still counts.
// Of two aborted attempts, the first row stays.
//
// The write runs on context.WithoutCancel(ctx), bounded by
// abortedTurnWriteTimeout: on a done activity context PgxTransactor.InTx
// would fail at Begin. A turn without a captured round made no model
// call and writes nothing. It covers a context that ended by the
// post-turn check (abortIfDone, or chooseDraftPlayer's for a draft);
// one ending after that check, up to and inside the commit transaction,
// still loses the spend, since the commit may have landed and a second
// write could count it twice.
//
// t.Header is marked errored; a header not already errored gets
// ErrorKindLLMError and cause as its detail. recordAbortedTurn returns
// cause, joined with the write's error when the write fails, so callers
// return its result directly. Temporal drops that error after a
// deadline, so a failed write is also logged.
func recordAbortedTurn(ctx context.Context, tx Transactor[abortedTurnQueries], t TurnTelemetry, cause error) error {
	if len(t.Captures) == 0 {
		return cause
	}
	if t.Header.Status != TurnStatusErrored {
		t.Header.Status = TurnStatusErrored
		t.Header.ErrorKind = ErrorKindLLMError
		t.Header.ErrorDetail = cause.Error()
	}
	err := writeAbortedTurn(ctx, tx, t)
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
func writeAbortedTurn(ctx context.Context, tx Transactor[abortedTurnQueries], t TurnTelemetry) error {
	costNum, err := pgconv.NumericFromFloat(t.Header.Spend.CostUsd)
	if err != nil {
		return fmt.Errorf("encode cost: %w", err)
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortedTurnWriteTimeout)
	defer cancel()
	return tx.InTx(writeCtx, func(q abortedTurnQueries) error {
		if _, err := q.IncrementSimPoolLLMCost(writeCtx, sqlcdb.IncrementSimPoolLLMCostParams{
			ID:              t.Header.PoolID,
			TotalLLMCostUSD: costNum,
		}); err != nil {
			return fmt.Errorf("increment llm cost: %w", err)
		}
		// The increment took the pool row lock, which every commit takes
		// before its turn row too, so a concurrent commit has finished
		// (or not started) by the time this insert runs.
		return recordTurnTelemetryIfAbsent(writeCtx, q, t)
	})
}

// recordTurnTelemetryIfAbsent writes t like RecordTurnTelemetry, but
// only when no turn holds its coordinate: it never deletes a row.
func recordTurnTelemetryIfAbsent(ctx context.Context, q abortedTurnQueries, t TurnTelemetry) error {
	parent, err := turnHeaderParams(t)
	if err != nil {
		return err
	}
	turnID, err := q.InsertSimAgentTurnIfAbsent(ctx, sqlcdb.InsertSimAgentTurnIfAbsentParams(parent))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("simulation: insert aborted turn: %w", err)
	}
	return recordTurnChildren(ctx, q, turnID, t)
}
