package yahoo

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeStatSet reads back the stats JSON of a ReplaceYahooTeamSummaryStatsBatch row.
func decodeStatSet(t *testing.T, encoded []byte) []summaryStat {
	t.Helper()
	var set []summaryStat
	require.NoError(t, json.Unmarshal(encoded, &set))
	return set
}

func floatPtr(v float64) *float64 { return &v }

func TestSummaryStatSet_KeepsEveryCategory(t *testing.T) {
	t.Parallel()
	date := pgDate(time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC))
	stats := []store.Stat{
		{StatID: "999", Value: "7"},   // a category no fixed column maps
		{StatID: "26", Value: ".915"}, // fractional value without a leading zero
		{StatID: "0", Value: "3"},
		{StatID: "19", Value: "-"}, // no data that day
		{StatID: "23", Value: ""},
		{StatID: "31", Value: "n/a"}, // not a number: raw text only
		{StatID: "1", Value: "2"},
		{StatID: "1", Value: "4"}, // listed twice: the last value wins
		{StatID: "goals", Value: "5"},
		{StatID: "-3", Value: "5"},
		{StatID: "2147483648", Value: "5"}, // above the integer stat_id column
		{StatID: "2147483647", Value: "6"},
		{StatID: "32", Value: "NaN"},
	}

	set, err := summaryStatSet(77777, 3, date, stats)

	require.NoError(t, err)
	assert.EqualValues(t, 77777, set.LeagueID)
	assert.EqualValues(t, 3, set.TeamID)
	assert.Equal(t, date, set.Date)
	assert.Equal(t, []summaryStat{
		{StatID: 0, Value: floatPtr(3), RawValue: "3"},
		{StatID: 1, Value: floatPtr(4), RawValue: "4"},
		{StatID: 19, RawValue: "-"},
		{StatID: 23, RawValue: ""},
		{StatID: 26, Value: floatPtr(0.915), RawValue: ".915"},
		{StatID: 31, RawValue: "n/a"},
		{StatID: 32, RawValue: "NaN"},
		{StatID: 999, Value: floatPtr(7), RawValue: "7"},
		{StatID: math.MaxInt32, Value: floatPtr(6), RawValue: "6"},
	}, decodeStatSet(t, set.Stats), "sorted by stat ID, invalid IDs left out")
}

// A summary without stats still replaces its set, with an empty one, so the
// rows of an earlier file are deleted.
func TestSummaryStatSet_EmptyIsAnEmptyArray(t *testing.T) {
	t.Parallel()
	set, err := summaryStatSet(1, 1, pgDate(time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)), nil)

	require.NoError(t, err)
	assert.JSONEq(t, `[]`, string(set.Stats))
}

func TestSummaryStatNumber(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]*float64{
		"12": floatPtr(12), "-1": floatPtr(-1), "2.45": floatPtr(2.45),
		"-": nil, "": nil, "Inf": nil, "1/3": nil,
	} {
		assert.Equal(t, want, summaryStatNumber(raw), raw)
	}
}
