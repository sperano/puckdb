package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Pure formatter tests — no DB. The tail command's DB plumbing is
// exercised by the integration test suite; here we pin the on-screen
// layout that scripts and humans will read.

func TestFormatTurnHeader_OkDailyWithDate(t *testing.T) {
	t.Parallel()
	date := time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC)
	completed := time.Date(2024, 11, 15, 14, 32, 1, 0, time.Local)
	t.Helper()
	got := formatTurnHeader(tailTurn{
		ID:          42,
		PoolID:      3,
		TeamName:   "sonnet",
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
		TeamName:   "haiku",
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
		TeamName:   "llama",
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
		RoundIndex:   0,
		Sequence:     1,
		ToolName:     "drop_player",
		ArgumentsRaw: `{"player_id":8478402}`,
		Outcome:      "accepted",
		AppliedTxID:  &txID,
	}, nil)
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
		ArgumentsRaw:  "{}",
		Outcome:       "validation_rejected",
		FailureReason: &reason,
	}, nil)
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
		ArgumentsRaw:  "{}",
		Outcome:       "accepted",
	}, nil)
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
			TeamName:   "sonnet",
			Phase:       "daily",
			SimDate:     &date,
			Status:      "ok",
			Rounds:      2,
			CostUSD:     0.018,
			CompletedAt: time.Date(2024, 11, 15, 14, 32, 1, 0, time.Local),
		},
		[]tailToolCall{
			{RoundIndex: 0, Sequence: 0, ToolName: "drop_player", ArgumentsRaw: `{"player_id":1}`, Outcome: "accepted", AppliedTxID: &txID},
			{RoundIndex: 0, Sequence: 1, ToolName: "add_player", ArgumentsRaw: `{"player_id":2}`, Outcome: "accepted"},
		},
		nil,
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
			TeamName:   "haiku",
			Phase:       "daily",
			Status:      "skipped",
			CompletedAt: time.Now(),
		},
		nil,
		nil,
	)
	assert.Equal(t, 1, strings.Count(out, "\n"), "exactly one trailing newline for a header-only row")
}

func TestExtractPlayerIDs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want []int64
	}{
		{"draft_player", `{"player_id":8478402,"reason":"x"}`, []int64{8478402}},
		{"add_player with drop", `{"player_id":1,"drop_player_id":2}`, []int64{2, 1}},
		{"set_lineup nested", `{"slots":[{"player_id":1,"slot":"C"},{"player_id":2,"slot":"LW"}]}`, []int64{1, 2}},
		{"no player ids", `{"team_name":"X"}`, nil},
		{"malformed json", `not json`, nil},
		{"empty", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, extractPlayerIDs(tc.in))
		})
	}
}

func TestFormatToolCallLine_DecoratesWithPlayerNames(t *testing.T) {
	t.Parallel()
	names := map[int64]string{
		8478402: "Connor McDavid",
		8477934: "Leon Draisaitl",
	}
	got := formatToolCallLine(tailToolCall{
		RoundIndex:   0,
		Sequence:     0,
		ToolName:     "add_player",
		ArgumentsRaw: `{"player_id":8478402,"drop_player_id":8477934}`,
		Outcome:      "accepted",
	}, names)
	// drop_player_id comes first in sorted key order, then player_id;
	// both names must appear, separated by ", " inside one bracket pair.
	assert.Contains(t, got, "[Leon Draisaitl, Connor McDavid]")
}

func TestFormatToolCallLine_NoDecorationWhenIDsUnknown(t *testing.T) {
	t.Parallel()
	got := formatToolCallLine(tailToolCall{
		ToolName:     "drop_player",
		ArgumentsRaw: `{"player_id":99999999}`,
		Outcome:      "accepted",
	}, map[int64]string{1: "Some Player"})
	// 99999999 isn't in the map → no [...] suffix at all.
	assert.NotContains(t, got, "[")
}

func TestFormatToolCallLine_NilMapSkipsDecoration(t *testing.T) {
	t.Parallel()
	got := formatToolCallLine(tailToolCall{
		ToolName:     "drop_player",
		ArgumentsRaw: `{"player_id":1}`,
		Outcome:      "accepted",
	}, nil)
	assert.NotContains(t, got, "[")
}

