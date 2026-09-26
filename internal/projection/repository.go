package projection

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

type Repository struct {
	pool    *pgxpool.Pool
	queries *sqlcdb.Queries
}

type BuildRequest struct {
	PlayerPool []PoolPlayer
	Categories []LeagueCategory
	Overrides  []Override
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, queries: sqlcdb.New(pool)}
}

// BuildSnapshot loads only regular-season games available by asOf, generates
// a deterministic baseline, and replaces the rows for the same snapshot
// identity atomically. This makes workflow retries idempotent.
func (r *Repository) BuildSnapshot(
	ctx context.Context,
	cfg Config,
	targetSeason int,
	asOf time.Time,
	request BuildRequest,
) (uuid.UUID, Snapshot, error) {
	input, err := r.loadInput(ctx, cfg, targetSeason, asOf, request.PlayerPool, nil)
	if err != nil {
		return uuid.Nil, Snapshot{}, err
	}
	baseline, err := Generate(cfg, input)
	if err != nil {
		return uuid.Nil, Snapshot{}, err
	}
	selected := baseline
	if len(request.Overrides) > 0 {
		input.Overrides = request.Overrides
		selected, err = Generate(cfg, input)
		if err != nil {
			return uuid.Nil, Snapshot{}, err
		}
	}
	if err := ValidateCoverage(selected, request.Categories, request.PlayerPool); err != nil {
		return uuid.Nil, selected, err
	}
	baselineID, err := r.storeSnapshot(ctx, baseline)
	if err != nil {
		return uuid.Nil, Snapshot{}, err
	}
	if len(request.Overrides) == 0 {
		return baselineID, baseline, nil
	}
	selectedID, err := r.storeSnapshot(ctx, selected)
	return selectedID, selected, err
}

func (r *Repository) loadInput(
	ctx context.Context,
	cfg Config,
	targetSeason int,
	asOf time.Time,
	pool []PoolPlayer,
	overrides []Override,
) (Input, error) {
	lowerSeason := historyFloorSeason(targetSeason, cfg.LookbackSeasons)
	cutoff := dateValue(asOf)
	skaters, err := r.queries.ListProjectionSkaterHistory(ctx, sqlcdb.ListProjectionSkaterHistoryParams{
		Season: int32(targetSeason), Season_2: int32(lowerSeason), GameDate: cutoff,
	})
	if err != nil {
		return Input{}, fmt.Errorf("load skater projection history: %w", err)
	}
	goalies, err := r.queries.ListProjectionGoalieHistory(ctx, sqlcdb.ListProjectionGoalieHistoryParams{
		Season: int32(targetSeason), Season_2: int32(lowerSeason), GameDate: cutoff,
		MinimumShutoutToiSeconds: int32(cfg.GoalieShutoutMinTOI),
	})
	if err != nil {
		return Input{}, fmt.Errorf("load goalie projection history: %w", err)
	}
	maxDate, err := r.queries.GetProjectionSourceMaxGameDate(ctx, sqlcdb.GetProjectionSourceMaxGameDateParams{
		Season: int32(targetSeason), Season_2: int32(lowerSeason), GameDate: cutoff,
	})
	if err != nil {
		return Input{}, fmt.Errorf("load projection source date: %w", err)
	}
	return projectionInput(targetSeason, asOf, maxDate, skaters, goalies, pool, overrides), nil
}

func (r *Repository) LoadEvaluationInput(
	ctx context.Context,
	cfg Config,
	targetSeason int,
	projectionAsOf time.Time,
	observedAt time.Time,
) (Input, error) {
	season, err := r.queries.GetSeason(ctx, int32(targetSeason))
	if err != nil {
		return Input{}, fmt.Errorf("load evaluation season: %w", err)
	}
	if err := validateEvaluationWindow(projectionAsOf, observedAt, season); err != nil {
		return Input{}, err
	}
	lowerSeason := historyFloorSeason(targetSeason, cfg.LookbackSeasons)
	skaters, err := r.queries.ListProjectionSkaterEvaluationData(
		ctx,
		sqlcdb.ListProjectionSkaterEvaluationDataParams{
			Season: int32(targetSeason), Season_2: int32(lowerSeason),
		},
	)
	if err != nil {
		return Input{}, fmt.Errorf("load skater evaluation data: %w", err)
	}
	goalies, err := r.queries.ListProjectionGoalieEvaluationData(
		ctx,
		sqlcdb.ListProjectionGoalieEvaluationDataParams{
			Season: int32(targetSeason), Season_2: int32(lowerSeason),
			MinimumShutoutToiSeconds: int32(cfg.GoalieShutoutMinTOI),
		},
	)
	if err != nil {
		return Input{}, fmt.Errorf("load goalie evaluation data: %w", err)
	}
	return evaluationInput(targetSeason, projectionAsOf, observedAt, skaters, goalies), nil
}

