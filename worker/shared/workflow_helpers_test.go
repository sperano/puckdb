package shared

import (
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestViperIntOrDefault(t *testing.T) {
	// Not parallel: viper.Set mutates global state and is not thread-safe.

	const testKey = "test.viper.int.or.default"

	t.Run("viper returns zero so fallback is used", func(t *testing.T) {
		result := ViperIntOrDefault(testKey+"_unset", 99)
		assert.Equal(t, 99, result)
	})

	t.Run("viper returns positive value so viper value is used", func(t *testing.T) {
		key := testKey + "_positive"
		viper.Set(key, 42)
		t.Cleanup(func() { viper.Set(key, 0) })

		result := ViperIntOrDefault(key, 99)
		assert.Equal(t, 42, result)
	})

	t.Run("viper returns negative value so fallback is used", func(t *testing.T) {
		key := testKey + "_negative"
		viper.Set(key, -5)
		t.Cleanup(func() { viper.Set(key, 0) })

		result := ViperIntOrDefault(key, 99)
		assert.Equal(t, 99, result)
	})
}

func TestEffectiveEndDate(t *testing.T) {
	t.Parallel()

	t.Run("future end date returns yesterday", func(t *testing.T) {
		t.Parallel()
		futureDate := time.Now().Add(30 * 24 * time.Hour)
		result := EffectiveEndDate(futureDate)
		yesterday := time.Now().AddDate(0, 0, -1)

		// Result should be approximately yesterday (within a few seconds of test execution).
		diff := result.Sub(yesterday).Abs()
		assert.Less(t, diff, time.Second, "result should be yesterday")
	})

	t.Run("past end date is returned unchanged", func(t *testing.T) {
		t.Parallel()
		pastDate := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
		result := EffectiveEndDate(pastDate)
		assert.Equal(t, pastDate, result)
	})

	t.Run("today is capped to yesterday", func(t *testing.T) {
		t.Parallel()
		today := time.Now()
		result := EffectiveEndDate(today)
		yesterday := time.Now().AddDate(0, 0, -1)

		diff := result.Sub(yesterday).Abs()
		assert.Less(t, diff, time.Second, "today should be capped to yesterday")
	})
}

func TestCountDaysInSeason(t *testing.T) {
	t.Parallel()

	t.Run("season fully in the past returns correct day count", func(t *testing.T) {
		t.Parallel()
		// Build a SeasonInfo for a 10-day window entirely in the past.
		start := nhl.NewDate(2020, time.January, 1)
		end := nhl.NewDate(2020, time.January, 10)
		season := nhl.SeasonInfo{
			ID:             nhl.NewSeason(2019),
			StandingsStart: start,
			StandingsEnd:   end,
		}

		count, err := CountDaysInSeason(season)
		require.NoError(t, err)
		// CountDays is inclusive: Jan 1 through Jan 10 = 10 days.
		assert.Equal(t, 10, count)
	})

	t.Run("season with future end date is capped at yesterday", func(t *testing.T) {
		t.Parallel()
		// Start three days ago, end two days from now.
		startTime := time.Now().AddDate(0, 0, -3)
		endTime := time.Now().AddDate(0, 0, 2)

		start := nhl.DateFromTime(startTime)
		end := nhl.DateFromTime(endTime)

		season := nhl.SeasonInfo{
			ID:             nhl.NewSeason(2025),
			StandingsStart: start,
			StandingsEnd:   end,
		}

		count, err := CountDaysInSeason(season)
		require.NoError(t, err)
		// The end is capped to ~yesterday, so count should be roughly 3
		// (3 days ago + 2 days ago + yesterday). Allow ±1 for clock boundaries.
		assert.GreaterOrEqual(t, count, 2)
		assert.LessOrEqual(t, count, 4)
	})

	t.Run("single-day season returns 1", func(t *testing.T) {
		t.Parallel()
		d := nhl.NewDate(2019, time.October, 1)
		season := nhl.SeasonInfo{
			ID:             nhl.NewSeason(2019),
			StandingsStart: d,
			StandingsEnd:   d,
		}

		count, err := CountDaysInSeason(season)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})
}

func TestGetDayConcurrency(t *testing.T) {
	// Not parallel: viper.Set mutates global state and is not thread-safe.

	t.Run("returns default when viper value is zero", func(t *testing.T) {
		viper.Set(config.FlagDayConcurrency, 0)
		t.Cleanup(func() { viper.Set(config.FlagDayConcurrency, 0) })

		result := GetDayConcurrency()
		assert.Equal(t, config.DefaultDayConcurrency, result)
	})

	t.Run("returns configured value when viper value is positive", func(t *testing.T) {
		viper.Set(config.FlagDayConcurrency, 7)
		t.Cleanup(func() { viper.Set(config.FlagDayConcurrency, 0) })

		result := GetDayConcurrency()
		assert.Equal(t, 7, result)
	})

	t.Run("returns default when viper value is negative", func(t *testing.T) {
		viper.Set(config.FlagDayConcurrency, -3)
		t.Cleanup(func() { viper.Set(config.FlagDayConcurrency, 0) })

		result := GetDayConcurrency()
		assert.Equal(t, config.DefaultDayConcurrency, result)
	})
}

func TestDefaultActivityOptions(t *testing.T) {
	// Not parallel: viper.Set mutates global state and is not thread-safe.

	t.Run("uses defaults when viper is not configured", func(t *testing.T) {
		opts := DefaultActivityOptions()

		expectedStartToClose := time.Duration(config.DefaultActivityStartToCloseTimeout) * time.Minute
		expectedHeartbeat := time.Duration(config.DefaultActivityHeartbeatTimeout) * time.Second
		expectedInitial := time.Duration(config.DefaultTemporalRetryInitialInterval) * time.Second
		expectedMax := time.Duration(config.DefaultTemporalRetryMaxInterval) * time.Second

		assert.Equal(t, expectedStartToClose, opts.StartToCloseTimeout)
		assert.Equal(t, expectedHeartbeat, opts.HeartbeatTimeout)
		require.NotNil(t, opts.RetryPolicy)
		assert.Equal(t, expectedInitial, opts.RetryPolicy.InitialInterval)
		assert.Equal(t, expectedMax, opts.RetryPolicy.MaximumInterval)
		assert.Equal(t, int32(config.DefaultTemporalRetryMaxAttempts), opts.RetryPolicy.MaximumAttempts)
		assert.Equal(t, config.DefaultBackoffCoefficient, opts.RetryPolicy.BackoffCoefficient)
	})

	t.Run("uses viper values when configured", func(t *testing.T) {
		viper.Set(config.FlagActivityStartToCloseTimeout, 30)
		viper.Set(config.FlagActivityHeartbeatTimeout, 120)
		t.Cleanup(func() {
			viper.Set(config.FlagActivityStartToCloseTimeout, 0)
			viper.Set(config.FlagActivityHeartbeatTimeout, 0)
		})

		opts := DefaultActivityOptions()

		assert.Equal(t, 30*time.Minute, opts.StartToCloseTimeout)
		assert.Equal(t, 120*time.Second, opts.HeartbeatTimeout)
	})
}
