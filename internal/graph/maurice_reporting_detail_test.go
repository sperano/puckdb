package graph

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/fixtures/mauricefixtures"
	"github.com/sperano/puckdb/internal/mauricereport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (r recordingReader) Transcript(context.Context, mauricereport.ConversationRef, mauricereport.PageRequest) (mauricereport.MessagePage, bool, error) {
	r.t.Error("Transcript read")
	return mauricereport.MessagePage{}, false, nil
}

func (r recordingReader) Turns(context.Context, mauricereport.ConversationRef, mauricereport.PageRequest) (mauricereport.TurnPage, bool, error) {
	r.t.Error("Turns read")
	return mauricereport.TurnPage{}, false, nil
}

func (r recordingReader) TurnCalls(context.Context, mauricereport.ConversationRef, string, mauricereport.PageRequest) (mauricereport.CallPage, bool, error) {
	r.t.Error("TurnCalls read")
	return mauricereport.CallPage{}, false, nil
}

func (r recordingReader) TitleCalls(context.Context, mauricereport.ConversationRef, mauricereport.PageRequest) (mauricereport.CallPage, bool, error) {
	r.t.Error("TitleCalls read")
	return mauricereport.CallPage{}, false, nil
}

func (r recordingReader) ToolCalls(context.Context, mauricereport.ConversationRef, string, mauricereport.PageRequest) (mauricereport.ToolCallPage, bool, error) {
	r.t.Error("ToolCalls read")
	return mauricereport.ToolCallPage{}, false, nil
}

func (r recordingReader) CallInput(context.Context, mauricereport.ConversationRef, string, mauricereport.PageRequest) (mauricereport.CallInput, bool, error) {
	r.t.Error("CallInput read")
	return mauricereport.CallInput{}, false, nil
}

func (r recordingReader) ToolCall(context.Context, mauricereport.ConversationRef, string) (mauricereport.ToolCallDetail, bool, error) {
	r.t.Error("ToolCall read")
	return mauricereport.ToolCallDetail{}, false, nil
}

func (r recordingReader) Payload(context.Context, mauricereport.ConversationRef, mauricereport.PayloadRequest) (mauricereport.Text, bool, error) {
	r.t.Error("Payload read")
	return mauricereport.Text{}, false, nil
}

// detailGraphWorld is one full-instance conversation with a succeeded turn
// (question, tool round, answer), a failed turn with malformed tool
// arguments and a title call, plus the same identity's public conversation.
type detailGraphWorld struct {
	conv, foreign, deleted     string
	turnOK, turnFailed         string
	call, final, title         int64
	foreignCall                int64
	toolOK, toolBad, foreignTC int64
	prompt                     string
}

