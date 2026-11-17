package mauricereport

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/fixtures/mauricefixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	modelHaiku    = "claude-haiku-5-5"
	toolGamesName = "get_games"
	// malformedArgs is raw tool arguments that are not JSON.
	malformedArgs = `{"id": 84`
)

// detailWorld is one conversation of the full instance's user with three
// turns (succeeded, failed, cancelled) and a title call, a deleted
// conversation of the same user, and a conversation of the same identity on
// the public instance, whose IDs the nested lookups must refuse.
type detailWorld struct {
	conv, deleted, foreign                 string
	turnOK, turnFailed, turnCancelled      string
	msgUser, msgCall, msgTool, msgAnswer   string
	msgFailed                              string
	callFirst, callSecond, callFailed      int64
	titleCall                              int64
	toolOK, toolMalformed, toolCancelled   int64
	toolTranscript                         int64
	foreignTurn, foreignMsg                string
	foreignCall, foreignTitle, foreignTool int64
	systemPrompt, toolDefinitions          string
	toolResult, answer                     string
}

func seedDetailWorld(seed *mauricefixtures.Seeder) detailWorld {
	w := detailWorld{
		// Longer than InlineTextRunes, with multi-byte code points.
		systemPrompt:    "Tu es Maurice. " + strings.Repeat("é🏒", InlineTextRunes),
		toolDefinitions: `[{"name": "get_player"}]`,
		toolResult:      strings.Repeat("r", InlineTextRunes+1),
		answer:          "He has 30 goals. " + strings.Repeat("🏒", InlineTextRunes),
	}
	owner := seed.User(mauricefixtures.User{Instance: instanceFull, Subject: "eric-uid", Username: "eric", FirstSeenAt: t0})
	public := seed.User(mauricefixtures.User{Instance: instanceCrapettes, Subject: "eric-uid", Username: "eric", FirstSeenAt: t0})
	w.conv = seed.Conversation(mauricefixtures.Conversation{UserID: owner, Title: "hello", CreatedAt: t0})
	gone := t0.Add(time.Hour)
	w.deleted = seed.Conversation(mauricefixtures.Conversation{UserID: owner, CreatedAt: t0, DeletedAt: &gone})
	w.foreign = seed.Conversation(mauricefixtures.Conversation{UserID: public, CreatedAt: t0})

	// Inserted in reverse turn order: the reads order by turn number.
	w.turnCancelled = seed.Turn(mauricefixtures.Turn{ConversationID: w.conv, UserID: owner, Number: 2,
		Status: mauricefixtures.StatusCancelled, StartedAt: t0.Add(2 * time.Minute)})
	seed.Message(mauricefixtures.Message{ConversationID: w.conv, TurnID: w.turnCancelled, Number: 0, Role: "user", Content: "stop"})
	w.turnFailed = seed.Turn(mauricefixtures.Turn{ConversationID: w.conv, UserID: owner, Number: 1,
		Status: mauricefixtures.StatusFailed, StartedAt: t0.Add(time.Minute)})
	w.turnOK = seed.Turn(mauricefixtures.Turn{ConversationID: w.conv, UserID: owner, Number: 0,
		Status: mauricefixtures.StatusSucceeded, StartedAt: t0, Duration: 3 * time.Second})
	seedSucceededTurn(seed, &w)
	seedFailedTurn(seed, &w)
	w.titleCall = seed.Call(mauricefixtures.Call{ConversationID: w.conv, Provider: providerAnthropic, Model: modelHaiku,
		StartedAt: t0.Add(5 * time.Second), Instruction: mauricefixtures.Text("Title this.")})
	seed.CallInput(w.titleCall, 0, w.msgUser)

	seedForeign(seed, &w, public)
	deletedTurn := seed.Turn(mauricefixtures.Turn{ConversationID: w.deleted, UserID: owner,
		Status: mauricefixtures.StatusSucceeded, StartedAt: t0})
	seed.Message(mauricefixtures.Message{ConversationID: w.deleted, TurnID: deletedTurn, Role: "user", Content: "gone"})
	return w
}

