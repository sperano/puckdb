package mauricereport

import (
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/fixtures/mauricefixtures"
	"github.com/stretchr/testify/assert"
)

// conv1Usage is every call of conv1: its turns' rounds and its title call.
var conv1Usage = ericActivity.Usage

// sonnetTurnUsage is the two sonnet rounds of conv1's turn 0.
var sonnetTurnUsage = Usage{Calls: 2,
	InputTokens: reported(300, 2, 0), OutputTokens: reported(50, 2, 0),
	CacheCreationInputTokens: reported(50, 1, 1), CacheReadInputTokens: reported(40, 1, 1),
	CallLatency: latency(2, 3.5, 3.5, 3.95, 4)}

// failedTurnUsage is the failed qwen round of conv1's turn 1: no count
// reported, no succeeded call.
var failedTurnUsage = Usage{Calls: 1, FailedCalls: 1,
	InputTokens: reported(0, 0, 1), OutputTokens: reported(0, 0, 1),
	CacheCreationInputTokens: reported(0, 0, 1), CacheReadInputTokens: reported(0, 0, 1)}

// A filter includes the conversations the conversation list returns for
// it, only their turns meeting every turn filter, those turns' calls, and
// the title calls that start in the interval and match provider/model when
// no status or tool filter is set.
func TestOverviewFiltersIncludeMatchingTurnsAndCalls(t *testing.T) {
	store, seed := openStore(t)
	seedOverviewWorld(seed)
	tests := []struct {
		name        string
		filter      ConversationFilter
		activeUsers int64
		want        Activity
	}{
		{
			name: "status: the failed turn only, no title call", filter: ConversationFilter{Statuses: []TurnStatus{TurnFailed}},
			activeUsers: 1,
			want: Activity{Conversations: 1, Turns: TurnCounts{Failed: 1},
				FirstTurnStartedAt: at(t0.Add(12 * time.Second)), LastTurnStartedAt: at(t0.Add(12 * time.Second)),
				Usage: failedTurnUsage, ProcessingSeconds: 6},
		},
		{
			name: "model: the sonnet turn and the sonnet title call", filter: ConversationFilter{Model: modelSonnet},
			activeUsers: 1,
			want: Activity{Conversations: 1, Turns: TurnCounts{Succeeded: 1},
				FirstTurnStartedAt: at(t0), LastTurnStartedAt: at(t0), TurnLatency: latency(1, 10, 10, 10, 10),
				Usage: Usage{Calls: 3,
					InputTokens: reported(310, 3, 0), OutputTokens: reported(55, 3, 0),
					CacheCreationInputTokens: reported(50, 1, 2), CacheReadInputTokens: reported(40, 1, 2),
					CallLatency: latency(3, 3, 3, 3.9, 4)},
				TitleGeneration: titleUsage, ProcessingSeconds: 12},
		},
		{
			name: "model: the qwen turns, not the sonnet title call", filter: ConversationFilter{Provider: providerOllama, Model: modelQwen},
			activeUsers: 2,
			want: Activity{Conversations: 2, Turns: TurnCounts{Succeeded: 1, Failed: 1},
				FirstTurnStartedAt: at(bobTurnStart), LastTurnStartedAt: at(t0.Add(12 * time.Second)),
				TurnLatency: latency(1, 1, 1, 1, 1),
				Usage: Usage{Calls: 2, FailedCalls: 1,
					InputTokens: reported(7, 1, 1), OutputTokens: reported(3, 1, 1),
					CacheCreationInputTokens: reported(0, 1, 1), CacheReadInputTokens: reported(0, 1, 1),
					CallLatency: latency(1, 1, 1, 1, 1)},
				ProcessingSeconds: 7},
		},
		{
			name: "tool: the turn that called it, no title call", filter: ConversationFilter{ToolName: toolPlayer},
			activeUsers: 1,
			want: Activity{Conversations: 1, Turns: TurnCounts{Succeeded: 1},
				FirstTurnStartedAt: at(t0), LastTurnStartedAt: at(t0), TurnLatency: latency(1, 10, 10, 10, 10),
				Usage: sonnetTurnUsage, ProcessingSeconds: 10},
		},
		{
			name: "search: every turn and call of the matching conversation", filter: ConversationFilter{Search: "habs"},
			activeUsers: 1,
			want: Activity{Conversations: 1, Turns: TurnCounts{Succeeded: 1, Failed: 1},
				FirstTurnStartedAt: at(t0), LastTurnStartedAt: at(t0.Add(12 * time.Second)),
				TurnLatency: latency(1, 10, 10, 10, 10), Usage: conv1Usage, TitleGeneration: titleUsage,
				ProcessingSeconds: 17},
		},
		{
			name: "interval: t0's UTC day leaves bob's turn a second before it out", filter: ConversationFilter{From: "2026-11-10", To: "2026-11-10"},
			activeUsers: 1, want: ericActivity,
		},
		{
			name: "interval: the day before is bob's turn only", filter: ConversationFilter{From: "2026-11-09", To: "2026-11-09"},
			activeUsers: 1, want: bobActivity,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := overview(t, store, tt.filter)
			assert.Equal(t, int64(3), got.Users, "the scope does not depend on the filter")
			assert.Equal(t, tt.activeUsers, got.ActiveUsers)
			assertActivity(t, tt.want, got.Activity)
		})
	}
}

