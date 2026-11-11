package shared

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

// failureTestSchema holds temporary tables, so each test connection sees only
// its own and nothing outlives it.
const failureTestSchema = `
CREATE TEMP TABLE failure_parent (id int PRIMARY KEY);
CREATE TEMP TABLE failure_child (
	id int PRIMARY KEY,
	parent_id int REFERENCES failure_parent (id),
	code varchar(3) NOT NULL,
	small smallint,
	positive int CHECK (positive > 0)
);
INSERT INTO failure_parent VALUES (1);
INSERT INTO failure_child VALUES (1, 1, 'abc', 1, 1);`

const insertFailureChild = `INSERT INTO failure_child (id, parent_id, code, small, positive) VALUES ($1, $2, $3, $4, $5)`

// TestClassifyActivityError_PostgreSQLErrors checks the classification of the
// errors a real PostgreSQL returns through pgx and ExecBatch.
func TestClassifyActivityError_PostgreSQLErrors(t *testing.T) {
	databaseURL := pgtest.URL(t)

	ctx, cancel := context.WithTimeout(context.Background(), batchTestTimeout)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(conn.Release)
	_, err = conn.Exec(ctx, failureTestSchema)
	require.NoError(t, err)

	const invalidUTF8 = "a\xff"
	tests := []struct {
		name      string
		row       []any
		permanent bool
	}{
		{"string too long", []any{2, 1, "abcd", 1, 1}, true},
		{"invalid UTF-8", []any{2, 1, invalidUTF8, 1, 1}, true},
		{"smallint out of range", []any{2, 1, "abc", "70000", 1}, true},
		{"not null", []any{2, 1, nil, 1, 1}, true},
		{"check", []any{2, 1, "abc", 1, -1}, true},
		{"foreign key: the parent row may still come", []any{2, 2, "abc", 1, 1}, false},
		{"unique: a concurrent writer won", []any{1, 1, "abc", 1, 1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			batch := &pgx.Batch{}
			batch.Queue(insertFailureChild, tt.row...)
			batch.Queue(batchFollowOnSQL)
			err := ExecBatch(pgxBatchExecer{results: conn.SendBatch(ctx, batch), count: batch.Len()},
				func(i int) string { return fmt.Sprintf("row %d", i) })
			err = fmt.Errorf("upsert failure_child: %w", err)

			got := ClassifyActivityError(err)

			if tt.permanent {
				requirePermanent(t, got, ErrTypeRejectedValue)
				return
			}
			assertUnchanged(t, err, got)
			var appErr *temporal.ApplicationError
			require.False(t, errors.As(got, &appErr))
		})
	}
}
