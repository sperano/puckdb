package shared

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDate(t *testing.T) {
	t.Parallel()

	t.Run("valid date string returns correct time", func(t *testing.T) {
		t.Parallel()
		result := ParseDate("2023-10-10")
		require.False(t, result.IsZero())
		assert.Equal(t, 2023, result.Year())
		assert.Equal(t, time.October, result.Month())
		assert.Equal(t, 10, result.Day())
	})

	t.Run("invalid date string returns zero value", func(t *testing.T) {
		t.Parallel()
		result := ParseDate("not-a-date")
		assert.True(t, result.IsZero())
	})

	t.Run("empty string returns zero value", func(t *testing.T) {
		t.Parallel()
		result := ParseDate("")
		assert.True(t, result.IsZero())
	})
}

func TestParseDateToPgDate(t *testing.T) {
	t.Parallel()

	t.Run("empty string returns invalid Date", func(t *testing.T) {
		t.Parallel()
		result := ParseDateToPgDate("")
		assert.False(t, result.Valid)
	})

	t.Run("valid date string returns correct pgtype.Date", func(t *testing.T) {
		t.Parallel()
		result := ParseDateToPgDate("2024-01-15")
		require.True(t, result.Valid)
		assert.Equal(t, 2024, result.Time.Year())
		assert.Equal(t, time.January, result.Time.Month())
		assert.Equal(t, 15, result.Time.Day())
	})

	t.Run("invalid date string returns invalid Date", func(t *testing.T) {
		t.Parallel()
		result := ParseDateToPgDate("15/01/2024")
		assert.False(t, result.Valid)
	})
}

func TestParseTimestamptz(t *testing.T) {
	t.Parallel()

	const rfc3339Layout = "2006-01-02T15:04:05Z07:00"

	t.Run("empty string returns invalid Timestamptz", func(t *testing.T) {
		t.Parallel()
		result := ParseTimestamptz(rfc3339Layout, "")
		assert.False(t, result.Valid)
	})

	t.Run("valid timestamp returns correct Timestamptz", func(t *testing.T) {
		t.Parallel()
		result := ParseTimestamptz(rfc3339Layout, "2024-03-20T14:30:00Z")
		require.True(t, result.Valid)
		assert.Equal(t, 2024, result.Time.Year())
		assert.Equal(t, time.March, result.Time.Month())
		assert.Equal(t, 20, result.Time.Day())
		assert.Equal(t, 14, result.Time.Hour())
		assert.Equal(t, 30, result.Time.Minute())
	})

	t.Run("invalid timestamp returns invalid Timestamptz", func(t *testing.T) {
		t.Parallel()
		result := ParseTimestamptz(rfc3339Layout, "not-a-timestamp")
		assert.False(t, result.Valid)
	})
}