// From is inclusive at 00:00 UTC and To ends before the next day's 00:00
// UTC, for turns and title calls alike; a title call outside the interval
// is left out even when its conversation is included.
func TestOverviewIntervalBoundaries(t *testing.T) {
	store, s := newConvSeed(t)
	midnight := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	conv := s.conversation("boundary", midnight)
	nextMidnight := midnight.Add(24 * time.Hour)
	s.turn(conv, 0, mauricefixtures.StatusSucceeded, midnight)
	s.turn(conv, 1, mauricefixtures.StatusSucceeded, nextMidnight)
	s.seed.Call(mauricefixtures.Call{ConversationID: conv, Provider: providerAnthropic, Model: modelSonnet,
		StartedAt: nextMidnight, InputTokens: tokens(10)})

	tests := []struct {
		from, to      string
		turns, titles int64
	}{
		{"2026-11-10", "2026-11-10", 1, 0},
		{"2026-11-10", "2026-11-11", 2, 1},
		{"2026-11-11", "2026-11-11", 1, 1},
		{"2026-11-09", "2026-11-09", 0, 0},
		{"2026-11-11", "", 1, 1},
		{"2026-11-12", "", 0, 0},
		{"", "2026-11-10", 1, 0},
	}
	for _, tt := range tests {
		got := overview(t, store, ConversationFilter{From: tt.from, To: tt.to})
		assert.Equal(t, tt.turns, got.Activity.Turns.Total(), "turns in [%s, %s]", tt.from, tt.to)
		assert.Equal(t, tt.titles, got.Activity.TitleGeneration.Calls, "title calls in [%s, %s]", tt.from, tt.to)
	}
}

// A turn filter selects turns, not calls: an included turn brings every one
// of its calls, those of another model included.
func TestOverviewTurnFilterKeepsTheTurnsOtherCalls(t *testing.T) {
	store, s := newConvSeed(t)
	conv := s.conversation("mixed", t0)
	turn := s.turn(conv, 0, mauricefixtures.StatusSucceeded, t0)
	s.seed.Call(mauricefixtures.Call{ConversationID: conv, TurnID: turn, Provider: providerOllama, Model: modelQwen,
		StartedAt: t0, Duration: 2 * time.Second, InputTokens: tokens(7)})
	s.seed.Call(mauricefixtures.Call{ConversationID: conv, TurnID: turn, Round: 1, Provider: providerAnthropic, Model: modelSonnet,
		StartedAt: t0.Add(2 * time.Second), Duration: 4 * time.Second, InputTokens: tokens(30)})
	want := Activity{Conversations: 1, Turns: TurnCounts{Succeeded: 1},
		FirstTurnStartedAt: at(t0), LastTurnStartedAt: at(t0), TurnLatency: latency(1, 1, 1, 1, 1),
		Usage: Usage{Calls: 2,
			InputTokens: reported(37, 2, 0), OutputTokens: reported(0, 0, 2),
			CacheCreationInputTokens: reported(0, 0, 2), CacheReadInputTokens: reported(0, 0, 2),
			CallLatency: latency(2, 3, 3, 3.9, 4)},
		ProcessingSeconds: 1}

	for _, filter := range []ConversationFilter{
		{Model: modelSonnet},
		{Provider: providerOllama, Model: modelQwen},
	} {
		t.Run(filter.Provider+"/"+filter.Model, func(t *testing.T) {
			assertActivity(t, want, overview(t, store, filter).Activity)
		})
	}
}
