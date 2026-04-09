package nhl

import (
	"context"

	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/worker/yahoo"
)

// BoxscoreUpserter is the interface for database operations needed by boxscore import.
type BoxscoreUpserter interface {
	UpsertGame(ctx context.Context, arg sqlcdb.UpsertGameParams) error
	UpsertGameSkaterStatsBatch(ctx context.Context, arg []sqlcdb.UpsertGameSkaterStatsBatchParams) *sqlcdb.UpsertGameSkaterStatsBatchBatchResults
	UpsertGameGoalieStatsBatch(ctx context.Context, arg []sqlcdb.UpsertGameGoalieStatsBatchParams) *sqlcdb.UpsertGameGoalieStatsBatchBatchResults
	UpsertGameBroadcastBatch(ctx context.Context, arg []sqlcdb.UpsertGameBroadcastBatchParams) *sqlcdb.UpsertGameBroadcastBatchBatchResults
}

// PlayerGameLogUpdater defines the interface for updating player game log stats.
type PlayerGameLogUpdater interface {
	UpdateSkaterGameLogStats(ctx context.Context, arg sqlcdb.UpdateSkaterGameLogStatsParams) error
}

// GameStoryUpdater is the interface for database operations needed by game story import.
type GameStoryUpdater interface {
	UpsertGameThreeStar(ctx context.Context, arg sqlcdb.UpsertGameThreeStarParams) error
	UpsertGoalHighlight(ctx context.Context, arg sqlcdb.UpsertGoalHighlightParams) error
	UpsertShootoutAttempt(ctx context.Context, arg sqlcdb.UpsertShootoutAttemptParams) error
	GetTeamIDByAbbrev(ctx context.Context, arg sqlcdb.GetTeamIDByAbbrevParams) (int64, error)
}

// PlayByPlayUpserter is the interface for database operations needed by play-by-play import.
type PlayByPlayUpserter interface {
	UpsertPlayEventBatch(ctx context.Context, arg []sqlcdb.UpsertPlayEventBatchParams) *sqlcdb.UpsertPlayEventBatchBatchResults
}

// ShiftChartUpserter is the interface for database operations needed by shift chart import.
type ShiftChartUpserter interface {
	UpsertShiftBatch(ctx context.Context, arg []sqlcdb.UpsertShiftBatchParams) *sqlcdb.UpsertShiftBatchBatchResults
}

// SeasonSeriesUpserter is the interface for database operations needed by season series import.
type SeasonSeriesUpserter interface {
	UpsertGameOfficial(ctx context.Context, arg sqlcdb.UpsertGameOfficialParams) error
	UpsertGameCoach(ctx context.Context, arg sqlcdb.UpsertGameCoachParams) error
	UpsertGameScratch(ctx context.Context, arg sqlcdb.UpsertGameScratchParams) error
	GetGameTeamIDs(ctx context.Context, id int64) (sqlcdb.GetGameTeamIDsRow, error)
}

// StandingsUpserter is the interface for standings database operations.
type StandingsUpserter interface {
	UpsertStandingsSnapshotBatch(ctx context.Context, arg []sqlcdb.UpsertStandingsSnapshotBatchParams) *sqlcdb.UpsertStandingsSnapshotBatchBatchResults
	GetSeasonTeamAbbrevs(ctx context.Context, season int32) ([]sqlcdb.GetSeasonTeamAbbrevsRow, error)
}

// Queries is a composite interface for all NHL import activity database operations.
type Queries interface {
	BoxscoreUpserter
	PlayerGameLogUpdater
	GameStoryUpdater
	PlayByPlayUpserter
	ShiftChartUpserter
	SeasonSeriesUpserter
	StandingsUpserter
}

// ImportActivities holds dependencies for NHL data import activities.
// These activities read cached data from the filesystem and upsert into the database.
type ImportActivities struct {
	Storage  store.Storage
	GobCache *cache.GobCache
	Queries  Queries
	Yahoo    *yahoo.ImportActivities
}
