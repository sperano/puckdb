package mauricereport

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/fixtures/mauricefixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	providerAnthropic = "anthropic"
	providerOllama    = "ollama"
	modelSonnet       = "claude-sonnet-5-5"
	modelQwen         = "qwen3"
	toolPlayer        = "get_player"
)

// convSeed seeds the conversations of one user of instanceFull.
type convSeed struct {
	seed *mauricefixtures.Seeder
	user string
}

func newConvSeed(t *testing.T) (*PGStore, convSeed) {
	t.Helper()
	store, seed := openStore(t)
	return store, convSeed{seed: seed, user: seed.User(mauricefixtures.User{Instance: instanceFull, Username: "eric", FirstSeenAt: t0})}
}

func (s convSeed) conversation(title string, created time.Time) string {
	return s.seed.Conversation(mauricefixtures.Conversation{UserID: s.user, Title: title, CreatedAt: created})
}

func (s convSeed) turn(conv string, number int, status string, started time.Time) string {
	return s.seed.Turn(mauricefixtures.Turn{ConversationID: conv, UserID: s.user, Number: number, Status: status, StartedAt: started})
}

// callTurn is a turn with one LLM call of provider/model and, when tool is
// set, one tool call of that name.
func (s convSeed) callTurn(conv string, number int, status, provider, model, tool string, started time.Time) string {
	turn := s.turn(conv, number, status, started)
	call := s.seed.Call(mauricefixtures.Call{ConversationID: conv, TurnID: turn, Provider: provider, Model: model, StartedAt: started})
	if tool != "" {
		s.seed.ToolCall(mauricefixtures.ToolCall{TurnID: turn, CallID: call, Name: tool, ArgumentsRaw: "{}", Result: "ok", StartedAt: started})
	}
	return turn
}

func (s convSeed) message(conv, turn string, number int, role, content string) {
	s.seed.Message(mauricefixtures.Message{ConversationID: conv, TurnID: turn, Number: number, Role: role, Content: content})
}

// matching returns the IDs filter selects on instanceFull, sorted.
func matching(t *testing.T, store *PGStore, filter ConversationFilter) []string {
	t.Helper()
	filter.Instance = instanceFull
	page, err := store.Conversations(context.Background(), filter, PageRequest{Limit: MaxLimit})
	require.NoError(t, err)
	ids := conversationIDs(page)
	sort.Strings(ids)
	return ids
}

func sorted(ids ...string) []string {
	sort.Strings(ids)
	return ids
}

func TestConversationMetadataComesFromStoredTurns(t *testing.T) {
	store, s := newConvSeed(t)
	updated := t0.Add(time.Hour)
	conv := s.seed.Conversation(mauricefixtures.Conversation{UserID: s.user, Title: "Habs", CreatedAt: t0, UpdatedAt: updated})
	s.callTurn(conv, 0, mauricefixtures.StatusSucceeded, providerOllama, modelQwen, "", t0)
	s.callTurn(conv, 1, mauricefixtures.StatusFailed, providerAnthropic, modelSonnet, toolPlayer, t0.Add(time.Minute))
	s.callTurn(conv, 2, mauricefixtures.StatusCancelled, providerAnthropic, modelSonnet, "", t0.Add(2*time.Minute))
	lastStart := t0.Add(3 * time.Minute)
	s.turn(conv, 3, mauricefixtures.StatusRunning, lastStart)
	s.seed.Call(mauricefixtures.Call{ConversationID: conv, Provider: providerAnthropic, Model: "titler", StartedAt: t0})
	empty := s.conversation("", t0.Add(-time.Hour))

	page, err := store.Conversations(context.Background(), ConversationFilter{Instance: instanceFull}, PageRequest{})
	require.NoError(t, err)
	require.Equal(t, []string{conv, empty}, conversationIDs(page))

	c := page.Conversations[0]
	running := TurnRunning
	title := "Habs"
	require.NotNil(t, c.LastTurnStartedAt)
	assert.True(t, c.LastTurnStartedAt.Equal(lastStart), "got %s", c.LastTurnStartedAt)
	assert.True(t, c.CreatedAt.Equal(t0), "created_at: got %s", c.CreatedAt)
	assert.True(t, c.UpdatedAt.Equal(updated), "updated_at: got %s", c.UpdatedAt)
	// The instants are checked above; normalize their location for Equal.
	c.CreatedAt, c.UpdatedAt, c.LastTurnStartedAt = t0, updated, &lastStart
	assert.Equal(t, Conversation{
		ID: conv, Instance: instanceFull, UserID: s.user, Username: "eric", Title: &title,
		CreatedAt: t0, UpdatedAt: updated, Status: &running, LastTurnStartedAt: &lastStart,
		Turns:  TurnCounts{Running: 1, Succeeded: 1, Failed: 1, Cancelled: 1},
		Models: []Model{{providerAnthropic, modelSonnet}, {providerOllama, modelQwen}},
	}, c, "status of the latest turn; title generation's model not listed")
	assert.EqualValues(t, 4, c.Turns.Total())

	e := page.Conversations[1]
	assert.Nil(t, e.Title)
	assert.Nil(t, e.Status, "no turns, no status")
	assert.Nil(t, e.LastTurnStartedAt)
	assert.Equal(t, TurnCounts{}, e.Turns)
	assert.Equal(t, []Model{}, e.Models)
}

