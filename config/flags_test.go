package config

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestGetSeasonRange(t *testing.T) {
	// Not parallel - modifies global viper state
	cleanup := func() {
		viper.Set(FlagSeasonYear, nil)
		viper.Set(FlagFromSeasonYear, nil)
		viper.Set(FlagToSeasonYear, nil)
	}
	t.Cleanup(cleanup)

	tests := []struct {
		name      string
		season    int
		from      int
		to        int
		wantStart int
		wantEnd   int
	}{
		{"single season flag", 2023, 0, 0, 2023, 2023},
		{"range flags", 0, 2020, 2024, 2020, 2024},
		{"season overrides range", 2023, 2020, 2024, 2023, 2023},
		{"no flags set", 0, 0, 0, 0, 0},
		{"only from set", 0, 2020, 0, 2020, 0},
		{"only to set", 0, 0, 2024, 0, 2024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup() // Reset before each subtest
			viper.Set(FlagSeasonYear, tt.season)
			viper.Set(FlagFromSeasonYear, tt.from)
			viper.Set(FlagToSeasonYear, tt.to)

			start, end := GetSeasonRange()
			assert.Equal(t, tt.wantStart, start)
			assert.Equal(t, tt.wantEnd, end)
		})
	}
}