func seedDetailGraphWorld(seed *mauricefixtures.Seeder) detailGraphWorld {
	w := detailGraphWorld{prompt: strings.Repeat("p", mauricereport.InlineTextRunes+10)}
	owner := seed.User(mauricefixtures.User{Instance: "maurice", Subject: "eric-uid", Username: "eric", FirstSeenAt: reportingT0})
	public := seed.User(mauricefixtures.User{Instance: "maurice-crapettes", Subject: "eric-uid", Username: "eric", FirstSeenAt: reportingT0})
	w.conv = seed.Conversation(mauricefixtures.Conversation{UserID: owner, CreatedAt: reportingT0})
	w.foreign = seed.Conversation(mauricefixtures.Conversation{UserID: public, CreatedAt: reportingT0})
	gone := reportingT0.Add(time.Hour)
	w.deleted = seed.Conversation(mauricefixtures.Conversation{UserID: owner, CreatedAt: reportingT0, DeletedAt: &gone})
	seed.Turn(mauricefixtures.Turn{ConversationID: w.deleted, UserID: owner, Status: mauricefixtures.StatusSucceeded, StartedAt: reportingT0})

	w.turnOK = seed.Turn(mauricefixtures.Turn{ConversationID: w.conv, UserID: owner, Status: mauricefixtures.StatusSucceeded, StartedAt: reportingT0})
	question := seed.Message(mauricefixtures.Message{ConversationID: w.conv, TurnID: w.turnOK, Role: "user", Content: "Goals?"})
	request := seed.Message(mauricefixtures.Message{ConversationID: w.conv, TurnID: w.turnOK, Number: 1, Role: "assistant"})
	w.call = seed.Call(mauricefixtures.Call{ConversationID: w.conv, TurnID: w.turnOK, Provider: "anthropic",
		Model: "claude-sonnet-5-5", StartedAt: reportingT0, InputTokens: mauricefixtures.Tokens(10), SystemPrompt: &w.prompt})
	seed.CallInput(w.call, 0, question)
	w.final = seed.Call(mauricefixtures.Call{ConversationID: w.conv, TurnID: w.turnOK, Round: 1, ForcedFinal: true,
		Provider: "anthropic", Model: "claude-sonnet-5-5", StartedAt: reportingT0.Add(time.Second)})
	w.toolOK = seed.ToolCall(mauricefixtures.ToolCall{TurnID: w.turnOK, CallID: w.call, MessageID: request,
		Name: "get_player", ArgumentsRaw: `{"id":1}`, Result: "30 goals", StartedAt: reportingT0})
	seed.Message(mauricefixtures.Message{ConversationID: w.conv, TurnID: w.turnOK, Number: 2, Role: "tool",
		Content: "30 goals", ResultToolCallID: w.toolOK})
	seed.Message(mauricefixtures.Message{ConversationID: w.conv, TurnID: w.turnOK, Number: 3, Role: "assistant", Content: "30."})
	w.title = seed.Call(mauricefixtures.Call{ConversationID: w.conv, Provider: "anthropic", Model: "claude-haiku-5-5",
		StartedAt: reportingT0.Add(time.Minute)})

	w.turnFailed = seed.Turn(mauricefixtures.Turn{ConversationID: w.conv, UserID: owner, Number: 1,
		Status: mauricefixtures.StatusFailed, StartedAt: reportingT0.Add(2 * time.Minute)})
	seed.Message(mauricefixtures.Message{ConversationID: w.conv, TurnID: w.turnFailed, Role: "user", Content: "Assists?"})
	failedCall := seed.Call(mauricefixtures.Call{ConversationID: w.conv, TurnID: w.turnFailed, Provider: "anthropic",
		Model: "claude-sonnet-5-5", StartedAt: reportingT0.Add(2 * time.Minute), Failed: true})
	w.toolBad = seed.ToolCall(mauricefixtures.ToolCall{TurnID: w.turnFailed, CallID: failedCall, Name: "get_player",
		ArgumentsRaw: `{"id":`, Result: "invalid arguments", Status: "parse_error", StartedAt: reportingT0.Add(2 * time.Minute)})

	foreignTurn := seed.Turn(mauricefixtures.Turn{ConversationID: w.foreign, UserID: public, Status: mauricefixtures.StatusSucceeded, StartedAt: reportingT0})
	w.foreignCall = seed.Call(mauricefixtures.Call{ConversationID: w.foreign, TurnID: foreignTurn, Provider: "anthropic",
		Model: "claude-sonnet-5-5", StartedAt: reportingT0, SystemPrompt: mauricefixtures.Text("secret")})
	w.foreignTC = seed.ToolCall(mauricefixtures.ToolCall{TurnID: foreignTurn, CallID: w.foreignCall, Name: "get_player",
		ArgumentsRaw: "{}", Result: "secret", StartedAt: reportingT0})
	return w
}

