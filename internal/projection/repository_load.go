package projection

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// LoadSnapshot reads a stored snapshot back with its players sorted by key.
// It fails when the stored parameters no longer hash to the stored config
// hash, so a reconstructed ranking cannot silently use altered inputs.
func (r *Repository) LoadSnapshot(ctx context.Context, id uuid.UUID) (Snapshot, error) {
	snapshotID := pgtype.UUID{Bytes: id, Valid: true}
	row, err := r.queries.GetProjectionSnapshot(ctx, snapshotID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load projection snapshot %s: %w", id, err)
	}
	players, err := r.queries.ListProjectionPlayers(ctx, snapshotID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load projection players of %s: %w", id, err)
	}
	values, err := r.queries.ListProjectionValues(ctx, snapshotID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load projection values of %s: %w", id, err)
	}
	return snapshotFromRows(row, players, values)
}

func snapshotFromRows(row sqlcdb.ProjectionSnapshot, players []sqlcdb.ProjectionPlayer, values []sqlcdb.ProjectionValue) (Snapshot, error) {
	cfg := Config{
		ModelVersion: row.ModelVersion, LookbackSeasons: int(row.LookbackSeasons), SeasonDecay: row.SeasonDecay,
		SkaterPriorTOISeconds: row.SkaterPriorToiSeconds, GoaliePriorShots: row.GoaliePriorShots,
		GoalieShutoutMinTOI: int(row.GoalieShutoutMinToi), MaxGames: row.MaxGames, IntervalZ: row.IntervalZ,
		MinimumUncertainty: row.MinimumUncertainty, MaximumUncertainty: row.MaximumUncertainty,
		MinimumHistoryGames: int(row.MinimumHistoryGames),
	}
	if configHash(cfg) != row.ConfigHash {
		return Snapshot{}, fmt.Errorf("projection snapshot config no longer matches its stored hash")
	}
	snapshot := Snapshot{
		TargetSeason: int(row.TargetSeason), AsOf: row.AsOf.Time.UTC(),
		SourceDataHash: row.SourceDataHash, Config: cfg, Players: make([]PlayerProjection, 0, len(players)),
	}
	if row.SourceMaxGameDate.Valid {
		snapshot.SourceMaxGameDate = row.SourceMaxGameDate.Time
	}
	byKey := make(map[string]map[Stat]Estimate, len(players))
	for _, value := range values {
		if byKey[value.PlayerKey] == nil {
			byKey[value.PlayerKey] = make(map[Stat]Estimate)
		}
		byKey[value.PlayerKey][Stat(value.Stat)] = Estimate{Mean: value.Mean, Low: value.Low, High: value.High}
	}
	for _, player := range players {
		snapshot.Players = append(snapshot.Players, playerFromRow(player, byKey[player.PlayerKey]))
	}
	slices.SortFunc(snapshot.Players, func(a, b PlayerProjection) int {
		return strings.Compare(a.PlayerKey, b.PlayerKey)
	})
	return snapshot, nil
}

func playerFromRow(row sqlcdb.ProjectionPlayer, values map[Stat]Estimate) PlayerProjection {
	if values == nil {
		values = make(map[Stat]Estimate)
	}
	player := PlayerProjection{
		PlayerKey: row.PlayerKey, Kind: PlayerKind(row.PlayerKind), Position: row.Position,
		Source: Source(row.Source), Provider: row.Provider, ProviderVersion: row.ProviderVersion,
		HistorySeasons: int(row.HistorySeasons), HistoryGames: int(row.HistoryGames),
		SampleExposure: row.SampleExposure, Uncertainty: row.Uncertainty,
		InsufficientHistory: row.InsufficientHistory, Values: values,
	}
	if row.PlayerID.Valid {
		player.PlayerID = playerIDPointer(row.PlayerID.Int64)
	}
	if row.TeamID.Valid {
		player.TeamID = playerIDPointer(row.TeamID.Int64)
	}
	if row.SourceAsOf.Valid {
		player.SourceAsOf = row.SourceAsOf.Time.UTC()
	}
	if row.IncorporatesNewsThrough.Valid {
		player.IncorporatesNewsThrough = row.IncorporatesNewsThrough.Time.UTC()
	}
	for _, stat := range row.MissingStats {
		player.MissingStats = append(player.MissingStats, Stat(stat))
	}
	return player
}
