package mauricereport

import (
	"context"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/fixtures/mauricefixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// latencyDelta absorbs float rounding in the percentile comparisons.
const latencyDelta = 1e-9

var tokens = mauricefixtures.Tokens

// overviewWorld is the fixture of the overview tests, on instanceFull:
//
//   - eric (first seen t0-48h):
//   - conv1 "Habs" (t0): turn 0 succeeded at t0 for 10s, with two sonnet
//     rounds (4s and 3s, some counts unreported), a get_player tool call
//     and four messages; a sonnet title call at t0+11s for 2s; turn 1
//     failed at t0+12s for 6s (overlapping the title call) with a failed
//     qwen round reporting no tokens.
//   - conv2 (t0+1h): turn 0 cancelled at t0+1h for 2s, no call; turn 1
//     running since t0+1h+10s.
//   - conv3 (t0+2h, deleted): turn 0 succeeded at t0+2h for 5s with a
//     sonnet round of 1000/1000 tokens.
//   - bob (first seen t0-24h): conv4 (t0-24h), turn 0 succeeded at
//     2026-11-09T23:59:59Z for 1s with a qwen round reporting 7/3/0/0.
//   - carol (first seen t0): no conversation.
//
// and eric on instanceCrapettes, with a succeeded sonnet turn at t0 that
// must never be counted on instanceFull.
type overviewWorld struct {
	eric, bob, carol, publicEric string
	conv1, conv2, conv3, conv4   string
}

// bobTurnStart is one second before the UTC day of t0.
var bobTurnStart = time.Date(2026, 11, 9, 23, 59, 59, 0, time.UTC)

func seedOverviewWorld(seed *mauricefixtures.Seeder) overviewWorld {
	var w overviewWorld
	w.eric = seed.User(mauricefixtures.User{Instance: instanceFull, Username: "eric", FirstSeenAt: t0.Add(-48 * time.Hour)})
	w.bob = seed.User(mauricefixtures.User{Instance: instanceFull, Username: "bob", FirstSeenAt: t0.Add(-24 * time.Hour)})
	w.carol = seed.User(mauricefixtures.User{Instance: instanceFull, Username: "carol", FirstSeenAt: t0})

	w.conv1 = seed.Conversation(mauricefixtures.Conversation{UserID: w.eric, Title: "Habs", CreatedAt: t0})
	turn := seed.Turn(mauricefixtures.Turn{ConversationID: w.conv1, UserID: w.eric, Number: 0,
		Status: mauricefixtures.StatusSucceeded, StartedAt: t0, Duration: 10 * time.Second})
	call := seed.Call(mauricefixtures.Call{ConversationID: w.conv1, TurnID: turn, Round: 0, Provider: providerAnthropic,
		Model: modelSonnet, StartedAt: t0, Duration: 4 * time.Second,
		InputTokens: tokens(100), OutputTokens: tokens(20), CacheCreationInputTokens: tokens(50)})
	seed.ToolCall(mauricefixtures.ToolCall{TurnID: turn, CallID: call, Name: toolPlayer, ArgumentsRaw: "{}", Result: "ok", StartedAt: t0})
	seed.Call(mauricefixtures.Call{ConversationID: w.conv1, TurnID: turn, Round: 1, Provider: providerAnthropic,
		Model: modelSonnet, StartedAt: t0.Add(5 * time.Second), Duration: 3 * time.Second,
		InputTokens: tokens(200), OutputTokens: tokens(30), CacheReadInputTokens: tokens(40)})
	for i, role := range []string{"user", "assistant", "tool", "assistant"} {
		seed.Message(mauricefixtures.Message{ConversationID: w.conv1, TurnID: turn, Number: i, Role: role, Content: role})
	}
	seed.Call(mauricefixtures.Call{ConversationID: w.conv1, Provider: providerAnthropic, Model: modelSonnet,
		StartedAt: t0.Add(11 * time.Second), Duration: 2 * time.Second, InputTokens: tokens(10), OutputTokens: tokens(5)})
	failed := seed.Turn(mauricefixtures.Turn{ConversationID: w.conv1, UserID: w.eric, Number: 1,
		Status: mauricefixtures.StatusFailed, StartedAt: t0.Add(12 * time.Second), Duration: 6 * time.Second})
	seed.Call(mauricefixtures.Call{ConversationID: w.conv1, TurnID: failed, Provider: providerOllama, Model: modelQwen,
		StartedAt: t0.Add(12 * time.Second), Duration: 6 * time.Second, Failed: true})

	w.conv2 = seed.Conversation(mauricefixtures.Conversation{UserID: w.eric, Title: "Leafs", CreatedAt: t0.Add(time.Hour)})
	seed.Turn(mauricefixtures.Turn{ConversationID: w.conv2, UserID: w.eric, Number: 0,
		Status: mauricefixtures.StatusCancelled, StartedAt: t0.Add(time.Hour), Duration: 2 * time.Second})
	seed.Turn(mauricefixtures.Turn{ConversationID: w.conv2, UserID: w.eric, Number: 1,
		Status: mauricefixtures.StatusRunning, StartedAt: t0.Add(time.Hour + 10*time.Second)})

	deletedAt := t0.Add(3 * time.Hour)
	w.conv3 = seed.Conversation(mauricefixtures.Conversation{UserID: w.eric, Title: "gone", CreatedAt: t0.Add(2 * time.Hour), DeletedAt: &deletedAt})
	turn = seed.Turn(mauricefixtures.Turn{ConversationID: w.conv3, UserID: w.eric, Number: 0,
		Status: mauricefixtures.StatusSucceeded, StartedAt: t0.Add(2 * time.Hour), Duration: 5 * time.Second})
	seed.Call(mauricefixtures.Call{ConversationID: w.conv3, TurnID: turn, Provider: providerAnthropic, Model: modelSonnet,
		StartedAt: t0.Add(2 * time.Hour), InputTokens: tokens(1000), OutputTokens: tokens(1000)})

	w.conv4 = seed.Conversation(mauricefixtures.Conversation{UserID: w.bob, Title: "Jets", CreatedAt: t0.Add(-24 * time.Hour)})
	turn = seed.Turn(mauricefixtures.Turn{ConversationID: w.conv4, UserID: w.bob, Number: 0,
		Status: mauricefixtures.StatusSucceeded, StartedAt: bobTurnStart})
	seed.Call(mauricefixtures.Call{ConversationID: w.conv4, TurnID: turn, Provider: providerOllama, Model: modelQwen,
		StartedAt: bobTurnStart, InputTokens: tokens(7), OutputTokens: tokens(3),
		CacheCreationInputTokens: tokens(0), CacheReadInputTokens: tokens(0)})

	w.publicEric = seed.User(mauricefixtures.User{Instance: instanceCrapettes, Subject: "eric", Username: "eric", FirstSeenAt: t0})
	public := seed.Conversation(mauricefixtures.Conversation{UserID: w.publicEric, Title: "Habs", CreatedAt: t0})
	turn = seed.Turn(mauricefixtures.Turn{ConversationID: public, UserID: w.publicEric,
		Status: mauricefixtures.StatusSucceeded, StartedAt: t0})
	seed.Call(mauricefixtures.Call{ConversationID: public, TurnID: turn, Provider: providerAnthropic, Model: modelSonnet,
		StartedAt: t0, InputTokens: tokens(999)})
	return w
}

func f64(v float64) *float64 { return &v }

func latency(count int64, mean, p50, p95, max float64) Latency {
	return Latency{Count: count, Mean: f64(mean), P50: f64(p50), P95: f64(p95), Max: f64(max)}
}

func reported(sum, calls, unreported int64) TokenCount {
	return TokenCount{Reported: sum, ReportedCalls: calls, UnreportedCalls: unreported}
}

// assertActivity compares two activities, the latencies to latencyDelta.
func assertActivity(t *testing.T, want, got Activity) {
	t.Helper()
	assertTime(t, want.FirstTurnStartedAt, got.FirstTurnStartedAt, "first turn")
	assertTime(t, want.LastTurnStartedAt, got.LastTurnStartedAt, "last turn")
	assertLatency(t, want.TurnLatency, got.TurnLatency, "turn latency")
	assertUsage(t, want.Usage, got.Usage, "usage")
	assertUsage(t, want.TitleGeneration, got.TitleGeneration, "title generation")
	assert.InDelta(t, want.ProcessingSeconds, got.ProcessingSeconds, latencyDelta, "processing seconds")
	assert.Equal(t, want.Conversations, got.Conversations, "conversations")
	assert.Equal(t, want.Turns, got.Turns, "turns")
}

func assertUsage(t *testing.T, want, got Usage, what string) {
	t.Helper()
	assertLatency(t, want.CallLatency, got.CallLatency, what+" call latency")
	want.CallLatency, got.CallLatency = Latency{}, Latency{}
	assert.Equal(t, want, got, what)
}

func assertLatency(t *testing.T, want, got Latency, what string) {
	t.Helper()
	assert.Equal(t, want.Count, got.Count, what+" count")
	for _, stat := range []struct {
		name      string
		want, got *float64
	}{{"mean", want.Mean, got.Mean}, {"p50", want.P50, got.P50}, {"p95", want.P95, got.P95}, {"max", want.Max, got.Max}} {
		if stat.want == nil {
			assert.Nil(t, stat.got, what+" "+stat.name)
			continue
		}
		if assert.NotNil(t, stat.got, what+" "+stat.name) {
			assert.InDelta(t, *stat.want, *stat.got, latencyDelta, what+" "+stat.name)
		}
	}
}

func assertTime(t *testing.T, want, got *time.Time, what string) {
	t.Helper()
	if want == nil {
		assert.Nil(t, got, what)
		return
	}
	if assert.NotNil(t, got, what) {
		assert.True(t, want.Equal(*got), "%s: want %s, got %s", what, want, *got)
	}
}

func at(t time.Time) *time.Time { return &t }

// ericActivity is eric's activity without filters: conv1 and conv2 (conv3
// is deleted).
var ericActivity = Activity{
	Conversations:      2,
	Turns:              TurnCounts{Running: 1, Succeeded: 1, Failed: 1, Cancelled: 1},
	FirstTurnStartedAt: at(t0),
	LastTurnStartedAt:  at(t0.Add(time.Hour + 10*time.Second)),
	TurnLatency:        latency(1, 10, 10, 10, 10),
	Usage: Usage{Calls: 4, FailedCalls: 1,
		InputTokens: reported(310, 3, 1), OutputTokens: reported(55, 3, 1),
		CacheCreationInputTokens: reported(50, 1, 3), CacheReadInputTokens: reported(40, 1, 3),
		CallLatency: latency(3, 3, 3, 3.9, 4)},
	TitleGeneration: titleUsage,
	// conv1: turn 0 [0, 10] plus the union of the title [11, 13] and the
	// failed turn [12, 18] = 10 + 7; conv2: the cancelled turn, 2.
	ProcessingSeconds: 19,
}

// titleUsage is conv1's title call: input and output reported, the cache
// counts not.
var titleUsage = Usage{Calls: 1,
	InputTokens: reported(10, 1, 0), OutputTokens: reported(5, 1, 0),
	CacheCreationInputTokens: reported(0, 0, 1), CacheReadInputTokens: reported(0, 0, 1),
	CallLatency: latency(1, 2, 2, 2, 2)}

// bobActivity is bob's activity without filters.
var bobActivity = Activity{
	Conversations:      1,
	Turns:              TurnCounts{Succeeded: 1},
	FirstTurnStartedAt: at(bobTurnStart),
	LastTurnStartedAt:  at(bobTurnStart),
	TurnLatency:        latency(1, 1, 1, 1, 1),
	Usage: Usage{Calls: 1,
		InputTokens: reported(7, 1, 0), OutputTokens: reported(3, 1, 0),
		CacheCreationInputTokens: reported(0, 1, 0), CacheReadInputTokens: reported(0, 1, 0),
		CallLatency: latency(1, 1, 1, 1, 1)},
	TitleGeneration:   noUsage,
	ProcessingSeconds: 1,
}

// noUsage is the usage of no call: every count a measured zero.
var noUsage = Usage{}

func overview(t *testing.T, store *PGStore, filter ConversationFilter) Overview {
	t.Helper()
	if filter.Instance == "" {
		filter.Instance = instanceFull
	}
	got, err := store.Overview(context.Background(), filter)
	require.NoError(t, err)
	return got
}

// The totals count every row once however many calls, tool calls and
// messages a turn has, keep each token category and its unreported calls
// apart, include the title call, leave the deleted conversation and the
// other instance out, and merge the overlapping title call and failed turn
// into one processing interval.
func TestOverviewTotalsAreExact(t *testing.T) {
	store, seed := openStore(t)
	seedOverviewWorld(seed)

	got := overview(t, store, ConversationFilter{})
	assert.Equal(t, int64(3), got.Users, "eric, bob and carol")
	assert.Equal(t, int64(2), got.ActiveUsers, "carol has no turn")
	assertActivity(t, Activity{
		Conversations:      3,
		Turns:              TurnCounts{Running: 1, Succeeded: 2, Failed: 1, Cancelled: 1},
		FirstTurnStartedAt: at(bobTurnStart),
		LastTurnStartedAt:  at(t0.Add(time.Hour + 10*time.Second)),
		// Succeeded turns of 10s and 1s.
		TurnLatency: latency(2, 5.5, 5.5, 9.55, 10),
		Usage: Usage{Calls: 5, FailedCalls: 1,
			InputTokens: reported(317, 4, 1), OutputTokens: reported(58, 4, 1),
			CacheCreationInputTokens: reported(50, 2, 3), CacheReadInputTokens: reported(40, 2, 3),
			// Succeeded calls of 4, 3, 2 and 1s.
			CallLatency: latency(4, 2.5, 2.5, 3.85, 4)},
		TitleGeneration:   titleUsage,
		ProcessingSeconds: 20,
	}, got.Activity)

	require.Len(t, got.Models, 2)
	assert.Equal(t, Model{providerAnthropic, modelSonnet}, got.Models[0].Model)
	assert.Equal(t, int64(1), got.Models[0].Turns, "the title call has no turn")
	assertUsage(t, Usage{Calls: 3,
		InputTokens: reported(310, 3, 0), OutputTokens: reported(55, 3, 0),
		CacheCreationInputTokens: reported(50, 1, 2), CacheReadInputTokens: reported(40, 1, 2),
		CallLatency: latency(3, 3, 3, 3.9, 4)}, got.Models[0].Usage, "sonnet")
	assert.Equal(t, Model{providerOllama, modelQwen}, got.Models[1].Model)
	assert.Equal(t, int64(2), got.Models[1].Turns)
	assertUsage(t, Usage{Calls: 2, FailedCalls: 1,
		InputTokens: reported(7, 1, 1), OutputTokens: reported(3, 1, 1),
		CacheCreationInputTokens: reported(0, 1, 1), CacheReadInputTokens: reported(0, 1, 1),
		CallLatency: latency(1, 1, 1, 1, 1)}, got.Models[1].Usage, "qwen")
}

func TestOverviewIncludeDeletedAddsTheDeletedConversation(t *testing.T) {
	store, seed := openStore(t)
	seedOverviewWorld(seed)

	got := overview(t, store, ConversationFilter{IncludeDeleted: true})
	a := got.Activity
	assert.Equal(t, int64(4), a.Conversations)
	assert.Equal(t, TurnCounts{Running: 1, Succeeded: 3, Failed: 1, Cancelled: 1}, a.Turns)
	assertTime(t, at(t0.Add(2*time.Hour)), a.LastTurnStartedAt, "last turn")
	assert.Equal(t, reported(1317, 5, 1), a.Usage.InputTokens)
	assert.InDelta(t, 25, a.ProcessingSeconds, latencyDelta)
}

func TestOverviewOfOneUser(t *testing.T) {
	store, seed := openStore(t)
	w := seedOverviewWorld(seed)

	got := overview(t, store, ConversationFilter{UserID: w.eric})
	assert.Equal(t, int64(1), got.Users)
	assert.Equal(t, int64(1), got.ActiveUsers)
	assertActivity(t, ericActivity, got.Activity)

	got = overview(t, store, ConversationFilter{UserID: w.carol})
	assert.Equal(t, int64(1), got.Users)
	assert.Zero(t, got.ActiveUsers)
	assertActivity(t, Activity{TitleGeneration: noUsage}, got.Activity)
}

// The other instance's identical identity has its own activity only.
func TestOverviewOfAnotherInstance(t *testing.T) {
	store, seed := openStore(t)
	seedOverviewWorld(seed)

	got := overview(t, store, ConversationFilter{Instance: instanceCrapettes})
	assert.Equal(t, int64(1), got.Users)
	assert.Equal(t, int64(1), got.ActiveUsers)
	assert.Equal(t, int64(1), got.Activity.Conversations)
	assert.Equal(t, TurnCounts{Succeeded: 1}, got.Activity.Turns)
	assert.Equal(t, reported(999, 1, 0), got.Activity.Usage.InputTokens)
	require.Len(t, got.Models, 1)
	assert.Equal(t, Model{providerAnthropic, modelSonnet}, got.Models[0].Model)
}

// Nothing selected is measured zero: token totals are known zeros, the
// latencies and turn dates are absent.
func TestOverviewOfNothingIsZero(t *testing.T) {
	store, seed := openStore(t)
	seedOverviewWorld(seed)

	got := overview(t, store, ConversationFilter{Instance: "nobody-here"})
	assert.Equal(t, Overview{Models: []ModelUsage{}}, got)
	total := got.Activity.Usage.InputTokens.Total()
	require.NotNil(t, total)
	assert.Zero(t, *total)
	assert.True(t, got.Activity.Usage.InputTokens.Complete())
}

func TestOverviewRefusesAnotherInstancesUser(t *testing.T) {
	store, seed := openStore(t)
	w := seedOverviewWorld(seed)
	_, err := store.Overview(context.Background(), ConversationFilter{Instance: instanceFull, UserID: w.publicEric})
	assert.ErrorIs(t, err, ErrUserNotInInstance)
	_, err = store.Overview(context.Background(), ConversationFilter{Instance: instanceFull, From: "2026-11-02", To: "2026-11-01"})
	assert.ErrorIs(t, err, ErrInvalidArgument)
}

func userActivity(t *testing.T, store *PGStore, filter ConversationFilter, page PageRequest) UserActivityPage {
	t.Helper()
	filter.Instance = instanceFull
	got, err := store.UserActivity(context.Background(), filter, page)
	require.NoError(t, err)
	return got
}

// Every user of the scope is listed, newest first_seen_at first, each with
// only their own included activity; paging follows the users list.
func TestUserActivityListsEveryUserWithTheirActivity(t *testing.T) {
	store, seed := openStore(t)
	w := seedOverviewWorld(seed)

	first := userActivity(t, store, ConversationFilter{}, PageRequest{Limit: 2})
	require.Len(t, first.Users, 2)
	assert.Equal(t, w.carol, first.Users[0].User.ID)
	assert.False(t, first.Users[0].Active)
	assertActivity(t, Activity{}, first.Users[0].Activity)
	assert.Equal(t, w.bob, first.Users[1].User.ID)
	assert.True(t, first.Users[1].Active)
	assertActivity(t, bobActivity, first.Users[1].Activity)
	require.NotEmpty(t, first.NextCursor)

	second := userActivity(t, store, ConversationFilter{}, PageRequest{Limit: 2, Cursor: first.NextCursor})
	require.Len(t, second.Users, 1)
	assert.Equal(t, w.eric, second.Users[0].User.ID)
	assert.Equal(t, "eric", second.Users[0].User.Username)
	assert.True(t, second.Users[0].Active)
	assertActivity(t, ericActivity, second.Users[0].Activity)
	assert.Empty(t, second.NextCursor, "last page")
}

func TestUserActivityAppliesTheFilterPerUser(t *testing.T) {
	store, seed := openStore(t)
	w := seedOverviewWorld(seed)

	page := userActivity(t, store, ConversationFilter{Statuses: []TurnStatus{TurnFailed}}, PageRequest{})
	require.Len(t, page.Users, 3)
	byID := map[string]UserActivity{}
	for _, u := range page.Users {
		byID[u.User.ID] = u
	}
	assert.False(t, byID[w.bob].Active, "bob has no failed turn")
	assertActivity(t, Activity{}, byID[w.bob].Activity)
	assert.True(t, byID[w.eric].Active)
	assert.Equal(t, TurnCounts{Failed: 1}, byID[w.eric].Activity.Turns)
	assertUsage(t, failedTurnUsage, byID[w.eric].Activity.Usage, "eric")

	one := userActivity(t, store, ConversationFilter{UserID: w.bob}, PageRequest{})
	require.Len(t, one.Users, 1)
	assert.Equal(t, w.bob, one.Users[0].User.ID)
	assertActivity(t, bobActivity, one.Users[0].Activity)
}

func TestUserActivityRefusesForeignUsersCursorsAndLimits(t *testing.T) {
	store, seed := openStore(t)
	w := seedOverviewWorld(seed)
	ctx := context.Background()
	full := ConversationFilter{Instance: instanceFull}

	_, err := store.UserActivity(ctx, ConversationFilter{Instance: instanceFull, UserID: w.publicEric}, PageRequest{})
	assert.ErrorIs(t, err, ErrUserNotInInstance)

	_, err = store.UserActivity(ctx, full, PageRequest{Limit: MaxLimit + 1})
	assert.ErrorIs(t, err, ErrInvalidArgument)

	page, err := store.UserActivity(ctx, full, PageRequest{Limit: 1})
	require.NoError(t, err)
	require.NotEmpty(t, page.NextCursor)
	_, err = store.UserActivity(ctx, ConversationFilter{Instance: instanceFull, Model: modelSonnet},
		PageRequest{Limit: 1, Cursor: page.NextCursor})
	assert.ErrorIs(t, err, ErrInvalidCursor, "another filter's cursor")

	users, err := store.Users(ctx, instanceFull, PageRequest{Limit: 1})
	require.NoError(t, err)
	_, err = store.UserActivity(ctx, full, PageRequest{Limit: 1, Cursor: users.NextCursor})
	assert.ErrorIs(t, err, ErrInvalidCursor, "the users list's cursor")
}