// seedSucceededTurn is turn 0: a question, a tool round and the answer.
func seedSucceededTurn(seed *mauricefixtures.Seeder, w *detailWorld) {
	msg := func(n int, role, content string, result int64) string {
		return seed.Message(mauricefixtures.Message{ConversationID: w.conv, TurnID: w.turnOK, Number: n,
			Role: role, Content: content, ResultToolCallID: result})
	}
	w.msgAnswer = msg(3, "assistant", w.answer, 0)
	w.msgUser = msg(0, "user", "How many goals?", 0)
	w.msgCall = msg(1, "assistant", "", 0)
	w.callSecond = seed.Call(mauricefixtures.Call{ConversationID: w.conv, TurnID: w.turnOK, Round: 1,
		Provider: providerAnthropic, Model: modelSonnet, StartedAt: t0.Add(2 * time.Second),
		InputTokens: mauricefixtures.Tokens(120), OutputTokens: mauricefixtures.Tokens(8),
		CacheCreationInputTokens: mauricefixtures.Tokens(0), CacheReadInputTokens: mauricefixtures.Tokens(50)})
	w.callFirst = seed.Call(mauricefixtures.Call{ConversationID: w.conv, TurnID: w.turnOK, Round: 0,
		Provider: providerAnthropic, Model: modelSonnet, StartedAt: t0,
		InputTokens: mauricefixtures.Tokens(100), SystemPrompt: &w.systemPrompt, ToolDefinitions: &w.toolDefinitions})
	w.toolOK = seed.ToolCall(mauricefixtures.ToolCall{TurnID: w.turnOK, CallID: w.callFirst, MessageID: w.msgCall,
		Name: toolPlayer, ArgumentsRaw: `{"id":8478402}`, Result: w.toolResult, StartedAt: t0.Add(time.Second)})
	w.msgTool = msg(2, "tool", w.toolResult, w.toolOK)
	seed.CallInput(w.callFirst, 0, w.msgUser)
	for i, m := range []string{w.msgUser, w.msgCall, w.msgTool} {
		seed.CallInput(w.callSecond, i, m)
	}
}

// seedFailedTurn is turn 1: a failed call that requested a malformed and a
// cancelled tool call, plus a transcript-only tool call.
func seedFailedTurn(seed *mauricefixtures.Seeder, w *detailWorld) {
	w.msgFailed = seed.Message(mauricefixtures.Message{ConversationID: w.conv, TurnID: w.turnFailed, Number: 0,
		Role: "user", Content: "And assists?"})
	w.callFailed = seed.Call(mauricefixtures.Call{ConversationID: w.conv, TurnID: w.turnFailed,
		Provider: providerAnthropic, Model: modelSonnet, StartedAt: t0.Add(time.Minute), Failed: true})
	w.toolMalformed = seed.ToolCall(mauricefixtures.ToolCall{TurnID: w.turnFailed, CallID: w.callFailed,
		Name: toolPlayer, ArgumentsRaw: malformedArgs, Result: "parse error", Status: "parse_error",
		StartedAt: t0.Add(time.Minute)})
	w.toolCancelled = seed.ToolCall(mauricefixtures.ToolCall{TurnID: w.turnFailed, CallID: w.callFailed, Sequence: 1,
		Name: toolGamesName, ArgumentsRaw: "{}", Status: mauricefixtures.StatusCancelled})
	w.toolTranscript = seed.ToolCall(mauricefixtures.ToolCall{TurnID: w.turnFailed, Sequence: 2,
		Name: toolGamesName, ArgumentsRaw: "{}"})
}

