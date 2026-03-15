package worker

import (
	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/log"
)

// configIntParam describes how to resolve a single integer configuration value.
// The resolution order is: viper flag → default → input override → cap at max.
type configIntParam struct {
	Flag       string // viper flag for initial value (empty = use Default directly)
	Default    int    // fallback when Flag is empty or viper returns <= 0
	MaxFlag    string // viper flag for cap (empty = no cap)
	MaxDefault int    // fallback cap (0 = only cap when viper max > 0)
}

// resolveConfigInt resolves an integer config value from viper, defaults, and an optional override.
// When logger is nil, capping warnings are silently suppressed.
func resolveConfigInt(logger log.Logger, p configIntParam, override *int) int {
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

var seasonConcurrencyParam = configIntParam{
	Default:    config.DefaultSeasonConcurrency,
	MaxFlag:    config.FlagMaxSeasonConcurrency,
	MaxDefault: config.DefaultMaxSeasonConcurrency,
}

var playerLandingConcurrencyParam = configIntParam{
	Flag:       config.FlagPlayerLandingConcurrency,
	Default:    config.DefaultPlayerLandingConcurrency,
	MaxFlag:    config.FlagMaxSeasonConcurrency,
	MaxDefault: 0, // only cap when viper max > 0
}

var playerLandingBatchSizeParam = configIntParam{
	Flag:    config.FlagPlayerLandingBatchSize,
	Default: config.DefaultPlayerLandingBatchSize,
}

var processPlayersConcurrencyParam = configIntParam{
	Flag:    config.FlagProcessPlayersConcurrency,
	Default: config.DefaultProcessPlayersConcurrency,
}

var processPlayersBatchSizeParam = configIntParam{
	Flag:    config.FlagProcessPlayersBatchSize,
	Default: config.DefaultProcessPlayersBatchSize,
}

var playerLogsBatchSizeParam = configIntParam{
	Flag:    config.FlagPlayerLogsBatchSize,
	Default: config.DefaultPlayerLogsBatchSize,
}

var playerLogsConcurrencyParam = configIntParam{
	Flag:    config.FlagPlayerLogsBatchConcurrency,
	Default: config.DefaultPlayerLogsBatchConcurrency,
}