func validateEvaluationWindow(projectionAsOf, observedAt time.Time, season sqlcdb.Season) error {
	if projectionAsOf.IsZero() || observedAt.IsZero() {
		return fmt.Errorf("projection and observation times are required")
	}
	if !season.StandingsStart.Valid || !season.StandingsEnd.Valid {
		return fmt.Errorf("target season has no evaluation window")
	}
	if projectionAsOf.After(season.StandingsStart.Time) {
		return fmt.Errorf("projection as-of must not be after target season start")
	}
	if observedAt.Before(season.StandingsEnd.Time) {
		return fmt.Errorf("target season is incomplete at observation time")
	}
	return nil
}

func evaluationInput(
	targetSeason int,
	asOf time.Time,
	observedAt time.Time,
	skaters []sqlcdb.ListProjectionSkaterEvaluationDataRow,
	goalies []sqlcdb.ListProjectionGoalieEvaluationDataRow,
) Input {
	input := Input{
		TargetSeason: targetSeason, AsOf: asOf, ObservedAt: observedAt,
		Skaters: make([]SkaterSeason, 0, len(skaters)),
		Goalies: make([]GoalieSeason, 0, len(goalies)),
	}
	for _, row := range skaters {
		input.Skaters = append(input.Skaters, SkaterSeason{
			PlayerID: row.PlayerID, TeamID: row.TeamID, Season: int(row.Season), Position: string(row.Position),
			GamesPlayed: int(row.GamesPlayed), TOISeconds: int(row.TOISeconds),
			Goals: int(row.Goals), Assists: int(row.Assists), PlusMinus: int(row.PlusMinus),
			PenaltyMinutes: int(row.PenaltyMinutes), PowerPlayPoints: int(row.PowerPlayPoints),
			ShotsOnGoal: int(row.ShotsOnGoal), Hits: int(row.Hits), BlockedShots: int(row.BlockedShots),
		})
	}
	for _, row := range goalies {
		input.Goalies = append(input.Goalies, GoalieSeason{
			PlayerID: row.PlayerID, TeamID: row.TeamID, Season: int(row.Season), GamesPlayed: int(row.GamesPlayed),
			GamesStarted: int(row.GamesStarted), TOISeconds: int(row.TOISeconds),
			Wins: int(row.Wins), Shutouts: int(row.Shutouts), ShotsAgainst: int(row.ShotsAgainst),
			Saves: int(row.Saves), GoalsAgainst: int(row.GoalsAgainst),
		})
	}
	return input
}

func projectionInput(
	targetSeason int,
	asOf time.Time,
	maxDate pgtype.Date,
	skaters []sqlcdb.ListProjectionSkaterHistoryRow,
	goalies []sqlcdb.ListProjectionGoalieHistoryRow,
	pool []PoolPlayer,
	overrides []Override,
) Input {
	input := Input{
		TargetSeason: targetSeason,
		AsOf:         asOf,
		PlayerPool:   pool,
		Overrides:    overrides,
		Skaters:      make([]SkaterSeason, 0, len(skaters)),
		Goalies:      make([]GoalieSeason, 0, len(goalies)),
	}
	if maxDate.Valid {
		input.SourceMaxGameDate = maxDate.Time
	}
	for _, row := range skaters {
		input.Skaters = append(input.Skaters, skaterSeasonFromRow(row))
	}
	for _, row := range goalies {
		input.Goalies = append(input.Goalies, goalieSeasonFromRow(row))
	}
	return input
}

