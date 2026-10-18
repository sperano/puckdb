package graph

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDateInput(t *testing.T) {
	t.Parallel()

	parsed, err := parseDateInput("date", "2024-03-15")
	require.NoError(t, err)
	require.True(t, parsed.Valid)
	assert.Equal(t, time.Date(2024, time.March, 15, 0, 0, 0, 0, time.UTC), parsed.Time)
}

func TestParseDateInputRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"not-a-date", "2024-02-30", ""} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			parsed, err := parseDateInput("startDate", value)
			require.Error(t, err)
			assert.False(t, parsed.Valid)
			assert.ErrorContains(t, err, "startDate")
			assert.ErrorContains(t, err, "expected YYYY-MM-DD")
		})
	}
}

func TestParseOptionalDateInput(t *testing.T) {
	t.Parallel()

	t.Run("nil is omitted", func(t *testing.T) {
		t.Parallel()
		parsed, err := parseOptionalDateInput("startDate", nil)
		require.NoError(t, err)
		assert.False(t, parsed.Valid)
	})

	t.Run("present valid date", func(t *testing.T) {
		t.Parallel()
		value := "2024-03-15"
		parsed, err := parseOptionalDateInput("startDate", &value)
		require.NoError(t, err)
		assert.True(t, parsed.Valid)
	})

	t.Run("present empty date is invalid", func(t *testing.T) {
		t.Parallel()
		value := ""
		parsed, err := parseOptionalDateInput("startDate", &value)
		require.Error(t, err)
		assert.False(t, parsed.Valid)
		assert.ErrorContains(t, err, "startDate")
	})
}

// TestParseDateInputErrorText pins the full error: argument name, quoted
// value, expected syntax, then time.Parse's reason.
func TestParseDateInputErrorText(t *testing.T) {
	t.Parallel()

	_, err := parseDateInput("endDate", "2024-02-30")
	assert.EqualError(t, err, `invalid endDate "2024-02-30" (expected YYYY-MM-DD): parsing time "2024-02-30": day out of range`)
	var parseErr *time.ParseError
	assert.ErrorAs(t, err, &parseErr)
}

// TestParseDateInputRejectsLooseSyntax checks the spellings a lenient
// parser (pgtype.Date.Scan, unpadded layouts) would take.
func TestParseDateInputRejectsLooseSyntax(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"2024-3-15", "2024-03-15T00:00:00Z", " 2024-03-15", "infinity", "2024-03-15 BC", "12024-03-15"} {
		parsed, err := parseDateInput("date", value)
		assert.Error(t, err, value)
		assert.False(t, parsed.Valid, value)
	}
}