// The interval is inclusive UTC days on the turn's start: the first
// microsecond of from and the last of to are in, their neighbours out.
func TestConversationsDateIntervalIsInclusiveUTCDays(t *testing.T) {
	store, s := newConvSeed(t)
	from := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	afterTo := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	starts := map[string]time.Time{
		"before": from.Add(-time.Microsecond),
		"first":  from,
		"last":   afterTo.Add(-time.Microsecond),
		"after":  afterTo,
	}
	ids := map[string]string{}
	for name, start := range starts {
		ids[name] = s.conversation(name, start)
		s.turn(ids[name], 0, mauricefixtures.StatusSucceeded, start)
	}

	assert.Equal(t, sorted(ids["first"], ids["last"]), matching(t, store, ConversationFilter{From: "2026-11-05", To: "2026-11-06"}))
	assert.Equal(t, sorted(ids["first"], ids["last"], ids["after"]), matching(t, store, ConversationFilter{From: "2026-11-05"}))
	assert.Equal(t, sorted(ids["before"], ids["first"]), matching(t, store, ConversationFilter{To: "2026-11-05"}))
	assert.Equal(t, []string{ids["first"]}, matching(t, store, ConversationFilter{From: "2026-11-05", To: "2026-11-05"}))
}

// Every turn filter must hold for the same turn: a failed turn and a
// turn using the model, in one conversation, do not make a failed turn
// using the model.
func TestConversationsTurnFiltersHoldForOneTurn(t *testing.T) {
	store, s := newConvSeed(t)
	mixed := s.conversation("mixed", t0)
	s.callTurn(mixed, 0, mauricefixtures.StatusFailed, providerOllama, modelQwen, "", t0)
	s.callTurn(mixed, 1, mauricefixtures.StatusSucceeded, providerAnthropic, modelSonnet, toolPlayer, t0.Add(24*time.Hour))
	failedSonnet := s.conversation("failed sonnet", t0)
	s.callTurn(failedSonnet, 0, mauricefixtures.StatusFailed, providerAnthropic, modelSonnet, "", t0)
	cancelled := s.conversation("cancelled", t0)
	s.callTurn(cancelled, 0, mauricefixtures.StatusCancelled, providerAnthropic, modelSonnet, "", t0)
	titleOnly := s.conversation("titled by titler", t0)
	s.callTurn(titleOnly, 0, mauricefixtures.StatusRunning, providerAnthropic, modelSonnet, "", t0)
	s.seed.Call(mauricefixtures.Call{ConversationID: titleOnly, Provider: providerOllama, Model: "titler", StartedAt: t0})
	noTurns := s.conversation("no turns", t0)

	failed := []TurnStatus{TurnFailed}
	tests := []struct {
		name   string
		filter ConversationFilter
		want   []string
	}{
		{"no filter", ConversationFilter{}, sorted(mixed, failedSonnet, cancelled, titleOnly, noTurns)},
		{"failed with sonnet", ConversationFilter{Statuses: failed, Model: modelSonnet}, []string{failedSonnet}},
		{"failed with qwen", ConversationFilter{Statuses: failed, Model: modelQwen}, []string{mixed}},
		{"failed or cancelled", ConversationFilter{Statuses: []TurnStatus{TurnFailed, TurnCancelled}}, sorted(mixed, failedSonnet, cancelled)},
		{"running", ConversationFilter{Statuses: []TurnStatus{TurnRunning}}, []string{titleOnly}},
		{"tool", ConversationFilter{ToolName: toolPlayer}, []string{mixed}},
		{"tool on a failed turn", ConversationFilter{ToolName: toolPlayer, Statuses: failed}, nil},
		{"tool on the first day", ConversationFilter{ToolName: toolPlayer, To: t0.Format(time.DateOnly)}, nil},
		{"provider", ConversationFilter{Provider: providerOllama}, []string{mixed}},
		{"provider and model on one call", ConversationFilter{Provider: providerOllama, Model: modelSonnet}, nil},
		{"title generation model", ConversationFilter{Model: "titler"}, nil},
		{"model is case-sensitive", ConversationFilter{Model: "QWEN3"}, nil},
	}
	for _, tt := range tests {
		got := matching(t, store, tt.filter)
		if tt.want == nil {
			assert.Empty(t, got, tt.name)
			continue
		}
		assert.Equal(t, tt.want, got, tt.name)
	}
}