func skaterSeasonFromRow(row sqlcdb.ListProjectionSkaterHistoryRow) SkaterSeason {
	return SkaterSeason{
		PlayerID: row.PlayerID, TeamID: row.TeamID, Season: int(row.Season), Position: string(row.Position),
		GamesPlayed: int(row.GamesPlayed), TOISeconds: int(row.TOISeconds),
		Goals: int(row.Goals), Assists: int(row.Assists), PlusMinus: int(row.PlusMinus),
		PenaltyMinutes: int(row.PenaltyMinutes), PowerPlayPoints: int(row.PowerPlayPoints),
		ShotsOnGoal: int(row.ShotsOnGoal), Hits: int(row.Hits), BlockedShots: int(row.BlockedShots),
	}
}

func goalieSeasonFromRow(row sqlcdb.ListProjectionGoalieHistoryRow) GoalieSeason {
	return GoalieSeason{
		PlayerID: row.PlayerID, TeamID: row.TeamID, Season: int(row.Season), GamesPlayed: int(row.GamesPlayed),
		GamesStarted: int(row.GamesStarted), TOISeconds: int(row.TOISeconds),
		Wins: int(row.Wins), Shutouts: int(row.Shutouts), ShotsAgainst: int(row.ShotsAgainst),
		Saves: int(row.Saves), GoalsAgainst: int(row.GoalsAgainst),
	}
}

func (r *Repository) storeSnapshot(ctx context.Context, snapshot Snapshot) (uuid.UUID, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin projection snapshot transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id, err := StoreSnapshotWith(ctx, r.queries.WithTx(tx), snapshot)
	if err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit projection snapshot: %w", err)
	}
	return id, nil
}

// StoreSnapshotWith writes a snapshot through queries bound to the caller's
// transaction, replacing the rows of an existing snapshot with the same
// identity. Callers that store a snapshot together with other records use it
// to keep both in one transaction.
func StoreSnapshotWith(ctx context.Context, queries *sqlcdb.Queries, snapshot Snapshot) (uuid.UUID, error) {
	row, err := queries.CreateProjectionSnapshot(ctx, snapshotParams(snapshot))
	if err != nil {
		return uuid.Nil, fmt.Errorf("create projection snapshot: %w", err)
	}
	if err := queries.DeleteProjectionPlayersBySnapshot(ctx, row.ID); err != nil {
		return uuid.Nil, fmt.Errorf("replace projection players: %w", err)
	}
	if err := storePlayers(ctx, queries, row.ID, snapshot.Players); err != nil {
		return uuid.Nil, err
	}
	return uuid.UUID(row.ID.Bytes), nil
}

