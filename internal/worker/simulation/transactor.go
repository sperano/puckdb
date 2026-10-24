package simulation

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// Transactor wraps the "begin → run a query batch → commit (or roll back)"
// dance for activities that mutate multiple sim_* tables atomically.
//
// The simulation activities have a hard requirement that a roster
// mutation, a sim_transactions audit row, and the sim_pools cost
// increment land together or not at all (PLAN.md > "Idempotency >
// Atomic commit"). A Temporal retry must observe either the full set
// or the empty set — partial state would corrupt the idempotency
// pre-flight check on the next attempt.
//
// The InTx callback receives a Q handle scoped to the open pgx.Tx.
// Returning an error rolls back; returning nil commits. The callback
// MUST NOT close over the outer (pool-scoped) Queries handle — using it
// would write outside the transaction and break atomicity.
//
// Q is the query group of the activity that opens the transaction:
// production wiring holds a Transactor[SimQueries] and narrows it per
// activity (narrowTx), and tests inject a stub Transactor that calls fn
// with the same focused fake the activity reads through, so the
// unit-test layer never sees a real DB.
type Transactor[Q any] interface {
	InTx(ctx context.Context, fn func(Q) error) error
}

// narrowTx exposes the transactions of tx as the query group Q. Go has
// no implicit conversion from Transactor[S] to Transactor[Q], even when
// S implements Q, so the caller passes the per-handle conversion as:
// normally the identity, func(q SimQueries) collectQueries { return q }.
// Writing it out also makes the compiler check that S provides every
// method of Q.
func narrowTx[S, Q any](tx Transactor[S], as func(S) Q) Transactor[Q] {
	return narrowedTx[S, Q]{tx: tx, as: as}
}

type narrowedTx[S, Q any] struct {
	tx Transactor[S]
	as func(S) Q
}

func (t narrowedTx[S, Q]) InTx(ctx context.Context, fn func(Q) error) error {
	return t.tx.InTx(ctx, func(q S) error { return fn(t.as(q)) })
}

// *sqlcdb.Queries must keep satisfying every query group: the worker
// wires one as Activities.Queries and PgxTransactor hands one to InTx.
var _ SimQueries = (*sqlcdb.Queries)(nil)

// PgxTransactor is the production Transactor backed by a *pgxpool.Pool.
// Constructed at worker startup in cmd/worker.go and injected into
// the Activities struct.
type PgxTransactor struct {
	Pool *pgxpool.Pool
}

// NewPgxTransactor wraps a pgx pool in the Transactor interface.
func NewPgxTransactor(pool *pgxpool.Pool) *PgxTransactor {
	return &PgxTransactor{Pool: pool}
}

// InTx opens a pgx transaction, calls fn with sqlcdb.New(tx) — which
// structurally satisfies SimQueries because every query group was
// assembled from sqlc's generated method signatures — then commits, or
// rolls back if fn returns an error.
//
// pgx's Rollback is a safe no-op when the transaction has already
// committed (documented in pgx; we rely on it to keep the deferred
// Rollback unconditional).
func (t *PgxTransactor) InTx(ctx context.Context, fn func(SimQueries) error) error {
	tx, err := t.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("simulation: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(sqlcdb.New(tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("simulation: commit tx: %w", err)
	}
	return nil
}
