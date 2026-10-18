package pgconv

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
)

func TestFormatDate(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		in   pgtype.Date
		want string
	}{
		"null":                {pgtype.Date{}, ""},
		"null ignores time":   {pgtype.Date{Time: time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)}, ""},
		"day":                 {validDate(2024, time.March, 15), "2024-03-15"},
		"leap day":            {validDate(2024, time.February, 29), "2024-02-29"},
		"padded year":         {validDate(99, time.January, 1), "0099-01-01"},
		"time of day dropped": {pgtype.Date{Time: time.Date(2024, 3, 15, 23, 59, 59, 0, time.UTC), Valid: true}, "2024-03-15"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, FormatDate(tc.in))
		})
	}
}

// TestFormatDateRoundTrip checks a parsed client date renders unchanged.
func TestFormatDateRoundTrip(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"2024-03-15", "2024-02-29", "1917-12-19", "9999-12-31"} {
		d, err := ParseDate(s)
		assert.NoError(t, err)
		assert.Equal(t, s, FormatDate(d))
	}
}

func TestFormatInt4(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", FormatInt4(pgtype.Int4{Int32: 7}))
	assert.Equal(t, "0", FormatInt4(pgtype.Int4{Valid: true}))
	assert.Equal(t, "-7", FormatInt4(pgtype.Int4{Int32: -7, Valid: true}))
	assert.Equal(t, "-2147483648", FormatInt4(pgtype.Int4{Int32: math.MinInt32, Valid: true}))
	assert.Equal(t, "2147483647", FormatInt4(pgtype.Int4{Int32: math.MaxInt32, Valid: true}))
}

func TestFormatInt8(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", FormatInt8(pgtype.Int8{Int64: 10}))
	assert.Equal(t, "0", FormatInt8(pgtype.Int8{Valid: true}))
	assert.Equal(t, "10", FormatInt8(pgtype.Int8{Int64: 10, Valid: true}))
	assert.Equal(t, "-9223372036854775808", FormatInt8(pgtype.Int8{Int64: math.MinInt64, Valid: true}))
	assert.Equal(t, "9223372036854775807", FormatInt8(pgtype.Int8{Int64: math.MaxInt64, Valid: true}))
}

// TestFormatIntMatchesFmt checks the replaced fmt.Sprintf("%d") renderers
// print the same bytes.
func TestFormatIntMatchesFmt(t *testing.T) {
	t.Parallel()

	for _, v := range []int32{math.MinInt32, -1, 0, 1, math.MaxInt32} {
		assert.Equal(t, fmt.Sprintf("%d", v), FormatInt4(pgtype.Int4{Int32: v, Valid: true}))
	}
	for _, v := range []int64{math.MinInt64, math.MinInt32 - 1, -1, 0, 1, math.MaxInt32 + 1, math.MaxInt64} {
		assert.Equal(t, fmt.Sprintf("%d", v), FormatInt8(pgtype.Int8{Int64: v, Valid: true}))
	}
}

func TestFormatPlayerPosition(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", FormatPlayerPosition(sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC}))
	cases := map[sqlcdb.PlayerPosition]string{
		sqlcdb.PlayerPositionC:  "C",
		sqlcdb.PlayerPositionLW: "LW",
		sqlcdb.PlayerPositionRW: "RW",
		sqlcdb.PlayerPositionF:  "F",
		sqlcdb.PlayerPositionD:  "D",
		sqlcdb.PlayerPositionG:  "G",
	}
	for p, want := range cases {
		assert.Equal(t, want, FormatPlayerPosition(sqlcdb.NullPlayerPosition{PlayerPosition: p, Valid: true}))
	}
}

func validDate(year int, month time.Month, day int) pgtype.Date {
	return pgtype.Date{Time: time.Date(year, month, day, 0, 0, 0, 0, time.UTC), Valid: true}
}
