package projection

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

type linemateKey struct {
	player, teammate int64
	season           int32
}

func TestLinemateContextFromSegmentsMatchesLegacyQuery(t *testing.T) {
	pool := openProjectionTestDB(t)
	seedEquivFixture(t, pool)
	cutoff, err := time.Parse(time.DateOnly, equivCutoffDate)
	require.NoError(t, err)
	params := sqlcdb.ListProjectionSkaterLinemateContextParams{
		MinSeason: equivSeasonA,
		MaxSeason: equivSeasonB,
		GameDate:  dateValue(cutoff),
	}

	got, err := sqlcdb.New(pool).ListProjectionSkaterLinemateContext(context.Background(), params)
	require.NoError(t, err)
	want := queryLegacyLinemateContext(t, pool, params)

	require.NotEmpty(t, want)
	require.Equal(t, want, got, "rows and their order must match the legacy query")

	byKey := make(map[linemateKey]sqlcdb.ListProjectionSkaterLinemateContextRow, len(got))
	for _, row := range got {
		byKey[linemateKey{row.PlayerID - equivPlayerBase, row.TeammateID - equivPlayerBase, row.Season}] = row
	}
	requireLinemateRow(t, byKey, linemateKey{h1, h2, equivSeasonA}, 290, 2, 290)
	requireLinemateRow(t, byKey, linemateKey{h1, h5, equivSeasonA}, 145, 2, 145)
	requireLinemateRow(t, byKey, linemateKey{a1, a2, equivSeasonA}, 290, 1, 290)
	requireLinemateRow(t, byKey, linemateKey{a1, a5, equivSeasonA}, 150, 1, 150)
	requireLinemateRow(t, byKey, linemateKey{a1, a6, equivSeasonA}, 50, 0, 50)
	requireLinemateRow(t, byKey, linemateKey{a2, h1, equivSeasonB}, 120, 1, 120)
	require.NotContains(t, byKey, linemateKey{h1, noStatsPlayer, equivSeasonA})
	require.NotContains(t, byKey, linemateKey{h1, homeGoalie, equivSeasonA})
}

func requireLinemateRow(
	t *testing.T,
	rows map[linemateKey]sqlcdb.ListProjectionSkaterLinemateContextRow,
	key linemateKey,
	sharedTOI, teammatePoints, teammateTOI int64,
) {
	t.Helper()
	row, ok := rows[key]
	require.True(t, ok, "missing row %+v", key)
	require.Equal(t, sharedTOI, row.SharedToiSeconds, "shared TOI %+v", key)
	require.Equal(t, teammatePoints, row.TeammateEvenStrengthPoints, "teammate points %+v", key)
	require.Equal(t, teammateTOI, row.TeammateEvenStrengthToiSeconds, "teammate TOI %+v", key)
}

func queryLegacyLinemateContext(
	t *testing.T,
	pool *pgxpool.Pool,
	params sqlcdb.ListProjectionSkaterLinemateContextParams,
) []sqlcdb.ListProjectionSkaterLinemateContextRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), legacyLinemateContextSQL,
		params.MinSeason, params.MaxSeason, params.GameDate)
	require.NoError(t, err)
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (sqlcdb.ListProjectionSkaterLinemateContextRow, error) {
		var r sqlcdb.ListProjectionSkaterLinemateContextRow
		err := row.Scan(&r.PlayerID, &r.Season, &r.TeammateID, &r.SharedToiSeconds,
			&r.TeammateEvenStrengthPoints, &r.TeammateEvenStrengthToiSeconds)
		return r, err
	})
	require.NoError(t, err)
	return out
}
