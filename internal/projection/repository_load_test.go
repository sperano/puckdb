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
	cfg.AgingCurve = fitAgingCurve(Input{TargetSeason: 20262027})
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

func TestSnapshotFromRowsRestoresTeamEnvironment(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	env := &TeamEnvironment{
		FromTeamID: 3, FromTeam: "NYR", ToTeamID: 26, ToTeam: "LAK", OtherClubShare: 1,
		Factors: map[Stat]float64{StatGoals: 1.04, StatAssists: 1.04, StatShotsOnGoal: 0.97, StatPowerPlayPoints: 1.02},
	}
	params, err := playerParams(pgtype.UUID{}, PlayerProjection{
		PlayerKey: "nhl:9", Kind: PlayerKindSkater, Position: "LW", Source: SourceInternal, TeamEnvironment: env,
	})
	require.NoError(t, err)
	players := []sqlcdb.ProjectionPlayer{{
		PlayerKey: params.PlayerKey, PlayerKind: params.PlayerKind, Position: params.Position, Source: params.Source,
		SourceAsOf: timestampValue(asOf), TeamEnvironment: params.TeamEnvironment,
	}}

	cfg := DefaultConfig()
	cfg.AgingCurve = fitAgingCurve(Input{TargetSeason: 20262027})
	snapshot, err := snapshotFromRows(snapshotRow(cfg, asOf), players, nil)
	require.NoError(t, err)
	require.Equal(t, env, snapshot.Players[0].TeamEnvironment)
}

// A published nhl-baseline-v4 snapshot has zero team-environment columns;
// its config must still hash to the value stored before those fields
// existed.
func TestSnapshotFromRowsLoadsV4SnapshotWithoutTeamEnvironment(t *testing.T) {
	t.Parallel()

	cfg := versionConfig(AgingModelVersion)
	cfg.AgingCurve = fitAgingCurve(Input{TargetSeason: 20262027})
	row := snapshotRow(cfg, time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC))
	row.ConfigHash = agingConfigHash(cfg)

	snapshot, err := snapshotFromRows(row, nil, nil)
	require.NoError(t, err)
	require.Equal(t, cfg, snapshot.Config)
}

// agingConfigHash hashes a config the way nhl-baseline-v4 did, before the
// team-environment fields existed.
func agingConfigHash(cfg Config) string {
	return hashValue(struct {
		ModelVersion               string
		LookbackSeasons            int
		SeasonDecay                float64
		SkaterPriorTOISeconds      float64
		LinemateRegressionStrength float64
		GoaliePriorShots           float64
		GoalieShutoutMinTOI        int
		MaxGames                   float64
		IntervalZ                  float64
		MinimumUncertainty         float64
		MaximumUncertainty         float64
		MinimumHistoryGames        int
		AgingCurve                 *AgingCurve `json:",omitempty"`
	}{
		cfg.ModelVersion, cfg.LookbackSeasons, cfg.SeasonDecay, cfg.SkaterPriorTOISeconds,
		cfg.LinemateRegressionStrength, cfg.GoaliePriorShots, cfg.GoalieShutoutMinTOI, cfg.MaxGames,
		cfg.IntervalZ, cfg.MinimumUncertainty, cfg.MaximumUncertainty, cfg.MinimumHistoryGames, cfg.AgingCurve,
	})
}

func TestSnapshotFromRowsRejectsAlteredConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.AgingCurve = fitAgingCurve(Input{TargetSeason: 20262027})
	row := snapshotRow(cfg, time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC))
	row.SeasonDecay = 0.9
	_, err := snapshotFromRows(row, nil, nil)
	require.ErrorContains(t, err, "no longer matches")
}

func TestSnapshotFromRowsRestoresPreAgingV2Snapshot(t *testing.T) {
	t.Parallel()

	cfg := versionConfig(FaceoffModelVersion)
	cfg.LinemateRegressionStrength = 0
	row := snapshotRow(cfg, time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC))

	snapshot, err := snapshotFromRows(row, nil, nil)
	require.NoError(t, err)
	require.Equal(t, cfg, snapshot.Config)
}

func TestSnapshotFromRowsRestoresPublishedV3LinemateSnapshot(t *testing.T) {
	t.Parallel()

	cfg := versionConfig(LinemateModelVersion)
	row := snapshotRow(cfg, time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC))

	snapshot, err := snapshotFromRows(row, nil, nil)
	require.NoError(t, err)
	require.Equal(t, cfg, snapshot.Config)
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
		AgingCurve:                 params.AgingCurve,
		TeamEnvironmentPriorGames:  params.TeamEnvironmentPriorGames,
		TeamEnvironmentMaxChange:   params.TeamEnvironmentMaxChange,
		GoalieShareWindowGames:     params.GoalieShareWindowGames,
		GoalieShareHalfLifeGames:   params.GoalieShareHalfLifeGames,
		GoaliePlayoffWeight:        params.GoaliePlayoffWeight,
		GoalieShareBlend:           params.GoalieShareBlend,
	}
}
