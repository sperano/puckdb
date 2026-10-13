package nhl

// PostgreSQL-backed coverage for the nhl_day_imports queries behind
// ImportDayIfChanged. It skips unless PUCKDB_TEST_PG_URL names a dedicated
// test database.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

// dayImportTestSeason is far from any real season so the test never touches
// rows another package's tests rely on.
const dayImportTestSeason = 19001901

func TestDayImportQueries_RoundTripAndUpsert(t *testing.T) {
	pool := openTransactorTestDB(t)
	ctx := context.Background()
	cleanup := func() {
		_, err := pool.Exec(context.Background(), `DELETE FROM nhl_day_imports WHERE season = $1`, dayImportTestSeason)
		require.NoError(t, err)
	}
	cleanup()
	t.Cleanup(cleanup)
	q := sqlcdb.New(pool)
	day := time.Date(1900, 10, 15, 0, 0, 0, 0, time.UTC)
	key := sqlcdb.GetNHLDayImportParams{Season: dayImportTestSeason, Date: dayImportDate(day)}

	_, err := q.GetNHLDayImport(ctx, key)
	require.ErrorIs(t, err, pgx.ErrNoRows, "a day never imported has no record")

	record := sqlcdb.UpsertNHLDayImportParams{
		Season: dayImportTestSeason, Date: dayImportDate(day),
		ImporterVersion: DayImporterVersion, InputsHash: "first", InputFiles: recordTestDayFiles,
	}
	require.NoError(t, q.UpsertNHLDayImport(ctx, record))
	got, err := q.GetNHLDayImport(ctx, key)
	require.NoError(t, err)
	require.Equal(t, sqlcdb.GetNHLDayImportRow{ImporterVersion: DayImporterVersion, InputsHash: "first"}, got)

	record.InputsHash = "second"
	record.ImporterVersion = DayImporterVersion + 1
	require.NoError(t, q.UpsertNHLDayImport(ctx, record))
	got, err = q.GetNHLDayImport(ctx, key)
	require.NoError(t, err)
	require.Equal(t, sqlcdb.GetNHLDayImportRow{ImporterVersion: DayImporterVersion + 1, InputsHash: "second"}, got)
}

// ImportActivities.DayImports takes the generated queries in the worker.
var _ DayImportStore = (*sqlcdb.Queries)(nil)
