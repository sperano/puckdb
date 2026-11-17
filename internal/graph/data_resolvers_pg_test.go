package graph

import (
	"context"
	"testing"

	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLatestNHLDataDate(t *testing.T) {
	pool := pgtest.Open(t, "nhl_day_imports")
	resolver := (&Resolver{Queries: sqlcdb.New(pool)}).Query()
	ctx := context.Background()

	got, err := resolver.LatestNHLDataDate(ctx)
	require.NoError(t, err)
	assert.Nil(t, got)

	_, err = pool.Exec(ctx, `
		INSERT INTO nhl_day_imports
			(season, date, importer_version, inputs_hash, input_files)
		VALUES
			(20232024, DATE '2024-04-18', 1, 'oldest-date-first', 1),
			(20242025, DATE '2025-10-07', 1, 'newest-date-middle', 1),
			(20252026, DATE '2025-04-17', 1, 'higher-season-older-date-last', 1)`)
	require.NoError(t, err)

	got, err = resolver.LatestNHLDataDate(ctx)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "2025-10-07", *got)
}