// Search is a case-insensitive literal substring of titles and of user and
// assistant messages only.
func TestConversationsSearchMatchesTitlesAndRetainedTextLiterally(t *testing.T) {
	store, s := newConvSeed(t)
	withMessage := func(title, role, content string) string {
		conv := s.conversation(title, t0)
		turn := s.turn(conv, 0, mauricefixtures.StatusFailed, t0)
		s.message(conv, turn, 0, role, content)
		return conv
	}
	percent := s.conversation("Habs at 100% tonight", t0)
	wildcardBait := s.conversation("1000 Habs fans", t0)
	underscore := withMessage("", "user", "the a_b case")
	underscoreBait := withMessage("", "user", "the axb case")
	backslash := withMessage("", "assistant", `saved to C:\temp\habs`)
	failedAnswer := withMessage("", "assistant", "McDavid leads the league")
	toolResult := withMessage("", "tool", "zebra in the tool result")
	system := withMessage("", "system", "walrus system prompt")
	args := s.conversation("", t0)
	argsTurn := s.turn(args, 0, mauricefixtures.StatusSucceeded, t0)
	call := s.seed.Call(mauricefixtures.Call{ConversationID: args, TurnID: argsTurn, Provider: providerAnthropic, Model: modelSonnet, StartedAt: t0})
	s.seed.ToolCall(mauricefixtures.ToolCall{TurnID: argsTurn, CallID: call, Name: toolPlayer,
		ArgumentsRaw: `{"name":"giraffe"}`, Result: "lion", StartedAt: t0})

	tests := []struct {
		search string
		want   []string
	}{
		{"100%", []string{percent}},
		{"habs", sorted(percent, wildcardBait, backslash)},
		{"HABS FANS", []string{wildcardBait}},
		{"a_b", []string{underscore}},
		{`C:\temp`, []string{backslash}},
		{"  mcdavid  ", []string{failedAnswer}},
		{"zebra", nil},
		{"walrus", nil},
		{"giraffe", nil},
		{"lion", nil},
		{"   ", sorted(percent, wildcardBait, underscore, underscoreBait, backslash, failedAnswer, toolResult, system, args)},
	}
	for _, tt := range tests {
		got := matching(t, store, ConversationFilter{Search: tt.search})
		if tt.want == nil {
			assert.Empty(t, got, "search %q", tt.search)
			continue
		}
		assert.Equal(t, tt.want, got, "search %q", tt.search)
	}
}

func TestConversationsCombineSearchOwnerAndTurnFilters(t *testing.T) {
	store, s := newConvSeed(t)
	other := s.seed.User(mauricefixtures.User{Instance: instanceFull, Username: "bob", FirstSeenAt: t0})
	mine := s.conversation("Habs trade", t0)
	s.callTurn(mine, 0, mauricefixtures.StatusSucceeded, providerAnthropic, modelSonnet, toolPlayer, t0)
	mineFailed := s.conversation("Habs lineup", t0)
	s.callTurn(mineFailed, 0, mauricefixtures.StatusFailed, providerAnthropic, modelSonnet, toolPlayer, t0)
	theirs := s.seed.Conversation(mauricefixtures.Conversation{UserID: other, Title: "Habs trade", CreatedAt: t0})
	s.seed.Turn(mauricefixtures.Turn{ConversationID: theirs, UserID: other, Status: mauricefixtures.StatusSucceeded, StartedAt: t0})

	assert.Equal(t, sorted(mine, mineFailed, theirs), matching(t, store, ConversationFilter{Search: "habs"}))
	assert.Equal(t, sorted(mine, mineFailed), matching(t, store, ConversationFilter{Search: "habs", UserID: s.user}))
	assert.Equal(t, []string{mine}, matching(t, store, ConversationFilter{
		Search: "habs", UserID: s.user, ToolName: toolPlayer, Statuses: []TurnStatus{TurnSucceeded},
		Provider: providerAnthropic, Model: modelSonnet, From: t0.Format(time.DateOnly), To: t0.Format(time.DateOnly),
	}))
}
