package nhl

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// ImportEdgeTeamInput contains parameters for a single-team Edge import activity.
type ImportEdgeTeamInput struct {
	Season     int
	GameType   int
	TeamID     int64
	TeamAbbrev string
}

// EdgeStatsUpserter defines the interface for importing Edge stats to the database.
type EdgeStatsUpserter interface {
	// Skater (upsert via ON CONFLICT to handle parallel activities for traded players)
	UpsertEdgeSkaterStats(ctx context.Context, arg sqlcdb.UpsertEdgeSkaterStatsParams) error
	UpsertEdgeSkaterShotLocation(ctx context.Context, arg sqlcdb.UpsertEdgeSkaterShotLocationParams) error
	UpsertEdgeSkaterSogSummary(ctx context.Context, arg sqlcdb.UpsertEdgeSkaterSogSummaryParams) error

	// Goalie (upsert via ON CONFLICT to handle parallel activities for traded goalies)
	UpsertEdgeGoalieStats(ctx context.Context, arg sqlcdb.UpsertEdgeGoalieStatsParams) error
	UpsertEdgeGoalieShotLocationSummary(ctx context.Context, arg sqlcdb.UpsertEdgeGoalieShotLocationSummaryParams) error
	UpsertEdgeGoalieShotLocation(ctx context.Context, arg sqlcdb.UpsertEdgeGoalieShotLocationParams) error

	// Team (all upserts for consistency)
	UpsertEdgeTeamStats(ctx context.Context, arg sqlcdb.UpsertEdgeTeamStatsParams) error
	UpsertEdgeTeamSogSummary(ctx context.Context, arg sqlcdb.UpsertEdgeTeamSogSummaryParams) error
	UpsertEdgeTeamShotLocation(ctx context.Context, arg sqlcdb.UpsertEdgeTeamShotLocationParams) error
	UpsertEdgeTeamZoneTimeByStrength(ctx context.Context, arg sqlcdb.UpsertEdgeTeamZoneTimeByStrengthParams) error
	UpsertEdgeTeamShotDifferential(ctx context.Context, arg sqlcdb.UpsertEdgeTeamShotDifferentialParams) error

	// Shared
	GetSeasonTeamAbbrevs(ctx context.Context, seasonID int32) ([]sqlcdb.GetSeasonTeamAbbrevsRow, error)
}

// ===== Season-wide import activities =====
//
// Current workflows dispatch the per-team activities below. These season-wide
// entry points stay registered so histories that recorded their activity names
// keep resolving; each one only walks the season's teams through the same
// per-team implementation.

// ImportEdgeSkaters reads cached Edge skater detail data and imports to the database.
func (a *SeasonsActivities) ImportEdgeSkaters(ctx context.Context, input FetchEdgeInput) error {
	return a.importEdgeSeason(ctx, input, edgeKindSkater, a.importEdgeTeamSkaters)
}

// ImportEdgeGoalies reads cached Edge goalie detail data and imports to the database.
func (a *SeasonsActivities) ImportEdgeGoalies(ctx context.Context, input FetchEdgeInput) error {
	return a.importEdgeSeason(ctx, input, edgeKindGoalie, a.importEdgeTeamGoalies)
}

// ImportEdgeTeams reads cached Edge team detail data and imports to the database.
func (a *SeasonsActivities) ImportEdgeTeams(ctx context.Context, input FetchEdgeInput) error {
	return a.importEdgeSeason(ctx, input, edgeKindTeam, a.importEdgeTeamStats)
}

// ImportEdgeTeamZoneTimeDetails reads cached Edge team zone time details and imports
// zone-time-by-strength and shot-differential data to the database.
func (a *SeasonsActivities) ImportEdgeTeamZoneTimeDetails(ctx context.Context, input FetchEdgeInput) error {
	return a.importEdgeSeason(ctx, input, edgeKindTeamZoneTime, a.importEdgeTeamZoneTimeDetails)
}

// ===== Per-team import activities (for fine-grained progress tracking) =====

// ImportEdgeTeam imports Edge team stats for a single team (detail + zone time).
func (a *SeasonsActivities) ImportEdgeTeam(ctx context.Context, input ImportEdgeTeamInput) error {
	scope, team := newEdgeImportScope(input.Season, input.GameType), input.team()
	if _, err := a.importEdgeTeamStats(ctx, scope, team); err != nil {
		return err
	}
	_, err := a.importEdgeTeamZoneTimeDetails(ctx, scope, team)
	return err
}

// ImportEdgeTeamSkaters imports Edge skater stats for a single team's roster.
func (a *SeasonsActivities) ImportEdgeTeamSkaters(ctx context.Context, input ImportEdgeTeamInput) error {
	_, err := a.importEdgeTeamSkaters(ctx, newEdgeImportScope(input.Season, input.GameType), input.team())
	return err
}

// ImportEdgeTeamGoalies imports Edge goalie stats for a single team's roster.
func (a *SeasonsActivities) ImportEdgeTeamGoalies(ctx context.Context, input ImportEdgeTeamInput) error {
	_, err := a.importEdgeTeamGoalies(ctx, newEdgeImportScope(input.Season, input.GameType), input.team())
	return err
}

// team returns the team the input addresses.
func (i ImportEdgeTeamInput) team() edgeTeam {
	return edgeTeam{id: nhlapi.TeamID(i.TeamID), abbrev: i.TeamAbbrev}
}

// pf32 creates a valid pgtype.Float4 from a float64.
func pf32(v float64) pgtype.Float4 {
	return pgtype.Float4{Float32: float32(v), Valid: true}
}

// pi32 creates a valid pgtype.Int4 from an int.
func pi32(v int) pgtype.Int4 {
	return pgtype.Int4{Int32: int32(v), Valid: true}
}