func seedForeign(seed *mauricefixtures.Seeder, w *detailWorld, owner string) {
	w.foreignTurn = seed.Turn(mauricefixtures.Turn{ConversationID: w.foreign, UserID: owner,
		Status: mauricefixtures.StatusSucceeded, StartedAt: t0})
	w.foreignMsg = seed.Message(mauricefixtures.Message{ConversationID: w.foreign, TurnID: w.foreignTurn,
		Role: "user", Content: "secret"})
	w.foreignCall = seed.Call(mauricefixtures.Call{ConversationID: w.foreign, TurnID: w.foreignTurn,
		Provider: providerAnthropic, Model: modelSonnet, StartedAt: t0, SystemPrompt: mauricefixtures.Text("secret"),
		ToolDefinitions: mauricefixtures.Text(`[{"name": "secret"}]`)})
	w.foreignTitle = seed.Call(mauricefixtures.Call{ConversationID: w.foreign, Provider: providerAnthropic,
		Model: modelHaiku, StartedAt: t0, Instruction: mauricefixtures.Text("secret")})
	w.foreignTool = seed.ToolCall(mauricefixtures.ToolCall{TurnID: w.foreignTurn, CallID: w.foreignCall,
		Name: toolPlayer, ArgumentsRaw: `{"secret":1}`, Result: "secret", StartedAt: t0})
}

func fullRef(conv string) ConversationRef {
	return ConversationRef{Instance: instanceFull, ConversationID: conv}
}

func TestTranscriptOrdersMessagesAndMarksFailedEvidence(t *testing.T) {
	store, seed := openStore(t)
	w := seedDetailWorld(seed)
	ctx := context.Background()

	var got []Message
	cursor := ""
	for pages := 0; ; pages++ {
		require.Less(t, pages, 4, "paging must end")
		page, found, err := store.Transcript(ctx, fullRef(w.conv), PageRequest{Limit: 2, Cursor: cursor})
		require.NoError(t, err)
		require.True(t, found)
		got = append(got, page.Messages...)
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	ids := make([]string, len(got))
	for i, m := range got {
		ids[i] = m.ID
	}
	require.Len(t, got, 6)
	assert.Equal(t, []string{w.msgUser, w.msgCall, w.msgTool, w.msgAnswer, w.msgFailed}, ids[:5])

	user, call, tool, answer, failed, cancelled := got[0], got[1], got[2], got[3], got[4], got[5]
	assert.Equal(t, &Text{Text: "How many goals?", Length: 15}, user.Content)
	assert.True(t, user.Replayable())
	assert.Equal(t, int64(1), call.ToolCalls, "the assistant message's tool request")
	assert.Nil(t, tool.Content, "a tool result is read only through Payload")
	assert.Equal(t, int64(InlineTextRunes+1), tool.ContentLength)
	assert.Equal(t, &w.toolOK, tool.ResultToolCallID)
	answerRunes := int64(len([]rune(w.answer)))
	require.NotNil(t, answer.Content)
	assert.Equal(t, string([]rune(w.answer)[:InlineTextRunes]), answer.Content.Text, "cut at code points, not bytes")
	assert.Equal(t, answerRunes, answer.Content.Length)
	assert.Equal(t, answerRunes, answer.ContentLength)
	assert.True(t, answer.Content.Truncated())
	assert.Equal(t, int64(InlineTextRunes), answer.Content.End())
	assert.Equal(t, TurnFailed, failed.TurnStatus)
	assert.False(t, failed.Replayable(), "a failed turn's prompt is evidence, not chat history")
	assert.Equal(t, "And assists?", failed.Content.Text)
	assert.Equal(t, TurnCancelled, cancelled.TurnStatus)
	assert.Equal(t, int32(2), cancelled.TurnNumber)
	assert.False(t, cancelled.Replayable())
}

func TestTurnsReportStatusCountsAndUnknownUsage(t *testing.T) {
	store, seed := openStore(t)
	w := seedDetailWorld(seed)

	page, found, err := store.Turns(context.Background(), fullRef(w.conv), PageRequest{})
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, page.Turns, 3)
	assert.Empty(t, page.NextCursor)
	ok, failed, cancelled := page.Turns[0], page.Turns[1], page.Turns[2]

	assert.Equal(t, w.turnOK, ok.ID)
	assert.Equal(t, TurnSucceeded, ok.Status)
	assert.True(t, ok.Replayable())
	assert.Nil(t, ok.ErrorClass)
	assert.Equal(t, t0.Add(3*time.Second), ok.CompletedAt.UTC())
	assert.Equal(t, []string{}, ok.Warnings)
	assert.Equal(t, int64(4), ok.Messages)
	assert.Equal(t, int64(1), ok.ToolCalls)
	assert.Equal(t, int64(2), ok.Usage.Calls)
	assert.Equal(t, TokenCount{Reported: 220, ReportedCalls: 2}, ok.Usage.InputTokens)
	assert.Equal(t, TokenCount{Reported: 8, ReportedCalls: 1, UnreportedCalls: 1}, ok.Usage.OutputTokens,
		"one call did not report: a lower bound")
	assert.Equal(t, []Model{{providerAnthropic, modelSonnet}}, ok.Models, "the title call has no turn")

	assert.Equal(t, TurnFailed, failed.Status)
	assert.Equal(t, "provider_error", *failed.ErrorClass)
	assert.Equal(t, int64(1), failed.Usage.FailedCalls)
	assert.Nil(t, failed.Usage.InputTokens.Total(), "calls ran and none reported: unknown, not zero")
	assert.Equal(t, int64(3), failed.ToolCalls)

	assert.Equal(t, TurnCancelled, cancelled.Status)
	assert.Equal(t, "cancelled", *cancelled.ErrorClass)
	assert.Zero(t, cancelled.Usage.Calls)
	assert.Equal(t, []Model{}, cancelled.Models)
}

