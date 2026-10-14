package shared

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	batchTestDatabaseURL = "PUCKDB_TEST_PG_URL"
	batchTestDBMarker    = "test"
	batchTestTimeout     = 10 * time.Second
	batchFailureSQL      = "select 1 / 0"
	batchFollowOnSQL     = "select 1"
)

type pgxBatchExecer struct {
	results pgx.BatchResults
	count   int
}

func (e pgxBatchExecer) Exec(callback func(int, error)) {
	defer func() { _ = e.results.Close() }()
	for i := range e.count {
		_, err := e.results.Exec()
		callback(i, err)
	}
}

func TestPgExecBatchRetainsPrimaryFailure(t *testing.T) {
	databaseURL := os.Getenv(batchTestDatabaseURL)
	if databaseURL == "" {
		t.Skip(batchTestDatabaseURL + " is not set")
	}
	require.Contains(t, strings.ToLower(databaseURL), batchTestDBMarker,
		"%s must name a dedicated test database", batchTestDatabaseURL)

	ctx, cancel := context.WithTimeout(context.Background(), batchTestTimeout)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	batch := &pgx.Batch{}
	batch.Queue(batchFailureSQL)
	batch.Queue(batchFollowOnSQL)

	err = ExecBatch(pgxBatchExecer{results: pool.SendBatch(ctx, batch), count: batch.Len()},
		func(i int) string { return fmt.Sprintf("statement %d", i) })

	require.Error(t, err)
	assert.Contains(t, err.Error(), "division by zero")
	assert.NotContains(t, err.Error(), "and 1 more error")
	var batchErr *BatchError
	require.ErrorAs(t, err, &batchErr)
	assert.Equal(t, []int{0}, batchErr.Indices)
	assert.Len(t, batchErr.Errors, 1)
}
