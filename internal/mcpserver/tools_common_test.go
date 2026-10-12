package mcpserver

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSeasonID int32 = 20252026
	// wrappedSeasonID wraps to testSeasonID in an unchecked int32 cast.
	wrappedSeasonID int64 = int64(testSeasonID) + 1<<32
)

func requestWith(args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

func TestIntegerValuePolicy(t *testing.T) {
	accepted := map[string]struct {
		raw  any
		want int64
	}{
		"json number":           {float64(testSeasonID), int64(testSeasonID)},
		"negative json number":  {float64(-3), -3},
		"zero":                  {float64(0), 0},
		"largest exact float":   {float64(maxExactJSONInteger), maxExactJSONInteger},
		"decimal string":        {"20252026", int64(testSeasonID)},
		"signed decimal string": {"-7", -7},
		"int":                   {7, 7},
		"int32":                 {int32(7), 7},
		"int64":                 {int64(7), 7},
	}
	for name, tc := range accepted {
		t.Run(name, func(t *testing.T) {
			got, ok := integerValue(tc.raw)
			require.True(t, ok)
			assert.Equal(t, tc.want, got)
		})
	}

	refused := map[string]any{
		"fractional":           20252026.9,
		"NaN":                  math.NaN(),
		"+Inf":                 math.Inf(1),
		"-Inf":                 math.Inf(-1),
		"beyond exact float":   float64(maxExactJSONInteger + 1),
		"below exact float":    -float64(maxExactJSONInteger + 1),
		"huge float":           1e300,
		"int64 beyond exact":   int64(maxExactJSONInteger + 1),
		"decimal point string": "20252026.0",
		"exponent string":      "2e7",
		"hex string":           "0x10",
		"padded string":        " 7",
		"empty string":         "",
		"word":                 "abc",
		"string beyond exact":  "9007199254740993",
		"bool":                 true,
		"array":                []any{float64(1)},
	}
	for name, raw := range refused {
		t.Run(name, func(t *testing.T) {
			_, ok := integerValue(raw)
			assert.False(t, ok)
		})
	}
}

func TestRequireIntChecksDestinationBeforeConverting(t *testing.T) {
	wide := intArg{name: "n", min: math.MinInt64, max: math.MaxInt64}

	_, errResult := requireInt[int32](requestWith(map[string]any{"n": float64(wrappedSeasonID)}), wide)
	require.NotNil(t, errResult)
	assert.Equal(t, "invalid n 4315219322: want an integer from -2147483648 to 2147483647", resultText(t, errResult))

	got, errResult := requireInt[int64](requestWith(map[string]any{"n": float64(wrappedSeasonID)}), wide)
	require.Nil(t, errResult)
	assert.Equal(t, wrappedSeasonID, got)
}

func TestRequireInt(t *testing.T) {
	tests := map[string]struct {
		args    map[string]any
		want    int32
		wantErr string
	}{
		"season":         {args: map[string]any{"season": float64(testSeasonID)}, want: testSeasonID},
		"numeric string": {args: map[string]any{"season": "20252026"}, want: testSeasonID},
		"absent":         {args: map[string]any{}, wantErr: "season is required"},
		"null":           {args: map[string]any{"season": nil}, wantErr: "season is required"},
		"zero":           {args: map[string]any{"season": float64(0)}, wantErr: "invalid season 0: want an integer from 19171918 to 99999999"},
		"negative":       {args: map[string]any{"season": float64(-20252026)}, wantErr: "invalid season -20252026: want an integer from 19171918 to 99999999"},
		"fractional":     {args: map[string]any{"season": 20252026.9}, wantErr: "invalid season 20252026.9: want an integer from 19171918 to 99999999"},
		"overflow":       {args: map[string]any{"season": float64(wrappedSeasonID)}, wantErr: "invalid season 4315219322: want an integer from 19171918 to 99999999"},
		"start year":     {args: map[string]any{"season": float64(2025)}, wantErr: "invalid season 2025: want an integer from 19171918 to 99999999"},
		"bad string":     {args: map[string]any{"season": "2025-2026"}, wantErr: `invalid season "2025-2026": want an integer from 19171918 to 99999999`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, errResult := requireInt[int32](requestWith(tc.args), seasonParam)
			if tc.wantErr != "" {
				require.NotNil(t, errResult)
				assert.True(t, errResult.IsError)
				assert.Equal(t, tc.wantErr, resultText(t, errResult))
				return
			}
			require.Nil(t, errResult)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFormatRawCapsLongValues(t *testing.T) {
	assert.Equal(t, "20252026.9", formatRaw(20252026.9))
	assert.Equal(t, `"abc"`, formatRaw("abc"))
	long := formatRaw(1e308)
	assert.Len(t, long, maxEchoedArgumentLength+len("..."))
	assert.True(t, strings.HasSuffix(long, "..."))
}

func TestOptionalIntKeepsZeroAsGiven(t *testing.T) {
	got, given, errResult := optionalInt[int32](requestWith(map[string]any{"limit": float64(0)}), limitParam)
	require.Nil(t, errResult)
	assert.True(t, given)
	assert.Zero(t, got)

	_, given, errResult = optionalInt[int32](requestWith(map[string]any{}), limitParam)
	require.Nil(t, errResult)
	assert.False(t, given)

	_, given, errResult = optionalInt[int32](requestWith(map[string]any{"limit": float64(-1)}), limitParam)
	require.NotNil(t, errResult)
	assert.False(t, given)
}

func TestOptionalFilterTreatsZeroAsNoFilter(t *testing.T) {
	for name, raw := range map[string]any{"json zero": float64(0), "string zero": "0", "null": nil} {
		t.Run(name, func(t *testing.T) {
			got, errResult := optionalInt4Filter(requestWith(map[string]any{"round": raw}), roundParam)
			require.Nil(t, errResult)
			assert.Equal(t, pgtype.Int4{}, got)
		})
	}

	got, errResult := optionalInt4Filter(requestWith(map[string]any{"round": float64(lastPlayoffRound)}), roundParam)
	require.Nil(t, errResult)
	assert.Equal(t, pgtype.Int4{Int32: lastPlayoffRound, Valid: true}, got)

	for name, raw := range map[string]any{"too high": float64(lastPlayoffRound + 1), "negative": float64(-1), "fractional": 0.5} {
		t.Run(name, func(t *testing.T) {
			_, errResult := optionalInt4Filter(requestWith(map[string]any{"round": raw}), roundParam)
			require.NotNil(t, errResult)
			assert.Contains(t, resultText(t, errResult), "want an integer from 1 to 4")
		})
	}
}

func TestLimitOrDefault(t *testing.T) {
	const fallback int32 = 100
	tests := map[string]struct {
		args map[string]any
		want int32
	}{
		"absent":   {args: map[string]any{}, want: fallback},
		"explicit": {args: map[string]any{"limit": float64(7)}, want: 7},
		"zero":     {args: map[string]any{"limit": float64(0)}, want: 0},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, errResult := limitOrDefault(requestWith(tc.args), fallback)
			require.Nil(t, errResult)
			assert.Equal(t, tc.want, got)
		})
	}

	_, errResult := limitOrDefault(requestWith(map[string]any{"limit": float64(math.MaxInt32 + 1)}), fallback)
	require.NotNil(t, errResult)
}

func TestRequireIntSlice(t *testing.T) {
	got, errResult := requireIntSlice[int64](requestWith(map[string]any{"ids": []any{float64(1), "2"}}), intArg{name: "ids", min: 1, max: 10})
	require.Nil(t, errResult)
	assert.Equal(t, []int64{1, 2}, got)

	tests := map[string]struct {
		raw     any
		wantErr string
	}{
		"fractional item": {raw: []any{float64(1), 2.5}, wantErr: "invalid ids[1] 2.5: want an integer from 1 to 10"},
		"item too large":  {raw: []any{float64(11)}, wantErr: "invalid ids[0] 11: want an integer from 1 to 10"},
		"not an array":    {raw: float64(1), wantErr: "invalid ids 1: want an array of integers"},
		"string":          {raw: "1,2", wantErr: `invalid ids "1,2": want an array of integers`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, errResult := requireIntSlice[int64](requestWith(map[string]any{"ids": tc.raw}), intArg{name: "ids", min: 1, max: 10})
			require.NotNil(t, errResult)
			assert.Equal(t, tc.wantErr, resultText(t, errResult))
		})
	}

	_, errResult = requireIntSlice[int64](requestWith(map[string]any{}), intArg{name: "ids", min: 1, max: 10})
	require.NotNil(t, errResult)
	assert.Equal(t, "ids is required", resultText(t, errResult))
}

func TestToolResultHelpersKeepShape(t *testing.T) {
	failed, err := toolResult([]int{}, errors.New("boom"))
	require.NoError(t, err)
	assert.True(t, failed.IsError)
	assert.Equal(t, "boom", resultText(t, failed))

	failed, err = jsonResult(struct{}{}, errors.New("boom"))
	require.NoError(t, err)
	assert.True(t, failed.IsError)
	assert.Equal(t, "boom", resultText(t, failed))

	ok, err := jsonResult(map[string]int{"a": 1}, nil)
	require.NoError(t, err)
	assert.False(t, ok.IsError)
	assert.JSONEq(t, `{"a":1}`, resultText(t, ok))
}

func TestOptionalDate(t *testing.T) {
	d, errResult := optionalDate(requestWith(map[string]any{}), "start_date")
	require.Nil(t, errResult)
	assert.False(t, d.Valid)

	d, errResult = optionalDate(requestWith(map[string]any{"start_date": "2025-10-08"}), "start_date")
	require.Nil(t, errResult)
	assert.True(t, d.Valid)

	d, errResult = optionalDate(requestWith(map[string]any{"start_date": ""}), "start_date")
	require.Nil(t, errResult)
	assert.False(t, d.Valid)

	_, errResult = optionalDate(requestWith(map[string]any{"start_date": "10/08/2025"}), "start_date")
	require.NotNil(t, errResult)
	assert.Equal(t, `invalid start_date "10/08/2025": want a date as YYYY-MM-DD`, resultText(t, errResult))
}

func TestOptionalBool(t *testing.T) {
	const name = "unmatched_only"
	for raw, want := range map[any]bool{true: true, false: false, "true": true, "FALSE": false, "1": true} {
		got, errResult := optionalBool(requestWith(map[string]any{name: raw}), name)
		require.Nil(t, errResult, "%v", raw)
		assert.Equal(t, want, got, "%v", raw)
	}
	for _, args := range []map[string]any{{}, {name: nil}} {
		got, errResult := optionalBool(requestWith(args), name)
		require.Nil(t, errResult)
		assert.False(t, got, "absent or null is false")
	}
	for _, raw := range []any{"yes", float64(1), ""} {
		_, errResult := optionalBool(requestWith(map[string]any{name: raw}), name)
		require.NotNil(t, errResult, "%v must be refused, not read as false", raw)
		assert.Contains(t, resultText(t, errResult), "invalid "+name)
	}
}

func TestOptionalString(t *testing.T) {
	const name = "positions"
	got, errResult := optionalString(requestWith(map[string]any{name: "C,LW"}), name)
	require.Nil(t, errResult)
	assert.Equal(t, "C,LW", got)

	got, errResult = optionalString(requestWith(map[string]any{name: nil}), name)
	require.Nil(t, errResult)
	assert.Empty(t, got)

	_, errResult = optionalString(requestWith(map[string]any{name: []any{"C"}}), name)
	require.NotNil(t, errResult, "an array must be refused, not read as no filter")
	assert.Equal(t, "invalid positions [C]: want a string", resultText(t, errResult))
}

// TestParseDateRejectsWhatPgtypeAccepts covers values pgtype.Date.Scan
// would have taken or normalized to another day.
func TestParseDateRejectsWhatPgtypeAccepts(t *testing.T) {
	tests := map[string]struct {
		raw  any
		want string
	}{
		"no such day":    {raw: "2025-02-30", want: `invalid start_date "2025-02-30": want a date as YYYY-MM-DD`},
		"infinity":       {raw: "infinity", want: `invalid start_date "infinity": want a date as YYYY-MM-DD`},
		"BC suffix":      {raw: "2025-10-08 BC", want: `invalid start_date "2025-10-08 BC": want a date as YYYY-MM-DD`},
		"five-digit":     {raw: "12025-10-08", want: `invalid start_date "12025-10-08": want a date as YYYY-MM-DD`},
		"unpadded":       {raw: "2025-1-8", want: `invalid start_date "2025-1-8": want a date as YYYY-MM-DD`},
		"with time":      {raw: "2025-10-08T00:00:00Z", want: `invalid start_date "2025-10-08T00:00:00Z": want a date as YYYY-MM-DD`},
		"number":         {raw: float64(20251008), want: "invalid start_date 20251008: want a date as YYYY-MM-DD"},
		"echo is capped": {raw: strings.Repeat("9", 2*maxEchoedArgumentLength), want: `invalid start_date "` + strings.Repeat("9", maxEchoedArgumentLength-1) + `...: want a date as YYYY-MM-DD`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, errResult := parseDate("start_date", tc.raw)
			require.NotNil(t, errResult)
			assert.Equal(t, tc.want, resultText(t, errResult))
		})
	}

	d, errResult := parseDate("start_date", "2024-02-29")
	require.Nil(t, errResult)
	assert.Equal(t, pgtype.Date{Time: time.Date(2024, time.February, 29, 0, 0, 0, 0, time.UTC), Valid: true}, d)
}