func TestTurnCallsAreOrderedByRoundAndTitleCallsKeptApart(t *testing.T) {
	store, seed := openStore(t)
	w := seedDetailWorld(seed)
	ctx := context.Background()

	first, found, err := store.TurnCalls(ctx, fullRef(w.conv), w.turnOK, PageRequest{Limit: 1})
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, first.Calls, 1)
	assert.Equal(t, w.callFirst, first.Calls[0].ID, "round 0 first, though inserted last")
	second, _, err := store.TurnCalls(ctx, fullRef(w.conv), w.turnOK, PageRequest{Limit: 1, Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Calls, 1)
	assert.Equal(t, w.callSecond, second.Calls[0].ID)
	assert.Empty(t, second.NextCursor)

	c := first.Calls[0]
	assert.Equal(t, "chat_round", c.Kind)
	assert.Equal(t, int32(0), *c.Round)
	assert.Equal(t, w.turnOK, *c.TurnID)
	assert.Equal(t, int64(1), c.InputMessages)
	assert.Equal(t, int64(len([]rune(w.systemPrompt))), *c.SystemPromptLength)
	assert.Nil(t, c.InstructionLength)
	assert.Equal(t, int64(len([]rune(w.toolDefinitions))), *c.ToolDefinitionsLength)
	assert.Equal(t, int64(100), *c.InputTokens)
	assert.Nil(t, c.OutputTokens, "not reported")
	assert.Equal(t, int64(1), c.ToolCalls)

	titles, found, err := store.TitleCalls(ctx, fullRef(w.conv), PageRequest{})
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, titles.Calls, 1)
	title := titles.Calls[0]
	assert.Equal(t, w.titleCall, title.ID)
	assert.Nil(t, title.TurnID)
	assert.Nil(t, title.Round)
	assert.Equal(t, "title_generation", title.Kind)
	assert.Equal(t, int64(len("Title this.")), *title.InstructionLength)

	_, _, err = store.TurnCalls(ctx, fullRef(w.conv), w.turnFailed, PageRequest{Cursor: first.NextCursor})
	assert.ErrorIs(t, err, ErrInvalidCursor, "another turn's cursor")
}

func TestToolCallsKeepMalformedCancelledAndTranscriptOnlyCalls(t *testing.T) {
	store, seed := openStore(t)
	w := seedDetailWorld(seed)

	page, found, err := store.ToolCalls(context.Background(), fullRef(w.conv), w.turnFailed, PageRequest{})
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, page.ToolCalls, 3)
	malformed, cancelled, transcript := page.ToolCalls[0], page.ToolCalls[1], page.ToolCalls[2]

	assert.Equal(t, w.toolMalformed, malformed.ID)
	assert.False(t, malformed.ArgumentsValid)
	assert.Equal(t, "parse_error", *malformed.Status)
	assert.Equal(t, int64(len(malformedArgs)), malformed.ArgumentsRawLength)
	assert.Equal(t, w.callFailed, *malformed.CallID)

	assert.Equal(t, mauricefixtures.StatusCancelled, *cancelled.Status)
	assert.Nil(t, cancelled.StartedAt, "never ran")
	assert.Nil(t, cancelled.ResultLength)
	assert.True(t, cancelled.ArgumentsValid)

	assert.Equal(t, w.toolTranscript, transcript.ID)
	assert.Nil(t, transcript.CallID, "no call telemetry")
	assert.Nil(t, transcript.Status)
	assert.Equal(t, toolGamesName, transcript.Name)
	assert.Equal(t, "function", transcript.Type)
}

