package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sperano/puckdb/internal/graph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Formatter tests pin the on-screen layout that scripts and humans will
// read; the feed-loop tests drive runSimTail with a fake feeder. The
// end-to-end run against the real API and PostgreSQL is in
// sim_client_pg_test.go.

// tailTestInterval keeps the follow loop fast in tests.
const tailTestInterval = time.Millisecond

// feedRequest is one SimTurnFeed call a fakeFeeder saw; -1 stands for nil.
type feedRequest struct {
	poolID, afterID, limit int
}

// fakeFeeder returns pages in order. Once they run out it cancels the
// context and fails the request with the context error, as an in-flight
// HTTP request does on Ctrl-C.
type fakeFeeder struct {
	pages    []tailPage
	err      error
	cancel   context.CancelFunc
	requests []feedRequest
}

func intOrNone(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

func (f *fakeFeeder) SimTurnFeed(ctx context.Context, poolID, afterID *int, limit int) (tailPage, error) {
	f.requests = append(f.requests, feedRequest{intOrNone(poolID), intOrNone(afterID), limit})
	if f.err != nil {
		return tailPage{}, f.err
	}
	if len(f.pages) == 0 {
		f.cancel()
		return tailPage{}, ctx.Err()
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}

func tailTestEntry(id int64) tailEntry {
	return tailEntry{
		Turn:  tailTurn{ID: id, PoolID: 3, TeamName: "t", Phase: "daily", Status: "ok", CompletedAt: time.Now(), ToolCallCount: 1},
		Calls: []tailToolCall{{ToolName: "add_player", Arguments: "{}", Outcome: "accepted", PlayerNames: []string{"P"}}},
	}
}

// The backlog is one latest-page request; following pages from its
// lastId with the server's maximum page size until hasMore is false; a
// Ctrl-C during a request ends the tail without an error.
func TestRunSimTail_BacklogThenPagedFollow(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pages := []tailPage{
		{Entries: []tailEntry{tailTestEntry(6), tailTestEntry(7)}, LastID: 7},
		{Entries: []tailEntry{tailTestEntry(8)}, LastID: 107, HasMore: true},
		{Entries: []tailEntry{tailTestEntry(108)}, LastID: 110},
		{LastID: 110},
	}
	feeder := &fakeFeeder{pages: pages, cancel: cancel}
	var out bytes.Buffer

	require.NoError(t, runSimTail(ctx, feeder, &out, 3, tailTestInterval))

	pageMax := graph.SimTurnFeedMaxLimit
	assert.Equal(t, []feedRequest{
		{3, -1, tailBacklog}, {3, 7, pageMax}, {3, 107, pageMax}, {3, 110, pageMax}, {3, 110, pageMax},
	}, feeder.requests)
	var want strings.Builder
	for _, p := range pages {
		for _, e := range p.Entries {
			want.WriteString(formatTailRow(e.Turn, e.Calls))
		}
	}
	assert.Equal(t, want.String(), out.String())
}

func TestRunSimTail_AllPoolsSendsNoPoolID(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feeder := &fakeFeeder{pages: []tailPage{{}}, cancel: cancel}
	require.NoError(t, runSimTail(ctx, feeder, &bytes.Buffer{}, 0, tailTestInterval))
	require.NotEmpty(t, feeder.requests)
	assert.Equal(t, feedRequest{-1, -1, tailBacklog}, feeder.requests[0])
	assert.Equal(t, feedRequest{-1, 0, graph.SimTurnFeedMaxLimit}, feeder.requests[1], "an empty backlog follows from 0")
}

func TestRunSimTail_FeedErrorEndsTail(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	err := runSimTail(context.Background(), &fakeFeeder{err: boom}, &bytes.Buffer{}, 0, tailTestInterval)
	require.ErrorIs(t, err, boom)
}

// The API's argument preview, even after its rune-boundary cut backs off
// up to utf8.UTFMax-1 bytes, must stay longer than the CLI's preview: then
// every argument the server cut still gets the CLI's "..." from the same
// leading bytes it cut when it read arguments_raw itself.
func TestTailArgPreviewFitsServerPreview(t *testing.T) {
	t.Parallel()
	assert.Greater(t, graph.SimTurnFeedMaxArgumentsBytes-(utf8.UTFMax-1), tailArgPreviewMax)
	assert.LessOrEqual(t, tailBacklog, graph.SimTurnFeedMaxLimit)
}

func TestFormatTurnHeader_OkDailyWithDate(t *testing.T) {
	t.Parallel()
	date := time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC)
	completed := time.Date(2024, 11, 15, 14, 32, 1, 0, time.Local)
	t.Helper()
	got := formatTurnHeader(tailTurn{
		ID:          42,
		PoolID:      3,
		TeamName:    "sonnet",
		Phase:       "daily",
		SimDate:     &date,
		Status:      "ok",
		Rounds:      4,
		CostUSD:     0.018,
		CompletedAt: completed,
	})
	assert.Contains(t, got, "[14:32:01]")
	assert.Contains(t, got, "pool=3")
	assert.Contains(t, got, "sonnet")
	assert.Contains(t, got, "daily")
	assert.Contains(t, got, "2024-11-15")
	assert.Contains(t, got, "ok")
	assert.Contains(t, got, "4r")
	assert.Contains(t, got, "$0.018")
	assert.NotContains(t, got, "err=", "no err= suffix when status=ok")
}

func TestFormatTurnHeader_TeamNamePhaseNullDate(t *testing.T) {
	t.Parallel()
	got := formatTurnHeader(tailTurn{
		PoolID:      1,
		TeamName:    "haiku",
		Phase:       "team_name",
		SimDate:     nil,
		Status:      "ok",
		Rounds:      1,
		CompletedAt: time.Now(),
	})
	// 10-wide blank placeholder where a date would normally appear so
	// columns stay aligned across mixed-phase output.
	assert.Contains(t, got, "team_name")
	assert.NotContains(t, got, "0001-01-01", "nil date must not render as Go zero time")
}

func TestFormatTurnHeader_ErroredAppendsErrKind(t *testing.T) {
	t.Parallel()
	kind := "parse_error"
	got := formatTurnHeader(tailTurn{
		PoolID:      2,
		TeamName:    "llama",
		Phase:       "draft",
		SimDate:     new(time.Date(2024, 10, 12, 0, 0, 0, 0, time.UTC)),
		Status:      "errored",
		ErrorKind:   &kind,
		Rounds:      1,
		CompletedAt: time.Now(),
	})
	assert.Contains(t, got, "err=parse_error")
}

func TestFormatToolCallLine_AcceptedWithTx(t *testing.T) {
	t.Parallel()
	txID := int64(10421)
	got := formatToolCallLine(tailToolCall{
		RoundIndex:  0,
		Sequence:    1,
		ToolName:    "drop_player",
		Arguments:   `{"player_id":8478402}`,
		Outcome:     "accepted",
		AppliedTxID: &txID,
	})
	assert.True(t, strings.HasPrefix(got, "           "), "tool-call line must be indented")
	assert.Contains(t, got, "r0/1")
	assert.Contains(t, got, "drop_player")
	assert.Contains(t, got, `{"player_id":8478402}`)
	assert.Contains(t, got, "accepted")
	assert.Contains(t, got, "tx=10421")
}

func TestFormatToolCallLine_RejectedWithReason(t *testing.T) {
	t.Parallel()
	reason := "invalid_slot"
	got := formatToolCallLine(tailToolCall{
		RoundIndex:    1,
		Sequence:      0,
		ToolName:      "set_lineup",
		Arguments:     "{}",
		Outcome:       "validation_rejected",
		FailureReason: &reason,
	})
	assert.Contains(t, got, "validation_rejected")
	assert.Contains(t, got, "reason=invalid_slot")
	assert.NotContains(t, got, "tx=", "rejected calls have no applied tx")
}

func TestFormatToolCallLine_RecoveredNameRendersArrow(t *testing.T) {
	t.Parallel()
	recovered := "add_player"
	got := formatToolCallLine(tailToolCall{
		ToolName:      "addPlayer",
		RecoveredName: &recovered,
		Arguments:     "{}",
		Outcome:       "accepted",
	})
	assert.Contains(t, got, "addPlayer→add_player")
}

func TestTruncateArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"short passes through", `{"a":1}`, 80, `{"a":1}`},
		{"collapses newlines", "{\n  \"a\": 1\n}", 80, `{ "a": 1 }`},
		{"truncates with ellipsis", strings.Repeat("x", 100), 20, strings.Repeat("x", 17) + "..."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, truncateArgs(tc.in, tc.max))
		})
	}
}

