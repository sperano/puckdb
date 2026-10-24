// Package nhl holds the Temporal activities that fetch NHL API data into
// the local cache (daily schedule, boxscores, rosters, play-by-play, shift
// charts, standings, Edge tracking, club/season stats) and parse the
// cached files into PostgreSQL. ImportDayIfChanged (import_day_record.go)
// is the per-day skip/import gate recorded in nhl_day_imports.
package nhl

import (
	"context"

	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/evenstrength"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/yahoo"
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

// EvenStrengthTotalsRebuilder is the interface for rebuilding one game's
// even_strength_pair_toi and even_strength_skater_games rows from its stored
// shifts (see evenstrength.Rebuild). All its calls must run in the same
// transaction; see Transactor.
type EvenStrengthTotalsRebuilder interface {
	evenstrength.Store
}

// Transactor runs fn in one database transaction: it commits when fn returns
// nil and rolls back otherwise. fn must use only the rebuilder it is given;
// the pool-scoped Queries would write outside the transaction.
type Transactor interface {
	InTx(ctx context.Context, fn func(EvenStrengthTotalsRebuilder) error) error
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
	SeasonTeamAbbrevsLister
}

// ImportQueries is the composite of the NHL import activities' per-file
// database operations.
type ImportQueries interface {
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
// Tx runs the multi-statement writes that must be atomic, such as the
// per-game even-strength segment rebuild after a shift chart import.
// DayImports records the days ImportDayIfChanged imported. The worker builds
// the struct with NewImportActivities.
type ImportActivities struct {
	Storage    store.Storage
	GobCache   *cache.GobCache
	Queries    ImportQueries
	Tx         Transactor
	DayImports DayImportStore
	Yahoo      *yahoo.ImportActivities
}

// ImportActivityQueries is every pool-scoped query the NHL import activities
// run, the single dependency NewImportActivities spreads over Queries and
// DayImports.
type ImportActivityQueries interface {
	ImportQueries
	DayImportStore
}

var _ ImportActivityQueries = (*sqlcdb.Queries)(nil)

// NewImportActivities returns a copy of deps with Queries and DayImports set
// to queries, so the worker cannot leave one nil; values already set in deps
// are replaced. Tx stays the caller's: it is a transaction runner, not a
// pool-scoped query.
func NewImportActivities(queries ImportActivityQueries, deps ImportActivities) *ImportActivities {
	deps.Queries = queries
	deps.DayImports = queries
	return &deps
}
