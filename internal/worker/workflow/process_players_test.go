package workflow

import (
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/stretchr/testify/require"
)

func TestLoadProcessPlayersConfig(t *testing.T) {
	// Not parallel: viper.Set mutates global state and is not thread-safe.

	t.Run("defaults when nothing configured", func(t *testing.T) {
		got := loadProcessPlayersConfig(nil, nil, nil)
		require.Equal(t, config.DefaultProcessPlayersBatchSize, got.BatchSize)
		require.Equal(t, config.DefaultProcessPlayersConcurrency, got.Concurrency)
		require.Equal(t, config.DefaultPlayerLandingPlayersPerExec, got.PlayersPerExec)
	})

	t.Run("viper flags override defaults", func(t *testing.T) {
		setViperInt(t, config.FlagProcessPlayersBatchSize, 77)
		setViperInt(t, config.FlagProcessPlayersConcurrency, 3)
		setViperInt(t, config.FlagPlayerLandingPlayersPerExec, 500)

		got := loadProcessPlayersConfig(nil, nil, nil)
		require.Equal(t, 77, got.BatchSize)
		require.Equal(t, 3, got.Concurrency)
		require.Equal(t, 500, got.PlayersPerExec)
	})

	t.Run("input overrides win for batch size and concurrency", func(t *testing.T) {
		setViperInt(t, config.FlagProcessPlayersBatchSize, 77)
		setViperInt(t, config.FlagProcessPlayersConcurrency, 3)

		got := loadProcessPlayersConfig(nil, intPtr(200), intPtr(9))
		require.Equal(t, 200, got.BatchSize)
		require.Equal(t, 9, got.Concurrency)
	})

	t.Run("PlayersPerExec has no override", func(t *testing.T) {
		// loadProcessPlayersConfig doesn't take a PlayersPerExec override; it
		// always resolves from shared.PlayerLandingPlayersPerExecParam.
		setViperInt(t, config.FlagPlayerLandingPlayersPerExec, 250)

		got := loadProcessPlayersConfig(nil, intPtr(1), intPtr(1))
		require.Equal(t, 250, got.PlayersPerExec)
	})
}