func TestFormatTailRow_TurnWithCalls(t *testing.T) {
	t.Parallel()
	date := time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC)
	txID := int64(10421)
	out := formatTailRow(
		tailTurn{
			PoolID:      3,
			TeamName:    "sonnet",
			Phase:       "daily",
			SimDate:     &date,
			Status:      "ok",
			Rounds:      2,
			CostUSD:     0.018,
			CompletedAt: time.Date(2024, 11, 15, 14, 32, 1, 0, time.Local),
		},
		[]tailToolCall{
			{RoundIndex: 0, Sequence: 0, ToolName: "drop_player", Arguments: `{"player_id":1}`, Outcome: "accepted", AppliedTxID: &txID},
			{RoundIndex: 0, Sequence: 1, ToolName: "add_player", Arguments: `{"player_id":2}`, Outcome: "accepted"},
		},
	)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	assert.Len(t, lines, 3, "1 header + 2 tool calls")
	assert.True(t, strings.HasPrefix(lines[0], "[14:32:01]"))
	assert.True(t, strings.HasPrefix(lines[1], "           r0/0"))
	assert.True(t, strings.HasPrefix(lines[2], "           r0/1"))
}

func TestFormatTailRow_TurnWithNoCalls(t *testing.T) {
	t.Parallel()
	// A turn can have zero tool calls (status=skipped) — render the
	// header only, no orphan blank line below.
	out := formatTailRow(
		tailTurn{
			PoolID:      1,
			TeamName:    "haiku",
			Phase:       "daily",
			Status:      "skipped",
			CompletedAt: time.Now(),
		},
		nil,
	)
	assert.Equal(t, 1, strings.Count(out, "\n"), "exactly one trailing newline for a header-only row")
}

