package shared

import (
	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/log"
)

// ConfigIntParam describes how to resolve a single integer configuration value.
// The resolution order is: viper flag → default → input override → cap at max.
type ConfigIntParam struct {
	Flag       string // viper flag for initial value (empty = use Default directly)
	Default    int    // fallback when Flag is empty or viper returns <= 0
	MaxFlag    string // viper flag for cap (empty = no cap)
	MaxDefault int    // fallback cap (0 = only cap when viper max > 0)
}

// ResolveConfigInt resolves an integer config value from viper, defaults, and an optional override.
// When logger is nil, capping warnings are silently suppressed.
func ResolveConfigInt(logger log.Logger, p ConfigIntParam, override *int) int {
	value := p.Default
	if p.Flag != "" {
		if v := viper.GetInt(p.Flag); v > 0 {
			value = v
		}
	}

	if override != nil && *override > 0 {
		value = *override
	}

	if p.MaxFlag != "" || p.MaxDefault > 0 {
		maxValue := p.MaxDefault
		if p.MaxFlag != "" {
			if v := viper.GetInt(p.MaxFlag); v > 0 {
				maxValue = v
			}
		}
		if maxValue > 0 && value > maxValue {
			if logger != nil {
				logger.Warn("Configured value exceeds maximum, capping",
					"requested", value,
					"max", maxValue)
			}
			value = maxValue
		}
	}

	return value
}

// Pre-defined params for common config patterns.

// DayConcurrencyParam resolves how many per-day activities a season workflow
// runs in parallel.
var DayConcurrencyParam = ConfigIntParam{
	Flag:    config.FlagDayConcurrency,
	Default: config.DefaultDayConcurrency,
}

// AssetBatchSizeParam resolves how many assets one FetchAssetBatch activity
// handles. The parent FetchAssetsWorkflow and its class children must agree on
// it, so the parent forwards its snapshot to the children.
var AssetBatchSizeParam = ConfigIntParam{
	Flag:    config.FlagAssetBatchSize,
	Default: config.DefaultAssetBatchSize,
}

var SeasonConcurrencyParam = ConfigIntParam{
	Default:    config.DefaultSeasonConcurrency,
	MaxFlag:    config.FlagMaxSeasonConcurrency,
	MaxDefault: config.DefaultMaxSeasonConcurrency,
}

var PlayerLandingConcurrencyParam = ConfigIntParam{
	Flag:       config.FlagPlayerLandingConcurrency,
	Default:    config.DefaultPlayerLandingConcurrency,
	MaxFlag:    config.FlagMaxPlayerLandingConcurrency,
	MaxDefault: config.DefaultMaxPlayerLandingConcurrency,
}

var PlayerLandingBatchSizeParam = ConfigIntParam{
	Flag:    config.FlagPlayerLandingBatchSize,
	Default: config.DefaultPlayerLandingBatchSize,
}

// PlayerLandingPlayersPerExecParam resolves how many players ProcessPlayersWorkflow
// processes per ContinueAsNew execution before yielding to a fresh execution.
var PlayerLandingPlayersPerExecParam = ConfigIntParam{
	Flag:    config.FlagPlayerLandingPlayersPerExec,
	Default: config.DefaultPlayerLandingPlayersPerExec,
}

// Yahoo player fetch params. FetchYahooPlayersWorkflow freezes these for a
// whole multi-execution run, so each must resolve to a positive value: a
// carried zero could not be corrected by a worker restart.
var (
	MaxYahooPlayerIDParam = ConfigIntParam{
		Flag:    config.FlagMaxYahooPlayerID,
		Default: config.DefaultMaxYahooPlayerID,
	}
	YahooPlayerConcurrencyParam = ConfigIntParam{
		Flag:    config.FlagYahooPlayerBatchSize,
		Default: config.DefaultYahooPlayerBatchSize,
	}
	YahooPlayerActivityBatchSizeParam = ConfigIntParam{
		Flag:    config.FlagYahooPlayerActivityBatchSize,
		Default: config.DefaultYahooPlayerActivityBatchSize,
	}
	YahooPlayersPerExecutionParam = ConfigIntParam{
		Flag:    config.FlagYahooPlayersPerExecution,
		Default: config.DefaultYahooPlayersPerExecution,
	}
)

var ProcessPlayersConcurrencyParam = ConfigIntParam{
	Flag:    config.FlagProcessPlayersConcurrency,
	Default: config.DefaultProcessPlayersConcurrency,
}

var ProcessPlayersBatchSizeParam = ConfigIntParam{
	Flag:    config.FlagProcessPlayersBatchSize,
	Default: config.DefaultProcessPlayersBatchSize,
}

var PlayerLogsBatchSizeParam = ConfigIntParam{
	Flag:    config.FlagPlayerLogsBatchSize,
	Default: config.DefaultPlayerLogsBatchSize,
}

var PlayerLogsConcurrencyParam = ConfigIntParam{
	Flag:    config.FlagPlayerLogsBatchConcurrency,
	Default: config.DefaultPlayerLogsBatchConcurrency,
}

// AssetClassConcurrencyParam resolves the within-class concurrency for asset
// batch activities (how many FetchAssetBatch activities run in parallel inside
// one FetchAssetsClassWorkflow). No upper cap is configured: the within-class
// fan-out is limited by the asset CDN's per-host connection pool (see
// asset.assetMaxIdleConnsPerHost), not by the cross-class parent pool.
var AssetClassConcurrencyParam = ConfigIntParam{
	Flag:    config.FlagAssetClassConcurrency,
	Default: config.DefaultAssetClassConcurrency,
}

// MaxAssetClassConcurrencyParam resolves how many class child workflows the
// parent FetchAssetsWorkflow may run concurrently. The flag name reads as a
// "max" because the parent has nine candidate children and the configured
// value is the worker-pool size that bounds them.
var MaxAssetClassConcurrencyParam = ConfigIntParam{
	Flag:    config.FlagMaxAssetClassConcurrency,
	Default: config.DefaultMaxAssetClassConcurrency,
}
