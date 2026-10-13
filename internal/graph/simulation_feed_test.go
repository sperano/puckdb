package graph

import (
	"context"
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noSQL is a DBTX that fails the test on any statement: the argument
// checks must reject a request before it reaches the database.
type noSQL struct{ t *testing.T }

func (d noSQL) fail(sql string) {
	d.t.Errorf("unexpected SQL: %s", sql)
}

func (d noSQL) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	d.fail(sql)
	return pgconn.CommandTag{}, nil
}

func (d noSQL) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	d.fail(sql)
	return nil, pgx.ErrNoRows
}

func (d noSQL) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	d.fail(sql)
	return nil
}

func (d noSQL) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	d.fail("COPY")
	return 0, nil
}

func (d noSQL) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	d.fail("batch")
	return nil
}

func TestParseSimTurnFeedArgs_Accepts(t *testing.T) {
	t.Parallel()
	q, err := parseSimTurnFeedArgs(nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, simTurnFeedQuery{limit: simTurnFeedDefaultLimit}, q)

	pool, after, limit := math.MaxInt32, 0, SimTurnFeedMaxLimit
	q, err = parseSimTurnFeedArgs(&pool, &after, &limit)
	require.NoError(t, err)
	assert.Equal(t, simTurnFeedQuery{poolID: math.MaxInt32, afterID: 0, hasAfter: true, limit: SimTurnFeedMaxLimit}, q)

	pool, after, limit = 1, math.MaxInt32, 1
	q, err = parseSimTurnFeedArgs(&pool, &after, &limit)
	require.NoError(t, err)
	assert.Equal(t, simTurnFeedQuery{poolID: 1, afterID: math.MaxInt32, hasAfter: true, limit: 1}, q)
}

// Out-of-range values are errors before any SQL; none is clamped or
// replaced by the default, and none wraps when narrowed to int32.
func TestSimTurnFeed_RejectsOutOfRangeArgumentsBeforeSQL(t *testing.T) {
	t.Parallel()
	r := &Resolver{Queries: sqlcdb.New(noSQL{t})}
	ptr := func(v int) *int { return &v }
	cases := []struct {
		name                 string
		poolID, afterID, lim *int
		wantErr              string
	}{
		{"pool zero", ptr(0), nil, nil, "poolId must be between 1 and 2147483647, got 0"},
		{"pool negative", ptr(-1), nil, nil, "poolId must be between 1 and 2147483647, got -1"},
		{"pool wraps int32", ptr(math.MaxInt32 + 1), nil, nil, "poolId must be between 1 and 2147483647, got 2147483648"},
		{"after negative", nil, ptr(-1), nil, "afterId must be between 0 and 2147483647, got -1"},
		{"after wraps int32", nil, ptr(math.MaxInt32 + 1), nil, "afterId must be between 0 and 2147483647, got 2147483648"},
		{"limit zero", nil, nil, ptr(0), "limit must be between 1 and 100, got 0"},
		{"limit above max", nil, nil, ptr(SimTurnFeedMaxLimit + 1), "limit must be between 1 and 100, got 101"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, err := r.simTurnFeed(context.Background(), tc.poolID, tc.afterID, tc.lim)
			require.EqualError(t, err, tc.wantErr)
			assert.Nil(t, page)
		})
	}
}

func TestSimDraftPicks_RejectsOutOfRangePoolBeforeSQL(t *testing.T) {
	t.Parallel()
	r := &Resolver{Queries: sqlcdb.New(noSQL{t})}
	for _, id := range []int{0, -1, math.MaxInt32 + 1} {
		picks, err := r.simDraftPicks(context.Background(), id)
		require.ErrorContains(t, err, "poolId must be between 1 and 2147483647")
		assert.Nil(t, picks)
	}
}

func TestExtractSimPlayerIDs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want []int64
	}{
		{"draft_player", `{"player_id":8478402,"reason":"x"}`, []int64{8478402}},
		{"sorted keys put drop first", `{"player_id":1,"drop_player_id":2}`, []int64{2, 1}},
		{"set_lineup nested", `{"slots":[{"player_id":1,"slot":"C"},{"player_id":2,"slot":"LW"}]}`, []int64{1, 2}},
		{"repeated ids kept for the caller", `{"slots":[{"player_id":1},{"player_id":1}]}`, []int64{1, 1}},
		{"beyond float64 precision", `{"player_id":9007199254740993}`, []int64{9007199254740993}},
		{"non-integer is not an id", `{"player_id":8478402.7}`, nil},
		{"integral float literal is not an id", `{"player_id":8478402.0}`, nil},
		{"exponent is not an id", `{"player_id":1e6}`, nil},
		{"string is not an id", `{"player_id":"8478402"}`, nil},
		{"other keys ignored", `{"team_name":"X","id":5}`, nil},
		{"trailing data", `{"player_id":1} {"player_id":2}`, nil},
		{"trailing garbage", `{"player_id":1}x`, nil},
		{"trailing whitespace", "{\"player_id\":1}\n ", []int64{1}},
		{"malformed json", `not json`, nil},
		{"truncated json", `{"player_id":`, nil},
		{"empty", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, extractSimPlayerIDs(tc.in))
		})
	}
}

func TestCollapseAndCutArguments(t *testing.T) {
	t.Parallel()
	assert.Equal(t, `{ "a": 1 }`, collapseAndCutArguments("{\n  \"a\":\t1\n}", 80))
	assert.Equal(t, "abcd", collapseAndCutArguments("abcd", 4), "exactly maxBytes is kept whole")
	assert.Equal(t, "abc", collapseAndCutArguments("abcd", 3))
	// "é" is 2 bytes: a cut at an odd byte would split it, so the cut moves
	// back to the rune start.
	in := "x" + strings.Repeat("é", 10)
	got := collapseAndCutArguments(in, 4)
	assert.Equal(t, "xé", got)
	assert.True(t, utf8.ValidString(got))
	assert.Equal(t, "xéé", collapseAndCutArguments(in, 5))
	// The collapse runs first, so the cut counts collapsed bytes.
	assert.Equal(t, "a b", collapseAndCutArguments("a \n\n  b c", 3))
}

func TestSimNamesFor(t *testing.T) {
	t.Parallel()
	names := map[int64]string{1: "One", 2: "Two"}
	assert.Equal(t, []string{"Two", "One"}, simNamesFor([]int64{2, 9, 1, 2, 1}, names))
	got := simNamesFor(nil, names)
	assert.NotNil(t, got, "playerNames is a non-null list")
	assert.Empty(t, got)
	assert.Empty(t, simNamesFor([]int64{1}, nil))
}
