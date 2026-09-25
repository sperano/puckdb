package yahoo

import (
	"context"

	"github.com/sperano/puckdb/internal/sqlcdb"
)

// YahooLeagueUpserter is the interface for database operations needed by Yahoo league import.
type YahooLeagueUpserter interface {
	UpsertYahooLeague(ctx context.Context, arg sqlcdb.UpsertYahooLeagueParams) error
	UpsertYahooLeagueRosterPositionBatch(ctx context.Context, arg []sqlcdb.UpsertYahooLeagueRosterPositionBatchParams) *sqlcdb.UpsertYahooLeagueRosterPositionBatchBatchResults
	UpsertYahooLeagueStatCategoryBatch(ctx context.Context, arg []sqlcdb.UpsertYahooLeagueStatCategoryBatchParams) *sqlcdb.UpsertYahooLeagueStatCategoryBatchBatchResults
}

// YahooTeamUpserter is the interface for database operations needed by Yahoo team import.
type YahooTeamUpserter interface {
	UpsertYahooTeamBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamBatchParams) *sqlcdb.UpsertYahooTeamBatchBatchResults
	UpsertYahooTeamManagerBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamManagerBatchParams) *sqlcdb.UpsertYahooTeamManagerBatchBatchResults
}

// YahooDataUpserter defines the interface for batch Yahoo data upsert operations.
type YahooDataUpserter interface {
	UpsertYahooTeamSummaryBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamSummaryBatchParams) *sqlcdb.UpsertYahooTeamSummaryBatchBatchResults
	UpsertYahooTeamRosterBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamRosterBatchParams) *sqlcdb.UpsertYahooTeamRosterBatchBatchResults
	UpsertYahooTransactionBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTransactionBatchParams) *sqlcdb.UpsertYahooTransactionBatchBatchResults
	UpsertYahooTransactionPlayerBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTransactionPlayerBatchParams) *sqlcdb.UpsertYahooTransactionPlayerBatchBatchResults
	UpsertYahooDraftResultBatch(ctx context.Context, arg []sqlcdb.UpsertYahooDraftResultBatchParams) *sqlcdb.UpsertYahooDraftResultBatchBatchResults
	UpsertYahooMatchupBatch(ctx context.Context, arg []sqlcdb.UpsertYahooMatchupBatchParams) *sqlcdb.UpsertYahooMatchupBatchBatchResults
}

// YahooStandInLeagueStore reads and clears league settings so a TEMPORARY
// stand-in league (see config.LeagueMetadataSource) can be written and later
// replaced by the real settings without leaving stale rows.
type YahooStandInLeagueStore interface {
	GetYahooLeague(ctx context.Context, id int32) (sqlcdb.YahooLeague, error)
	DeleteYahooLeagueRosterPositions(ctx context.Context, leagueID int32) error
	DeleteYahooLeagueStatCategories(ctx context.Context, leagueID int32) error
}

// YahooLeagueRuleStore versions league rules for the draft helper.
type YahooLeagueRuleStore interface {
	UpsertYahooLeagueRuleSnapshot(ctx context.Context, arg sqlcdb.UpsertYahooLeagueRuleSnapshotParams) (sqlcdb.UpsertYahooLeagueRuleSnapshotRow, error)
}

// YahooLeaguePlayerStore replaces a league's draftable player pool.
type YahooLeaguePlayerStore interface {
	UpsertYahooLeaguePlayerBatch(ctx context.Context, arg []sqlcdb.UpsertYahooLeaguePlayerBatchParams) *sqlcdb.UpsertYahooLeaguePlayerBatchBatchResults
	DeleteStaleYahooLeaguePlayers(ctx context.Context, arg sqlcdb.DeleteStaleYahooLeaguePlayersParams) (int64, error)
}

// Queries is a composite interface for all Yahoo import activity database operations.
type Queries interface {
	YahooLeagueUpserter
	YahooStandInLeagueStore
	YahooLeagueRuleStore
	YahooLeaguePlayerStore
	YahooTeamUpserter
	YahooDataUpserter
}
