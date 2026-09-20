package workflow

import (
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

// setViperInt sets a viper int flag for the duration of the test and restores
// the previous value on cleanup. Not safe for parallel tests (viper is global
// state) — matches the convention in replay_helpers_test.go.
func setViperInt(t *testing.T, key string, value int) {
	t.Helper()
	prev := viper.Get(key)
	viper.Set(key, value)
	t.Cleanup(func() { viper.Set(key, prev) })
}

// intPtr returns a pointer to v, for building test input literals.
func intPtr(v int) *int { return &v }

func TestLoadFetchPlayerLandingsConfig(t *testing.T) {
	// Not parallel: viper.Set mutates global state and is not thread-safe.

	tests := []struct {
		name                string
		viperBatch          *int
		viperConcurrency    *int
		batchOverride       *int
		concurrencyOverride *int
		wantBatchSize       int
		wantConcurrency     int
	}{
		{
			name:            "defaults when nothing configured",
			wantBatchSize:   config.DefaultPlayerLandingBatchSize,
			wantConcurrency: config.DefaultPlayerLandingConcurrency,
		},
		{
			name:             "viper flags override defaults",
			viperBatch:       intPtr(77),
			viperConcurrency: intPtr(9),
			wantBatchSize:    77,
			wantConcurrency:  9,
		},
		{
			name:                "input overrides win over viper and defaults",
			viperBatch:          intPtr(77),
			viperConcurrency:    intPtr(9),
			batchOverride:       intPtr(123),
			concurrencyOverride: intPtr(4),
			wantBatchSize:       123,
			wantConcurrency:     4,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.viperBatch != nil {
				setViperInt(t, config.FlagPlayerLandingBatchSize, *tc.viperBatch)
			}
			if tc.viperConcurrency != nil {
				setViperInt(t, config.FlagPlayerLandingConcurrency, *tc.viperConcurrency)
			}

			got := loadFetchPlayerLandingsConfig(nil, tc.batchOverride, tc.concurrencyOverride)
			require.Equal(t, tc.wantBatchSize, got.BatchSize)
			require.Equal(t, tc.wantConcurrency, got.Concurrency)
		})
	}
}
