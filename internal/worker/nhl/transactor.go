package nhl

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// PgxTransactor is the production Transactor backed by a *pgxpool.Pool.
type PgxTransactor struct {
	Pool *pgxpool.Pool
}

// NewPgxTransactor wraps a pgx pool in the Transactor interface.
func NewPgxTransactor(pool *pgxpool.Pool) *PgxTransactor {
	return &PgxTransactor{Pool: pool}
}

// InTx opens a transaction, calls fn with sqlcdb.New(tx), then commits, or
// rolls back if fn returns an error. The deferred Rollback is a no-op once
// the transaction has committed.
func (t *PgxTransactor) InTx(ctx context.Context, fn func(EvenStrengthTotalsRebuilder) error) error {
	tx, err := t.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("nhl import: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(sqlcdb.New(tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("nhl import: commit tx: %w", err)
	}
	return nil
}