func TestRequireDate(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"date": "2025-01-15"}
	d, errResult := requireDate(req)
	require.Nil(t, errResult)
	assert.True(t, d.Valid)
	assert.Equal(t, "2025-01-15", d.Time.Format("2006-01-02"))

	req.Params.Arguments = map[string]any{"date": "15/01/2025"}
	_, errResult = requireDate(req)
	require.NotNil(t, errResult)
	assert.Equal(t, `invalid date "15/01/2025": want a date as YYYY-MM-DD`, resultText(t, errResult))

	req.Params.Arguments = map[string]any{}
	_, errResult = requireDate(req)
	require.NotNil(t, errResult)
	assert.Equal(t, "date is required", resultText(t, errResult))
}

// truncatingGetters are the SDK request getters that truncate fractional
// numbers (or read integers without destination checks).
var truncatingGetters = []string{".GetInt(", ".RequireInt(", ".GetIntSlice(", ".RequireIntSlice("}

// TestToolsReadIntegersOnlyThroughStrictHelpers keeps new tools off the
// SDK's truncating integer getters; integer arguments go through
// requireInt, optionalInt and friends.
func TestToolsReadIntegersOnlyThroughStrictHelpers(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	require.NoError(t, err)
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		content, err := os.ReadFile(source)
		require.NoError(t, err)
		for _, getter := range truncatingGetters {
			assert.NotContains(t, string(content), getter, "%s: read integer arguments with requireInt/optionalInt", source)
		}
	}
}
