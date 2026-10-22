// Package pgxfixtures provides small pgx test doubles shared by package tests.
package pgxfixtures

import (
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const insertOneCommandTag = "INSERT 0 1"

// BatchResults implements pgx.BatchResults for generated sqlc batch tests.
// Every Exec returns ExecErr; Close always succeeds.
type BatchResults struct {
	ExecErr error
}

func (r *BatchResults) Exec() (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(insertOneCommandTag), r.ExecErr
}

func (r *BatchResults) Query() (pgx.Rows, error) { return nil, nil }
func (r *BatchResults) QueryRow() pgx.Row        { return nil }
func (r *BatchResults) Close() error             { return nil }
