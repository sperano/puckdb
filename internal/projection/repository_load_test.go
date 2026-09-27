package projection

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

func TestSnapshotFromRowsRestoresStoredSnapshot(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	asOf := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	row := snapshotRow(cfg, asOf)
	players := []sqlcdb.ProjectionPlayer{
		{PlayerKey: "nhl:2", PlayerKind: string(PlayerKindSkater), Position: "C", Source: string(SourceInternal),
			SourceAsOf: timestampValue(asOf), Uncertainty: 0.2, MissingStats: []string{string(StatHits)},
			LinemateObservedPointsPer60: floatValue(2.5), LinemateAveragePointsPer60: floatValue(2),
			LinemateSharedToiSeconds: floatValue(30_000), LinemateAdjustmentFactor: floatValue(0.98)},
		{PlayerKey: "nhl:1", PlayerID: pgtype.Int8{Int64: 1, Valid: true}, TeamID: pgtype.Int8{Int64: 52, Valid: true},
			PlayerKind: string(PlayerKindGoalie), Position: "G", Source: string(SourceImported), Provider: "acme",
			IncorporatesNewsThrough: timestampValue(asOf)},
	}
	values := []sqlcdb.ProjectionValue{{PlayerKey: "nhl:1", Stat: string(StatWins), Mean: 30, Low: 20, High: 40}}

	snapshot, err := snapshotFromRows(row, players, values)
	require.NoError(t, err)
	require.Equal(t, cfg, snapshot.Config)
	require.Equal(t, "nhl:1", snapshot.Players[0].PlayerKey)
	require.Equal(t, int64(52), *snapshot.Players[0].TeamID)
	require.Equal(t, Estimate{Mean: 30, Low: 20, High: 40}, snapshot.Players[0].Values[StatWins])
	require.Equal(t, asOf, snapshot.Players[0].IncorporatesNewsThrough)
	require.Empty(t, snapshot.Players[1].Values)
	require.Equal(t, []Stat{StatHits}, snapshot.Players[1].MissingStats)
	require.Equal(t, &LinemateContext{
		ObservedPointsPer60: 2.5, AveragePointsPer60: 2,
		SharedTOISeconds: 30_000, AdjustmentFactor: 0.98,
	}, snapshot.Players[1].LinemateContext)
}

func TestSnapshotFromRowsRejectsAlteredConfig(t *testing.T) {
	t.Parallel()

	row := snapshotRow(DefaultConfig(), time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC))
	row.SeasonDecay = 0.9
	_, err := snapshotFromRows(row, nil, nil)
	require.ErrorContains(t, err, "no longer matches")
}

func snapshotRow(cfg Config, asOf time.Time) sqlcdb.ProjectionSnapshot {
	params := snapshotParams(Snapshot{TargetSeason: 20262027, AsOf: asOf, SourceDataHash: "hash", Config: cfg})
	return sqlcdb.ProjectionSnapshot{
		TargetSeason: params.TargetSeason, AsOf: params.AsOf, ModelVersion: params.ModelVersion,
		ConfigHash: params.ConfigHash, SourceDataHash: params.SourceDataHash, LookbackSeasons: params.LookbackSeasons,
		SeasonDecay: params.SeasonDecay, SkaterPriorToiSeconds: params.SkaterPriorToiSeconds,
		GoaliePriorShots: params.GoaliePriorShots, GoalieShutoutMinToi: params.GoalieShutoutMinToi,
		MaxGames: params.MaxGames, IntervalZ: params.IntervalZ, MinimumUncertainty: params.MinimumUncertainty,
		MaximumUncertainty: params.MaximumUncertainty, MinimumHistoryGames: params.MinimumHistoryGames,
		LinemateRegressionStrength: params.LinemateRegressionStrength,
	}
}
