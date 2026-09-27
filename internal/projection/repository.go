package projection

import (
	"context"
	"encoding/json"
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
	lowerSeason := projectionLoadFloor(cfg, targetSeason)
	cutoff := dateValue(asOf)
	skaters, err := r.queries.ListProjectionSkaterHistory(ctx, sqlcdb.ListProjectionSkaterHistoryParams{
		Season: int32(targetSeason), Season_2: int32(lowerSeason), GameDate: cutoff,
	})
	if err != nil {
		return Input{}, fmt.Errorf("load skater projection history: %w", err)
	}
	var linemates []sqlcdb.ListProjectionSkaterLinemateContextRow
	if supportsLinemateContext(cfg.ModelVersion) {
		linemates, err = r.queries.ListProjectionSkaterLinemateContext(ctx, sqlcdb.ListProjectionSkaterLinemateContextParams{
			MinSeason: int32(historyFloorSeason(targetSeason, cfg.LookbackSeasons)),
			MaxSeason: int32(historyFloorSeason(targetSeason, 1)), GameDate: cutoff,
		})
		if err != nil {
			return Input{}, fmt.Errorf("load skater linemate context: %w", err)
		}
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
	return projectionInput(targetSeason, asOf, maxDate, skaters, linemates, goalies, pool, overrides), nil
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
	lowerSeason := projectionLoadFloor(cfg, targetSeason)
	skaters, err := r.queries.ListProjectionSkaterEvaluationData(
		ctx,
		sqlcdb.ListProjectionSkaterEvaluationDataParams{
			Season: int32(targetSeason), Season_2: int32(lowerSeason),
		},
	)
	if err != nil {
		return Input{}, fmt.Errorf("load skater evaluation data: %w", err)
	}
	var linemates []sqlcdb.ListProjectionSkaterLinemateContextRow
	if supportsLinemateContext(cfg.ModelVersion) {
		linemates, err = r.queries.ListProjectionSkaterLinemateContext(
			ctx,
			sqlcdb.ListProjectionSkaterLinemateContextParams{
				MinSeason: int32(historyFloorSeason(targetSeason, cfg.LookbackSeasons)),
				MaxSeason: int32(targetSeason), GameDate: dateValue(observedAt),
			},
		)
		if err != nil {
			return Input{}, fmt.Errorf("load skater linemate evaluation data: %w", err)
		}
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
	return evaluationInput(targetSeason, projectionAsOf, observedAt, skaters, linemates, goalies), nil
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
	linemates []sqlcdb.ListProjectionSkaterLinemateContextRow,
	goalies []sqlcdb.ListProjectionGoalieEvaluationDataRow,
) Input {
	input := Input{
		TargetSeason: targetSeason, AsOf: asOf, ObservedAt: observedAt,
		Skaters: make([]SkaterSeason, 0, len(skaters)),
		Goalies: make([]GoalieSeason, 0, len(goalies)),
	}
	for _, row := range skaters {
		input.Skaters = append(input.Skaters, SkaterSeason{
			PlayerID: row.PlayerID, BirthDate: optionalDate(row.BirthDate), TeamID: row.TeamID, Season: int(row.Season), Position: string(row.Position),
			GamesPlayed: int(row.GamesPlayed), TOISeconds: int(row.TOISeconds),
			Goals: int(row.Goals), Assists: int(row.Assists), PlusMinus: int(row.PlusMinus),
			PenaltyMinutes: int(row.PenaltyMinutes), PowerPlayPoints: int(row.PowerPlayPoints),
			ShotsOnGoal: int(row.ShotsOnGoal), Hits: int(row.Hits), BlockedShots: int(row.BlockedShots),
			FaceoffsWon: int(row.FaceoffsWon), FaceoffsLost: int(row.FaceoffsLost),
		})
	}
	applyLinemateContext(input.Skaters, linemates)
	for _, row := range goalies {
		input.Goalies = append(input.Goalies, GoalieSeason{
			PlayerID: row.PlayerID, BirthDate: optionalDate(row.BirthDate), TeamID: row.TeamID, Season: int(row.Season), GamesPlayed: int(row.GamesPlayed),
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
	linemates []sqlcdb.ListProjectionSkaterLinemateContextRow,
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
	applyLinemateContext(input.Skaters, linemates)
	for _, row := range goalies {
		input.Goalies = append(input.Goalies, goalieSeasonFromRow(row))
	}
	return input
}

type skaterSeasonKey struct {
	playerID int64
	season   int
}

type linemateContextTotal struct {
	weightedPoints float64
	sharedSeconds  int64
}

func applyLinemateContext(
	skaters []SkaterSeason,
	linemates []sqlcdb.ListProjectionSkaterLinemateContextRow,
) {
	byPlayerSeason := indexSkaterSeasons(skaters)
	totals := aggregateLinemateRows(byPlayerSeason, linemates)
	for key, total := range totals {
		index := byPlayerSeason[key]
		skaters[index].LinemateTOISeconds = int(total.sharedSeconds)
		skaters[index].LinematePointsPer60 = total.weightedPoints / float64(total.sharedSeconds)
	}
}

func indexSkaterSeasons(skaters []SkaterSeason) map[skaterSeasonKey]int {
	byPlayerSeason := make(map[skaterSeasonKey]int, len(skaters))
	for index := range skaters {
		key := skaterSeasonKey{playerID: skaters[index].PlayerID, season: skaters[index].Season}
		byPlayerSeason[key] = index
	}
	return byPlayerSeason
}

func aggregateLinemateRows(
	byPlayerSeason map[skaterSeasonKey]int,
	linemates []sqlcdb.ListProjectionSkaterLinemateContextRow,
) map[skaterSeasonKey]linemateContextTotal {
	totals := make(map[skaterSeasonKey]linemateContextTotal)
	for _, context := range linemates {
		key := skaterSeasonKey{playerID: context.PlayerID, season: int(context.Season)}
		_, exists := byPlayerSeason[key]
		if !exists {
			continue
		}
		if context.TeammateEvenStrengthToiSeconds <= 0 || context.SharedToiSeconds <= 0 {
			continue
		}
		pointsPer60 := float64(context.TeammateEvenStrengthPoints) /
			float64(context.TeammateEvenStrengthToiSeconds) * secondsPerHour
		total := totals[key]
		total.weightedPoints += float64(context.SharedToiSeconds) * pointsPer60
		total.sharedSeconds += context.SharedToiSeconds
		totals[key] = total
	}
	return totals
}

func skaterSeasonFromRow(row sqlcdb.ListProjectionSkaterHistoryRow) SkaterSeason {
	return SkaterSeason{
		PlayerID: row.PlayerID, BirthDate: optionalDate(row.BirthDate), TeamID: row.TeamID, Season: int(row.Season), Position: string(row.Position),
		GamesPlayed: int(row.GamesPlayed), TOISeconds: int(row.TOISeconds),
		Goals: int(row.Goals), Assists: int(row.Assists), PlusMinus: int(row.PlusMinus),
		PenaltyMinutes: int(row.PenaltyMinutes), PowerPlayPoints: int(row.PowerPlayPoints),
		ShotsOnGoal: int(row.ShotsOnGoal), Hits: int(row.Hits), BlockedShots: int(row.BlockedShots),
		FaceoffsWon: int(row.FaceoffsWon), FaceoffsLost: int(row.FaceoffsLost),
	}
}

func goalieSeasonFromRow(row sqlcdb.ListProjectionGoalieHistoryRow) GoalieSeason {
	return GoalieSeason{
		PlayerID: row.PlayerID, BirthDate: optionalDate(row.BirthDate), TeamID: row.TeamID, Season: int(row.Season), GamesPlayed: int(row.GamesPlayed),
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
	var agingCurve []byte
	if cfg.AgingCurve != nil {
		var err error
		agingCurve, err = json.Marshal(cfg.AgingCurve)
		if err != nil {
			panic(fmt.Sprintf("marshal aging curve: %v", err))
		}
	}
	return sqlcdb.CreateProjectionSnapshotParams{
		TargetSeason: int32(snapshot.TargetSeason), AsOf: timestampValue(snapshot.AsOf),
		SourceMaxGameDate: dateValue(snapshot.SourceMaxGameDate), ModelVersion: cfg.ModelVersion,
		ConfigHash: configHash(cfg), SourceDataHash: snapshot.SourceDataHash,
		LookbackSeasons: int32(cfg.LookbackSeasons),
		SeasonDecay:     cfg.SeasonDecay, SkaterPriorToiSeconds: cfg.SkaterPriorTOISeconds,
		GoaliePriorShots: cfg.GoaliePriorShots, MaxGames: cfg.MaxGames, IntervalZ: cfg.IntervalZ,
		GoalieShutoutMinToi: int32(cfg.GoalieShutoutMinTOI),
		MinimumUncertainty:  cfg.MinimumUncertainty, MaximumUncertainty: cfg.MaximumUncertainty,
		MinimumHistoryGames:        int32(cfg.MinimumHistoryGames),
		LinemateRegressionStrength: cfg.LinemateRegressionStrength,
		AgingCurve:                 agingCurve,
	}
}

func projectionLoadFloor(cfg Config, targetSeason int) int {
	floor := historyFloorSeason(targetSeason, cfg.LookbackSeasons)
	if supportsAgingCurve(cfg.ModelVersion) && AgingTrainingFloorSeason < floor {
		return AgingTrainingFloorSeason
	}
	return floor
}

func optionalDate(value pgtype.Date) time.Time {
	if value.Valid {
		return value.Time.UTC()
	}
	return time.Time{}
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
	var observed, average, sharedTOI, adjustment pgtype.Float8
	if player.LinemateContext != nil {
		observed = floatValue(player.LinemateContext.ObservedPointsPer60)
		average = floatValue(player.LinemateContext.AveragePointsPer60)
		sharedTOI = floatValue(player.LinemateContext.SharedTOISeconds)
		adjustment = floatValue(player.LinemateContext.AdjustmentFactor)
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
		MissingStats:                statStrings(player.MissingStats),
		LinemateObservedPointsPer60: observed, LinemateAveragePointsPer60: average,
		LinemateSharedToiSeconds: sharedTOI, LinemateAdjustmentFactor: adjustment,
	}
}

func floatValue(value float64) pgtype.Float8 {
	return pgtype.Float8{Float64: value, Valid: true}
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