func TestFormatToolCallLine_DecoratesWithPlayerNames(t *testing.T) {
	t.Parallel()
	got := formatToolCallLine(tailToolCall{
		RoundIndex:  0,
		Sequence:    0,
		ToolName:    "add_player",
		Arguments:   `{"player_id":8478402,"drop_player_id":8477934}`,
		Outcome:     "accepted",
		PlayerNames: []string{"Leon Draisaitl", "Connor McDavid"},
	})
	// The server sends the names in argument order; both appear,
	// separated by ", " inside one bracket pair, after the outcome.
	assert.True(t, strings.HasSuffix(got, "accepted  [Leon Draisaitl, Connor McDavid]"), got)
}

func TestFormatToolCallLine_NoNamesNoDecoration(t *testing.T) {
	t.Parallel()
	for _, names := range [][]string{nil, {}} {
		got := formatToolCallLine(tailToolCall{
			ToolName:    "drop_player",
			Arguments:   `{"player_id":99999999}`,
			Outcome:     "accepted",
			PlayerNames: names,
		})
		assert.NotContains(t, got, "[")
	}
}

func TestFormatTailRow_OmittedToolCallsLine(t *testing.T) {
	t.Parallel()
	turn := tailTurn{PoolID: 1, TeamName: "t", Phase: "daily", Status: "ok", CompletedAt: time.Now(), ToolCallCount: 35}
	calls := []tailToolCall{{ToolName: "a", Arguments: "{}", Outcome: "accepted"}}
	lines := strings.Split(strings.TrimRight(formatTailRow(turn, calls), "\n"), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "           … 34 more tool calls", lines[2])

	turn.ToolCallCount = len(calls)
	assert.Equal(t, 2, strings.Count(formatTailRow(turn, calls), "\n"), "no extra line when every call is listed")
}

// A pool ID is a flag, never a positional argument: `sim tail 3` used to
// tail every pool.
func TestCmdSimTail_RejectsPositionalPoolID(t *testing.T) {
	t.Parallel()
	cmd := cmdSimTail()
	require.Error(t, cmd.Args(cmd, []string{"3"}))
	require.NoError(t, cmd.Args(cmd, nil))
}

func TestCmdSimTail_RejectsNegativePool(t *testing.T) {
	t.Parallel()
	cmd := cmdSimTail()
	require.NoError(t, cmd.Flags().Set("pool", "-2"))
	err := cmd.RunE(cmd, nil)
	require.EqualError(t, err, "--pool must be a pool ID, or 0 for all pools; got -2")
}