func (r *Repository) StoreEvaluation(ctx context.Context, evaluation Evaluation) (uuid.UUID, error) {
	if err := validateEvaluation(evaluation); err != nil {
		return uuid.Nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin projection evaluation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := r.queries.WithTx(tx)
	row, err := queries.CreateProjectionEvaluation(ctx, sqlcdb.CreateProjectionEvaluationParams{
		ModelVersion: evaluation.ModelVersion, ConfigHash: evaluation.ConfigHash,
		SourceDataHash: evaluation.SourceDataHash, TargetSeason: int32(evaluation.TargetSeason),
		AsOf: timestampValue(evaluation.AsOf), ObservedAt: timestampValue(evaluation.ObservedAt),
		PlayerKind:      string(evaluation.PlayerKind),
		ComparisonModel: evaluation.ComparisonModel,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("create projection evaluation: %w", err)
	}
	if err := queries.DeleteProjectionEvaluationMetrics(ctx, row.ID); err != nil {
		return uuid.Nil, fmt.Errorf("replace projection evaluation metrics: %w", err)
	}
	for _, metric := range evaluation.Metrics {
		err := queries.CreateProjectionEvaluationMetric(ctx, sqlcdb.CreateProjectionEvaluationMetricParams{
			EvaluationID: row.ID, Stat: string(metric.Stat), SampleSize: int32(metric.SampleSize),
			ModelMae: metric.ModelMAE, ModelRmse: metric.ModelRMSE,
			ComparisonMae: metric.ComparisonMAE, ComparisonRmse: metric.ComparisonRMSE,
		})
		if err != nil {
			return uuid.Nil, fmt.Errorf("create projection evaluation metric %q: %w", metric.Stat, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit projection evaluation: %w", err)
	}
	return uuid.UUID(row.ID.Bytes), nil
}

func storePlayers(
	ctx context.Context,
	queries *sqlcdb.Queries,
	snapshotID pgtype.UUID,
	players []PlayerProjection,
) error {
	for _, player := range players {
		if err := queries.CreateProjectionPlayer(ctx, playerParams(snapshotID, player)); err != nil {
			return fmt.Errorf("create projection player %q: %w", player.PlayerKey, err)
		}
		stats := make([]Stat, 0, len(player.Values))
		for stat := range player.Values {
			stats = append(stats, stat)
		}
		slices.Sort(stats)
		for _, stat := range stats {
			if err := queries.CreateProjectionValue(ctx, valueParams(snapshotID, player, stat)); err != nil {
				return fmt.Errorf("create projection value %q/%q: %w", player.PlayerKey, stat, err)
			}
		}
	}
	return nil
}

func snapshotParams(snapshot Snapshot) sqlcdb.CreateProjectionSnapshotParams {
	cfg := snapshot.Config
	return sqlcdb.CreateProjectionSnapshotParams{
		TargetSeason: int32(snapshot.TargetSeason), AsOf: timestampValue(snapshot.AsOf),
		SourceMaxGameDate: dateValue(snapshot.SourceMaxGameDate), ModelVersion: cfg.ModelVersion,
		ConfigHash: configHash(cfg), SourceDataHash: snapshot.SourceDataHash,
		LookbackSeasons: int32(cfg.LookbackSeasons),
		SeasonDecay:     cfg.SeasonDecay, SkaterPriorToiSeconds: cfg.SkaterPriorTOISeconds,
		GoaliePriorShots: cfg.GoaliePriorShots, MaxGames: cfg.MaxGames, IntervalZ: cfg.IntervalZ,
		GoalieShutoutMinToi: int32(cfg.GoalieShutoutMinTOI),
		MinimumUncertainty:  cfg.MinimumUncertainty, MaximumUncertainty: cfg.MaximumUncertainty,
		MinimumHistoryGames: int32(cfg.MinimumHistoryGames),
	}
}

func playerParams(snapshotID pgtype.UUID, player PlayerProjection) sqlcdb.CreateProjectionPlayerParams {
	var playerID pgtype.Int8
	if player.PlayerID != nil {
		playerID = pgtype.Int8{Int64: *player.PlayerID, Valid: true}
	}
	var teamID pgtype.Int8
	if player.TeamID != nil {
		teamID = pgtype.Int8{Int64: *player.TeamID, Valid: true}
	}
	return sqlcdb.CreateProjectionPlayerParams{
		SnapshotID: snapshotID, PlayerKey: player.PlayerKey, PlayerID: playerID, TeamID: teamID,
		PlayerKind: string(player.Kind), Position: player.Position, Source: string(player.Source),
		Provider: player.Provider, ProviderVersion: player.ProviderVersion,
		SourceAsOf:              timestampValue(player.SourceAsOf),
		IncorporatesNewsThrough: timestampValue(player.IncorporatesNewsThrough),
		HistorySeasons:          int32(player.HistorySeasons),
		HistoryGames:            int32(player.HistoryGames), SampleExposure: player.SampleExposure,
		Uncertainty: player.Uncertainty, InsufficientHistory: player.InsufficientHistory,
		MissingStats: statStrings(player.MissingStats),
	}
}

func statStrings(stats []Stat) []string {
	values := make([]string, len(stats))
	for i, stat := range stats {
		values[i] = string(stat)
	}
	return values
}

func valueParams(snapshotID pgtype.UUID, player PlayerProjection, stat Stat) sqlcdb.CreateProjectionValueParams {
	value := player.Values[stat]
	return sqlcdb.CreateProjectionValueParams{
		SnapshotID: snapshotID, PlayerKey: player.PlayerKey, Stat: string(stat),
		Mean: value.Mean, Low: value.Low, High: value.High,
	}
}

func historyFloorSeason(targetSeason, lookback int) int {
	start := seasonStartYear(targetSeason) - lookback
	return start*10_000 + start + 1
}

func timestampValue(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: !value.IsZero()}
}

func dateValue(value time.Time) pgtype.Date {
	return pgtype.Date{Time: value.UTC(), Valid: !value.IsZero()}
}
