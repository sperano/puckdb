package workflow

import (
	"github.com/sperano/puckdb/internal/graph/model"
	"go.temporal.io/sdk/workflow"
)

// ImportSeasonsWorkflow imports season data from cached files into the
// database: Yahoo season metadata (independent of NHL day availability) via
// ImportYahooSeasonWorkflow, then per-day NHL data (boxscores, game stories,
// shifts, and per-day Yahoo team data) via ImportNHLSeasonWorkflow.
func ImportSeasonsWorkflow(ctx workflow.Context, input *model.SeasonsInput) error {
	return runSeasonSyncWorkflow(ctx, input, seasonSyncImport)
}
