package workflow

import (
	"testing"

	"github.com/sperano/puckdb/config"
	"github.com/stretchr/testify/require"
)

func TestLoadFetchSeasonPlayerLogsConfig(t *testing.T) {
	// Not parallel: viper.Set mutates global state and is not thread-safe.

	t.Run("defaults when nothing configured", func(t *testing.T) {
		got := loadFetchSeasonPlayerLogsConfig()
		require.Equal(t, config.DefaultPlayerLogsBatchSize, got.BatchSize)
		require.Equal(t, config.DefaultPlayerLogsBatchConcurrency, got.Concurrency)
	})

	t.Run("viper flags override defaults", func(t *testing.T) {
		setViperInt(t, config.FlagPlayerLogsBatchSize, 42)
		setViperInt(t, config.FlagPlayerLogsBatchConcurrency, 6)

		got := loadFetchSeasonPlayerLogsConfig()
		require.Equal(t, 42, got.BatchSize)
		require.Equal(t, 6, got.Concurrency)
	})
}
