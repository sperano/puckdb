package yahoo

import (
	"context"

	"github.com/sperano/puckdb/sqlcdb"
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
	UpsertYahooTeamSummaryStatBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamSummaryStatBatchParams) *sqlcdb.UpsertYahooTeamSummaryStatBatchBatchResults
	UpsertYahooTeamRosterBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamRosterBatchParams) *sqlcdb.UpsertYahooTeamRosterBatchBatchResults
	UpsertYahooTransactionBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTransactionBatchParams) *sqlcdb.UpsertYahooTransactionBatchBatchResults
	UpsertYahooDraftResultBatch(ctx context.Context, arg []sqlcdb.UpsertYahooDraftResultBatchParams) *sqlcdb.UpsertYahooDraftResultBatchBatchResults
	UpsertYahooMatchupBatch(ctx context.Context, arg []sqlcdb.UpsertYahooMatchupBatchParams) *sqlcdb.UpsertYahooMatchupBatchBatchResults
}

// Queries is a composite interface for all Yahoo import activity database operations.
type Queries interface {
	YahooLeagueUpserter
	YahooTeamUpserter
	YahooDataUpserter
}
