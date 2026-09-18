package shared

import (
	"errors"
	"fmt"

	"github.com/sperano/puckdb/config"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

// configSnapshotChangeID is the workflow.GetVersion change ID that gates
// configuration snapshotting. Histories recorded before snapshotting existed
// carry no marker for it, so GetVersion returns DefaultVersion for them.
const configSnapshotChangeID = "config-snapshot"

// configSnapshotVersion is the version recorded for executions that snapshot
// their configuration via SideEffect.
const configSnapshotVersion = 1

// SnapshotConfig resolves a workflow's configuration once per execution and
// records the result in history, so a replay after a worker restart reuses the
// recorded value instead of re-reading process-local settings (viper flags,
// YAML files) that may have changed. Anything that shapes the command sequence
// (concurrency, batch sizes, which leagues to fetch) must go through here.
//
// load runs inside workflow.SideEffect on new executions and is never re-run
// on replay. On a history recorded before snapshotting existed, load runs live
// instead, which reproduces the read those histories were recorded with.
// Call it once per execution; ContinueAsNew runs are new executions and must
// either snapshot again or carry the value in their input.
func SnapshotConfig[T any](ctx workflow.Context, load func() T) (T, error) {
	var cfg T
	version := workflow.GetVersion(ctx, configSnapshotChangeID, workflow.DefaultVersion, configSnapshotVersion)
	if version == workflow.DefaultVersion {
		return load(), nil
	}
	encoded := workflow.SideEffect(ctx, func(workflow.Context) any { return load() })
	if err := encoded.Get(&cfg); err != nil {
		return cfg, fmt.Errorf("decode config snapshot: %w", err)
	}
	return cfg, nil
}

// SnapshotConfigInt is SnapshotConfig for a single ResolveConfigInt value.
func SnapshotConfigInt(ctx workflow.Context, logger log.Logger, p ConfigIntParam, override *int) (int, error) {
	return SnapshotConfig(ctx, func() int { return ResolveConfigInt(logger, p, override) })
}

// YahooSeasonsSnapshot is the recorded outcome of config.GetYahooSeasonsConfig,
// shaped so it round-trips through the history data converter: a load failure
// is carried as a string rather than an error value.
type YahooSeasonsSnapshot struct {
	// Seasons is the loaded map; nil when Yahoo is not configured or loading failed.
	Seasons config.YahooSeasonsMap `json:"seasons,omitempty"`
	// Err is the load failure message. Empty on success and when Yahoo is
	// simply not configured (config.ErrYahooNotConfigured), which is not a failure.
	Err string `json:"err,omitempty"`
}

// LoadYahooSeasonsSnapshot reads the Yahoo seasons config into a snapshot.
func LoadYahooSeasonsSnapshot() YahooSeasonsSnapshot {
	seasons, err := config.GetYahooSeasonsConfig()
	switch {
	case errors.Is(err, config.ErrYahooNotConfigured):
		return YahooSeasonsSnapshot{}
	case err != nil:
		return YahooSeasonsSnapshot{Err: err.Error()}
	}
	return YahooSeasonsSnapshot{Seasons: seasons}
}

// LoadError returns the recorded load failure, or nil when loading succeeded
// or Yahoo is not configured.
func (s YahooSeasonsSnapshot) LoadError() error {
	if s.Err == "" {
		return nil
	}
	return errors.New(s.Err)
}

// Season returns the Yahoo config for startYear and whether the season is
// configured at all.
func (s YahooSeasonsSnapshot) Season(startYear int) (config.Season, bool) {
	season, ok := s.Seasons[startYear]
	return season, ok
}