func ref(instance, id string) map[string]any {
	return map[string]any{"instance": instance, "id": id}
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

const transcriptQuery = `query($ref: MauriceReportingConversationRef!, $limit: Int, $cursor: String) {
	mauriceReportingTranscript(conversation: $ref, limit: $limit, cursor: $cursor) {
		items { turnNumber turnStatus replayable number role contentLength content { text truncated } resultToolCallId toolCallCount }
		nextCursor
	}
}`

type transcriptData struct {
	Page *struct {
		Items []struct {
			TurnNumber    int    `json:"turnNumber"`
			TurnStatus    string `json:"turnStatus"`
			Replayable    bool   `json:"replayable"`
			Number        int    `json:"number"`
			Role          string `json:"role"`
			ContentLength int64  `json:"contentLength"`
			Content       *struct {
				Text      string `json:"text"`
				Truncated bool   `json:"truncated"`
			} `json:"content"`
			ResultToolCallID *string `json:"resultToolCallId"`
			ToolCallCount    int64   `json:"toolCallCount"`
		} `json:"items"`
		NextCursor *string `json:"nextCursor"`
	} `json:"mauriceReportingTranscript"`
}

func TestReportingTranscriptMarksEvidenceAndWithholdsToolResults(t *testing.T) {
	t.Parallel()
	server, seed, _ := newReportingDB(t)
	w := seedDetailGraphWorld(seed)

	got := gqlData[transcriptData](t, postGraphQL(t, server, adminHeaders(), transcriptQuery,
		map[string]any{"ref": ref("maurice", w.conv)}))
	require.NotNil(t, got.Page)
	items := got.Page.Items
	require.Len(t, items, 5)
	assert.Nil(t, got.Page.NextCursor)
	assert.Equal(t, "USER", items[0].Role)
	assert.Equal(t, "Goals?", items[0].Content.Text)
	assert.True(t, items[0].Replayable)
	assert.Equal(t, int64(1), items[1].ToolCallCount)
	assert.Equal(t, "TOOL", items[2].Role)
	assert.Nil(t, items[2].Content, "tool results only through mauriceReportingPayload")
	assert.Equal(t, int64(len("30 goals")), items[2].ContentLength)
	assert.Equal(t, itoa(w.toolOK), *items[2].ResultToolCallID)
	assert.Equal(t, "30.", items[3].Content.Text)
	assert.Equal(t, 1, items[4].TurnNumber)
	assert.Equal(t, "FAILED", items[4].TurnStatus)
	assert.False(t, items[4].Replayable, "failed turn evidence is not chat history")
	assert.Equal(t, "Assists?", items[4].Content.Text)
}

// limit and cursor reach the store: a page of one message, then the next
// one from its cursor.
func TestReportingTranscriptPagesWithLimitAndCursor(t *testing.T) {
	t.Parallel()
	server, seed, _ := newReportingDB(t)
	w := seedDetailGraphWorld(seed)
	vars := map[string]any{"ref": ref("maurice", w.conv), "limit": 1}

	first := gqlData[transcriptData](t, postGraphQL(t, server, adminHeaders(), transcriptQuery, vars))
	require.NotNil(t, first.Page)
	require.Len(t, first.Page.Items, 1)
	assert.Equal(t, "Goals?", first.Page.Items[0].Content.Text)
	require.NotNil(t, first.Page.NextCursor)

	vars["cursor"] = *first.Page.NextCursor
	second := gqlData[transcriptData](t, postGraphQL(t, server, adminHeaders(), transcriptQuery, vars))
	require.NotNil(t, second.Page)
	require.Len(t, second.Page.Items, 1)
	assert.Equal(t, 0, second.Page.Items[0].TurnNumber)
	assert.Equal(t, 1, second.Page.Items[0].Number)
	assert.Equal(t, "ASSISTANT", second.Page.Items[0].Role)
	require.NotNil(t, second.Page.NextCursor, "three messages remain")
	assert.NotEqual(t, *first.Page.NextCursor, *second.Page.NextCursor)
}

func TestReportingTurnsAndCallsKeepTitleCallsApart(t *testing.T) {
	t.Parallel()
	server, seed, _ := newReportingDB(t)
	w := seedDetailGraphWorld(seed)
	vars := map[string]any{"ref": ref("maurice", w.conv), "turn": w.turnOK}

	got := gqlData[struct {
		Turns struct {
			Items []struct {
				Status     string  `json:"status"`
				Replayable bool    `json:"replayable"`
				ErrorClass *string `json:"errorClass"`
				Usage      struct {
					Calls        int64 `json:"calls"`
					OutputTokens struct {
						Total *int64 `json:"total"`
					} `json:"outputTokens"`
				} `json:"usage"`
			} `json:"items"`
		} `json:"mauriceReportingTurns"`
		Calls struct {
			Items []struct {
				ID                 string  `json:"id"`
				TurnID             *string `json:"turnId"`
				Kind               string  `json:"kind"`
				Round              *int    `json:"round"`
				InputTokens        *int64  `json:"inputTokens"`
				OutputTokens       *int64  `json:"outputTokens"`
				SystemPromptLength *int64  `json:"systemPromptLength"`
			} `json:"items"`
		} `json:"mauriceReportingTurnCalls"`
		Titles struct {
			Items []struct {
				ID     string  `json:"id"`
				TurnID *string `json:"turnId"`
				Kind   string  `json:"kind"`
				Round  *int    `json:"round"`
			} `json:"items"`
		} `json:"mauriceReportingTitleCalls"`
	}](t, postGraphQL(t, server, adminHeaders(), `query($ref: MauriceReportingConversationRef!, $turn: String!) {
		mauriceReportingTurns(conversation: $ref) { items { status replayable errorClass usage { calls outputTokens { total } } } }
		mauriceReportingTurnCalls(conversation: $ref, turnId: $turn) { items { id turnId kind round inputTokens outputTokens systemPromptLength } }
		mauriceReportingTitleCalls(conversation: $ref) { items { id turnId kind round } }
	}`, vars))

	require.Len(t, got.Turns.Items, 2)
	assert.Equal(t, "SUCCEEDED", got.Turns.Items[0].Status)
	assert.True(t, got.Turns.Items[0].Replayable)
	assert.Nil(t, got.Turns.Items[0].Usage.OutputTokens.Total, "unreported: unknown, not zero")
	assert.Equal(t, "FAILED", got.Turns.Items[1].Status)
	assert.Equal(t, "provider_error", *got.Turns.Items[1].ErrorClass)

	require.Len(t, got.Calls.Items, 2)
	call := got.Calls.Items[0]
	assert.Equal(t, itoa(w.call), call.ID)
	assert.Equal(t, w.turnOK, *call.TurnID)
	assert.Equal(t, "chat_round", call.Kind)
	assert.Equal(t, 0, *call.Round)
	assert.Equal(t, int64(10), *call.InputTokens)
	assert.Nil(t, call.OutputTokens)
	assert.Equal(t, int64(len(w.prompt)), *call.SystemPromptLength)
	final := got.Calls.Items[1]
	assert.Equal(t, itoa(w.final), final.ID)
	assert.Equal(t, "forced_final", final.Kind)
	assert.Equal(t, 1, *final.Round)
	assert.Equal(t, int64(2), got.Turns.Items[0].Usage.Calls)

	require.Len(t, got.Titles.Items, 1)
	assert.Equal(t, itoa(w.title), got.Titles.Items[0].ID)
	assert.Nil(t, got.Titles.Items[0].TurnID, "title generation has no turn")
	assert.Nil(t, got.Titles.Items[0].Round)
	assert.Equal(t, "title_generation", got.Titles.Items[0].Kind)
}

const payloadDetailQuery = `query($ref: MauriceReportingConversationRef!, $call: String!, $tool: String!, $offset: Int) {
	mauriceReportingCallInput(conversation: $ref, callId: $call) {
		systemPrompt { length truncated nextOffset }
		inputs { number withheld messageId message { role content { text } } }
	}
	mauriceReportingToolCall(conversation: $ref, toolCallId: $tool) {
		toolCall { status argumentsValid } argumentsRaw { text } arguments { text } result { text }
	}
	mauriceReportingPayload(conversation: $ref, kind: SYSTEM_PROMPT, id: $call, offset: $offset) { text offset length truncated nextOffset }
}`

type payloadDetailData struct {
	Input *struct {
		SystemPrompt struct {
			Length     int64  `json:"length"`
			Truncated  bool   `json:"truncated"`
			NextOffset *int64 `json:"nextOffset"`
		} `json:"systemPrompt"`
		Inputs []struct {
			Number    int    `json:"number"`
			Withheld  bool   `json:"withheld"`
			MessageID string `json:"messageId"`
			Message   struct {
				Role    string `json:"role"`
				Content struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		} `json:"inputs"`
	} `json:"mauriceReportingCallInput"`
	Tool *struct {
		ToolCall struct {
			Status         string `json:"status"`
			ArgumentsValid bool   `json:"argumentsValid"`
		} `json:"toolCall"`
		ArgumentsRaw struct {
			Text string `json:"text"`
		} `json:"argumentsRaw"`
		Arguments *struct {
			Text string `json:"text"`
		} `json:"arguments"`
		Result *struct {
			Text string `json:"text"`
		} `json:"result"`
	} `json:"mauriceReportingToolCall"`
	Payload *struct {
		Text       string `json:"text"`
		Offset     int64  `json:"offset"`
		Length     int64  `json:"length"`
		Truncated  bool   `json:"truncated"`
		NextOffset *int64 `json:"nextOffset"`
	} `json:"mauriceReportingPayload"`
}

func TestReportingPayloadsLoadOnlyOnExplicitRequest(t *testing.T) {
	t.Parallel()
	server, seed, _ := newReportingDB(t)
	w := seedDetailGraphWorld(seed)

	got := gqlData[payloadDetailData](t, postGraphQL(t, server, adminHeaders(), payloadDetailQuery, map[string]any{
		"ref": ref("maurice", w.conv), "call": itoa(w.call), "tool": itoa(w.toolBad),
		"offset": mauricereport.InlineTextRunes}))
	require.NotNil(t, got.Input)
	prompt := got.Input.SystemPrompt
	assert.Equal(t, int64(len(w.prompt)), prompt.Length)
	assert.True(t, prompt.Truncated)
	require.NotNil(t, prompt.NextOffset)
	assert.Equal(t, int64(mauricereport.InlineTextRunes), *prompt.NextOffset)
	require.Len(t, got.Input.Inputs, 1)
	assert.Equal(t, "Goals?", got.Input.Inputs[0].Message.Content.Text)

	require.NotNil(t, got.Tool)
	assert.Equal(t, "parse_error", got.Tool.ToolCall.Status)
	assert.False(t, got.Tool.ToolCall.ArgumentsValid)
	assert.Equal(t, `{"id":`, got.Tool.ArgumentsRaw.Text, "malformed raw arguments kept as sent")
	assert.Nil(t, got.Tool.Arguments)
	assert.Equal(t, "invalid arguments", got.Tool.Result.Text)

	require.NotNil(t, got.Payload)
	assert.Equal(t, strings.Repeat("p", 10), got.Payload.Text, "the rest after the inline part")
	assert.Equal(t, int64(mauricereport.InlineTextRunes), got.Payload.Offset)
	assert.False(t, got.Payload.Truncated)
	assert.Nil(t, got.Payload.NextOffset)
}

// Nested IDs of another instance's conversation are null under this
// conversation, and so is the other conversation under this instance.
func TestReportingDetailRefusesForeignAndDeletedConversations(t *testing.T) {
	t.Parallel()
	server, seed, _ := newReportingDB(t)
	w := seedDetailGraphWorld(seed)
	null := map[string]any{"mauriceReportingCallInput": nil, "mauriceReportingToolCall": nil, "mauriceReportingPayload": nil}

	guessed := gqlData[map[string]any](t, postGraphQL(t, server, adminHeaders(), payloadDetailQuery, map[string]any{
		"ref": ref("maurice", w.conv), "call": itoa(w.foreignCall), "tool": itoa(w.foreignTC)}))
	assert.Equal(t, null, guessed, "another conversation's call and tool call IDs")
	foreign := gqlData[map[string]any](t, postGraphQL(t, server, adminHeaders(), payloadDetailQuery, map[string]any{
		"ref": ref("maurice", w.foreign), "call": itoa(w.foreignCall), "tool": itoa(w.foreignTC)}))
	assert.Equal(t, null, foreign, "the public conversation under the full instance")

	turnCalls := `query($ref: MauriceReportingConversationRef!, $turn: String!) {
		mauriceReportingTurnCalls(conversation: $ref, turnId: $turn) { items { id } }
		mauriceReportingToolCalls(conversation: $ref, turnId: $turn) { items { id } }
	}`
	other := gqlData[map[string]any](t, postGraphQL(t, server, adminHeaders(), turnCalls,
		map[string]any{"ref": ref("maurice-crapettes", w.foreign), "turn": w.turnOK}))
	assert.Equal(t, map[string]any{"mauriceReportingTurnCalls": nil, "mauriceReportingToolCalls": nil}, other,
		"a turn of another conversation")

	hidden := gqlData[transcriptData](t, postGraphQL(t, server, adminHeaders(), transcriptQuery,
		map[string]any{"ref": ref("maurice", w.deleted)}))
	assert.Nil(t, hidden.Page, "deleted: excluded by default")
	deleted := ref("maurice", w.deleted)
	deleted["includeDeleted"] = true
	shown := gqlData[transcriptData](t, postGraphQL(t, server, adminHeaders(), transcriptQuery, map[string]any{"ref": deleted}))
	require.NotNil(t, shown.Page, "explicitly included")
	assert.Empty(t, shown.Page.Items)
}

func TestReportingDetailRejectsInvalidArguments(t *testing.T) {
	t.Parallel()
	server, seed, _ := newReportingDB(t)
	w := seedDetailGraphWorld(seed)
	tests := []struct {
		name, query, want string
	}{
		{"call id", `query($ref: MauriceReportingConversationRef!) { mauriceReportingCallInput(conversation: $ref, callId: "x") { call { id } } }`,
			"invalid argument: callId is not an ID"},
		{"turn id", `query($ref: MauriceReportingConversationRef!) { mauriceReportingToolCalls(conversation: $ref, turnId: "x") { items { id } } }`,
			"invalid argument: turnId is not a UUID"},
		{"payload limit", `query($ref: MauriceReportingConversationRef!) { mauriceReportingPayload(conversation: $ref, kind: TOOL_RESULT, id: "1", limit: 0) { text } }`,
			"invalid argument: limit 0 is outside 1..262144"},
		{"payload offset", `query($ref: MauriceReportingConversationRef!) { mauriceReportingPayload(conversation: $ref, kind: TOOL_RESULT, id: "1", offset: -1) { text } }`,
			"invalid argument: offset -1 is outside 0..1073741824"},
		{"page limit", `query($ref: MauriceReportingConversationRef!) { mauriceReportingTurns(conversation: $ref, limit: 0) { items { id } } }`,
			"invalid argument: limit 0 is outside 1..200"},
		{"cursor", `query($ref: MauriceReportingConversationRef!) { mauriceReportingTurns(conversation: $ref, cursor: "bogus") { items { id } } }`,
			"invalid cursor"},
	}
	for _, tt := range tests {
		resp := postGraphQL(t, server, adminHeaders(), tt.query, map[string]any{"ref": ref("maurice", w.conv)})
		require.Len(t, resp.Errors, 1, tt.name)
		assert.Equal(t, tt.want, resp.Errors[0].Message, tt.name)
	}
}

// failingPayloadReader fails every payload read with a storage error that
// quotes stored content.
type failingPayloadReader struct{ recordingReader }

func (failingPayloadReader) Payload(context.Context, mauricereport.ConversationRef, mauricereport.PayloadRequest) (mauricereport.Text, bool, error) {
	return mauricereport.Text{}, false, errors.New(`payload tool_result: value "secret" rejected`)
}

func TestReportingDetailHidesStorageFaults(t *testing.T) {
	t.Parallel()
	server := newReportingServer(t, enabledReporting, failingPayloadReader{recordingReader{t}})
	resp := postGraphQL(t, server, adminHeaders(),
		`{ mauriceReportingPayload(conversation: {instance: "maurice", id: "0b5f3a52-8a4b-4a52-9d55-2a6fb3e6a001"}, kind: TOOL_RESULT, id: "1") { text } }`, nil)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, errMauriceInternal.Error(), resp.Errors[0].Message)
}
