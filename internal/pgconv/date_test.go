package pgconv

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDateAccepts(t *testing.T) {
	t.Parallel()

	cases := map[string]time.Time{
		"2024-03-15": time.Date(2024, time.March, 15, 0, 0, 0, 0, time.UTC),
		"2024-02-29": time.Date(2024, time.February, 29, 0, 0, 0, 0, time.UTC),
		"0001-01-01": time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC),
		"9999-12-31": time.Date(9999, time.December, 31, 0, 0, 0, 0, time.UTC),
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseDate(in)
			require.NoError(t, err)
			assert.True(t, got.Valid)
			assert.Equal(t, want, got.Time)
			assert.Equal(t, time.UTC, got.Time.Location())
		})
	}
}

// TestParseDateRejects pins what pgtype.Date.Scan would have accepted or
// normalized, and every looser spelling of a day.
func TestParseDateRejects(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"",
		"not-a-date",
		"2024-02-30",
		"2023-02-29",
		"2024-13-01",
		"2024-00-10",
		"2024-3-15",
		"2024-03-5",
		"24-03-15",
		"12025-10-08",
		"2024/03/15",
		"15/03/2024",
		" 2024-03-15",
		"2024-03-15 ",
		"2024-03-15T00:00:00Z",
		"2025-10-08 BC",
		"infinity",
		"-infinity",
	} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseDate(in)
			require.Error(t, err)
			assert.False(t, got.Valid)
			assert.Equal(t, time.Time{}, got.Time)
		})
	}
}
