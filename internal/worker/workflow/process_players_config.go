package workflow

import (
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

// processPlayersConfig is the configuration ProcessPlayersWorkflow snapshots
// once at start: batch size and concurrency decide how many ProcessPlayerBatch
// activities get scheduled per phase, and PlayersPerExec decides how many
// players (and therefore how many ContinueAsNew executions) Phase 2 processes,
// so none of them may be re-read on replay.
type processPlayersConfig struct {
	BatchSize      int `json:"batchSize"`
	Concurrency    int `json:"concurrency"`
	PlayersPerExec int `json:"playersPerExec"`
}

// loadProcessPlayersConfig resolves processPlayersConfig, honouring the same
// input overrides used today for batch size and concurrency. PlayersPerExec
// has no override.
func loadProcessPlayersConfig(logger log.Logger, batchOverride, concurrencyOverride *int) processPlayersConfig {
	return processPlayersConfig{
		BatchSize:      shared.ResolveConfigInt(nil, shared.ProcessPlayersBatchSizeParam, batchOverride),
		Concurrency:    shared.ResolveConfigInt(logger, shared.ProcessPlayersConcurrencyParam, concurrencyOverride),
		PlayersPerExec: shared.ResolveConfigInt(nil, shared.PlayerLandingPlayersPerExecParam, nil),
	}
}

// snapshotProcessPlayersConfig records processPlayersConfig in history once
// per execution (see shared.SnapshotConfig).
func snapshotProcessPlayersConfig(ctx workflow.Context, logger log.Logger, batchOverride, concurrencyOverride *int) (processPlayersConfig, error) {
	return shared.SnapshotConfig(ctx, func() processPlayersConfig {
		return loadProcessPlayersConfig(logger, batchOverride, concurrencyOverride)
	})
}
