package workflow

import (
	"fmt"

	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// seasonConfig is the configuration FetchSeasonWorkflow and ImportSeasonWorkflow
// snapshot at start: both the Yahoo leagues to process and the day concurrency
// decide which activities get scheduled, so neither may be re-read on replay.
type seasonConfig struct {
	Yahoo          shared.YahooSeasonsSnapshot `json:"yahoo"`
	DayConcurrency int                         `json:"dayConcurrency"`
}

// loadYahooSeasons is the Yahoo seasons loader used by loadSeasonConfig. It is
// a variable so replay tests can swap the configuration between recording and
// replaying a history.
var loadYahooSeasons = shared.LoadYahooSeasonsSnapshot

func loadSeasonConfig() seasonConfig {
	return seasonConfig{
		Yahoo:          loadYahooSeasons(),
		DayConcurrency: shared.ResolveConfigInt(nil, shared.DayConcurrencyParam, nil),
	}
}

// snapshotSeasonConfig records the season configuration in history and fails
// the workflow when the Yahoo seasons file is set but unreadable or malformed.
func snapshotSeasonConfig(ctx workflow.Context) (seasonConfig, error) {
	cfg, err := shared.SnapshotConfig(ctx, loadSeasonConfig)
	if err != nil {
		return cfg, err
	}
	if err := cfg.Yahoo.LoadError(); err != nil {
		return cfg, fmt.Errorf("load yahoo seasons config: %w", err)
	}
	return cfg, nil
}