// Every read refuses a conversation of another instance, and a deleted one
// unless IncludeDeleted; the deleted one is readable with it.
func TestDetailReadsScopeInstanceAndDeletion(t *testing.T) {
	store, seed := openStore(t)
	w := seedDetailWorld(seed)
	ctx := context.Background()
	reads := detailReads(w)

	for name, read := range reads {
		found, err := read(ctx, store, ConversationRef{Instance: instanceCrapettes, ConversationID: w.conv})
		require.NoError(t, err, name)
		assert.False(t, found, "%s: the same identity's conversation on another instance", name)
		found, err = read(ctx, store, ConversationRef{Instance: instanceFull, ConversationID: w.conv})
		require.NoError(t, err, name)
		assert.True(t, found, "%s: its own instance", name)
	}

	deleted := ConversationRef{Instance: instanceFull, ConversationID: w.deleted}
	page, found, err := store.Transcript(ctx, deleted, PageRequest{})
	require.NoError(t, err)
	assert.False(t, found, "deleted, excluded by default")
	assert.Empty(t, page.Messages)
	deleted.IncludeDeleted = true
	page, found, err = store.Transcript(ctx, deleted, PageRequest{})
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, page.Messages, 1)
	assert.Equal(t, "gone", page.Messages[0].Content.Text)
	turns, found, err := store.Turns(ctx, deleted, PageRequest{})
	require.NoError(t, err)
	assert.True(t, found)
	assert.Len(t, turns.Turns, 1)
}

// detailRead is one detail read of w's own nested IDs under ref.
type detailRead func(ctx context.Context, s *PGStore, ref ConversationRef) (bool, error)

func detailReads(w detailWorld) map[string]detailRead {
	return map[string]detailRead{
		"transcript": func(ctx context.Context, s *PGStore, ref ConversationRef) (bool, error) {
			_, found, err := s.Transcript(ctx, ref, PageRequest{})
			return found, err
		},
		"turns": func(ctx context.Context, s *PGStore, ref ConversationRef) (bool, error) {
			_, found, err := s.Turns(ctx, ref, PageRequest{})
			return found, err
		},
		"turn calls": func(ctx context.Context, s *PGStore, ref ConversationRef) (bool, error) {
			_, found, err := s.TurnCalls(ctx, ref, w.turnOK, PageRequest{})
			return found, err
		},
		"title calls": func(ctx context.Context, s *PGStore, ref ConversationRef) (bool, error) {
			_, found, err := s.TitleCalls(ctx, ref, PageRequest{})
			return found, err
		},
		"tool calls": func(ctx context.Context, s *PGStore, ref ConversationRef) (bool, error) {
			_, found, err := s.ToolCalls(ctx, ref, w.turnOK, PageRequest{})
			return found, err
		},
		"call input": func(ctx context.Context, s *PGStore, ref ConversationRef) (bool, error) {
			_, found, err := s.CallInput(ctx, ref, idText(w.callFirst), PageRequest{})
			return found, err
		},
		"tool call": func(ctx context.Context, s *PGStore, ref ConversationRef) (bool, error) {
			_, found, err := s.ToolCall(ctx, ref, idText(w.toolOK))
			return found, err
		},
		"payload": func(ctx context.Context, s *PGStore, ref ConversationRef) (bool, error) {
			_, found, err := s.Payload(ctx, ref, PayloadRequest{Kind: PayloadMessageContent, ID: w.msgUser})
			return found, err
		},
	}
}
