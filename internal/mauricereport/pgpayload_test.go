package mauricereport

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/sperano/puckdb/internal/fixtures/mauricefixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func idText(id int64) string {
	return strconv.FormatInt(id, 10)
}

func TestCallInputReturnsExactInputsInOrder(t *testing.T) {
	store, seed := openStore(t)
	w := seedDetailWorld(seed)
	ctx := context.Background()

	first, found, err := store.CallInput(ctx, fullRef(w.conv), idText(w.callFirst), PageRequest{})
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, w.callFirst, first.Call.ID)
	prompt := first.SystemPrompt
	require.NotNil(t, prompt)
	assert.Equal(t, string([]rune(w.systemPrompt)[:InlineTextRunes]), prompt.Text)
	assert.Equal(t, int64(len([]rune(w.systemPrompt))), prompt.Length)
	assert.True(t, prompt.Truncated())
	assert.Equal(t, int64(InlineTextRunes), prompt.End())
	assert.Nil(t, first.Instruction, "not stored")
	require.NotNil(t, first.ToolDefinitions)
	assert.Equal(t, `[{"name": "get_player"}]`, first.ToolDefinitions.Text)
	require.Len(t, first.Inputs, 1)
	assert.Equal(t, w.msgUser, first.Inputs[0].Message.ID)

	page, found, err := store.CallInput(ctx, fullRef(w.conv), idText(w.callSecond), PageRequest{Limit: 2})
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, page.Inputs, 2)
	require.NotEmpty(t, page.NextCursor)
	rest, _, err := store.CallInput(ctx, fullRef(w.conv), idText(w.callSecond), PageRequest{Limit: 2, Cursor: page.NextCursor})
	require.NoError(t, err)
	require.Len(t, rest.Inputs, 1)
	assert.Empty(t, rest.NextCursor)
	tool := rest.Inputs[0]
	assert.Equal(t, int32(2), tool.Number)
	assert.Equal(t, w.msgTool, tool.Message.ID)
	assert.Equal(t, "tool", tool.Message.Role)
	require.NotNil(t, tool.Message.Content, "a call's inputs carry every role")
	assert.Equal(t, w.toolResult[:InlineTextRunes], tool.Message.Content.Text)
	assert.True(t, tool.Message.Content.Truncated())

	title, found, err := store.CallInput(ctx, fullRef(w.conv), idText(w.titleCall), PageRequest{})
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, &Text{Text: "Title this.", Length: 11}, title.Instruction)
	require.Len(t, title.Inputs, 1)
	assert.Equal(t, "How many goals?", title.Inputs[0].Message.Content.Text)
}

// A call input linked to another conversation's message is reported as
// withheld, with its ID only.
func TestCallInputWithholdsAnotherConversationsMessage(t *testing.T) {
	store, seed := openStore(t)
	w := seedDetailWorld(seed)
	seed.CallInput(w.callFirst, 1, w.foreignMsg)

	got, found, err := store.CallInput(context.Background(), fullRef(w.conv), idText(w.callFirst), PageRequest{})
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, got.Inputs, 2)
	assert.Equal(t, InputMessage{Number: 1, Withheld: true, Message: Message{ID: w.foreignMsg}}, got.Inputs[1])
}

func TestToolCallDetailKeepsMalformedRawArguments(t *testing.T) {
	store, seed := openStore(t)
	w := seedDetailWorld(seed)
	ctx := context.Background()

	malformed, found, err := store.ToolCall(ctx, fullRef(w.conv), idText(w.toolMalformed))
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, Text{Text: malformedArgs, Length: int64(len(malformedArgs))}, malformed.ArgumentsRaw)
	assert.Nil(t, malformed.Arguments, "did not parse")
	assert.Equal(t, &Text{Text: "parse error", Length: 11}, malformed.Result)

	ok, found, err := store.ToolCall(ctx, fullRef(w.conv), idText(w.toolOK))
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, `{"id":8478402}`, ok.ArgumentsRaw.Text)
	assert.Equal(t, `{"id": 8478402}`, ok.Arguments.Text, "the parsed projection")
	assert.Equal(t, w.toolResult[:InlineTextRunes], ok.Result.Text)
	assert.Equal(t, int64(InlineTextRunes+1), ok.Result.Length)
	assert.True(t, ok.Result.Truncated())

	cancelled, found, err := store.ToolCall(ctx, fullRef(w.conv), idText(w.toolCancelled))
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, w.toolCancelled, cancelled.ToolCall.ID)
	assert.Equal(t, mauricefixtures.StatusCancelled, *cancelled.ToolCall.Status)
	assert.Equal(t, Text{Text: "{}", Length: 2}, cancelled.ArgumentsRaw)
	assert.Nil(t, cancelled.Result, "never ran")
}

// Windows read with Payload's continuation rebuild the stored text exactly,
// multi-byte code points included.
func TestPayloadWindowsRebuildTheStoredText(t *testing.T) {
	store, seed := openStore(t)
	w := seedDetailWorld(seed)
	ctx := context.Background()
	const window = 1000

	var rebuilt strings.Builder
	offset := int64(0)
	for reads := 0; ; reads++ {
		require.Less(t, reads, len(w.systemPrompt), "reading must end")
		got, found, err := store.Payload(ctx, fullRef(w.conv), PayloadRequest{
			Kind: PayloadSystemPrompt, ID: idText(w.callFirst), Offset: offset, Limit: window})
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, offset, got.Offset)
		rebuilt.WriteString(got.Text)
		if !got.Truncated() {
			break
		}
		offset = got.End()
	}
	assert.Equal(t, w.systemPrompt, rebuilt.String())

	past, found, err := store.Payload(ctx, fullRef(w.conv), PayloadRequest{
		Kind: PayloadToolResult, ID: idText(w.toolOK), Offset: InlineTextRunes + 5})
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, Text{Offset: InlineTextRunes + 5, Length: InlineTextRunes + 1}, past, "past the end: empty")

	whole, _, err := store.Payload(ctx, fullRef(w.conv), PayloadRequest{Kind: PayloadToolResult, ID: idText(w.toolOK)})
	require.NoError(t, err)
	assert.Equal(t, w.toolResult, whole.Text, "the default window holds it")
}

func TestPayloadReadsEveryKind(t *testing.T) {
	store, seed := openStore(t)
	w := seedDetailWorld(seed)
	tests := []struct {
		kind PayloadKind
		id   string
		want string
	}{
		{PayloadMessageContent, w.msgTool, w.toolResult},
		{PayloadInstruction, idText(w.titleCall), "Title this."},
		{PayloadToolDefinitions, idText(w.callFirst), `[{"name": "get_player"}]`},
		{PayloadToolArgumentsRaw, idText(w.toolMalformed), malformedArgs},
		{PayloadToolArguments, idText(w.toolOK), `{"id": 8478402}`},
		{PayloadToolResult, idText(w.toolMalformed), "parse error"},
	}
	for _, tt := range tests {
		got, found, err := store.Payload(context.Background(), fullRef(w.conv), PayloadRequest{Kind: tt.kind, ID: tt.id})
		require.NoError(t, err, tt.kind)
		require.True(t, found, tt.kind)
		assert.Equal(t, tt.want, got.Text, tt.kind)
	}
}

// Guessed nested IDs of another conversation, or of no stored value, are
// not found, though the conversation itself is.
func TestNestedIDsOfAnotherConversationAreNotFound(t *testing.T) {
	store, seed := openStore(t)
	w := seedDetailWorld(seed)
	ctx := context.Background()
	ref := fullRef(w.conv)

	_, found, err := store.TurnCalls(ctx, ref, w.foreignTurn, PageRequest{})
	require.NoError(t, err)
	assert.False(t, found, "turn calls")
	_, found, err = store.ToolCalls(ctx, ref, w.foreignTurn, PageRequest{})
	require.NoError(t, err)
	assert.False(t, found, "tool calls")
	for _, call := range []int64{w.foreignCall, w.foreignTitle} {
		_, found, err = store.CallInput(ctx, ref, idText(call), PageRequest{})
		require.NoError(t, err)
		assert.False(t, found, "call input %d", call)
	}
	_, found, err = store.ToolCall(ctx, ref, idText(w.foreignTool))
	require.NoError(t, err)
	assert.False(t, found, "tool call")

	foreignPayloads := []PayloadRequest{
		{Kind: PayloadMessageContent, ID: w.foreignMsg},
		{Kind: PayloadSystemPrompt, ID: idText(w.foreignCall)},
		{Kind: PayloadInstruction, ID: idText(w.foreignTitle)},
		{Kind: PayloadToolDefinitions, ID: idText(w.foreignCall)},
		{Kind: PayloadToolArgumentsRaw, ID: idText(w.foreignTool)},
		{Kind: PayloadToolArguments, ID: idText(w.foreignTool)},
		{Kind: PayloadToolResult, ID: idText(w.foreignTool)},
	}
	own := ConversationRef{Instance: instanceCrapettes, ConversationID: w.foreign}
	for _, req := range foreignPayloads {
		_, found, err := store.Payload(ctx, own, req)
		require.NoError(t, err)
		require.True(t, found, "%s %s: stored, under its own conversation", req.Kind, req.ID)
	}
	payloads := append(foreignPayloads, []PayloadRequest{
		// Stored nothing: no instruction, unparsed arguments, never ran.
		{Kind: PayloadInstruction, ID: idText(w.callFirst)},
		{Kind: PayloadToolArguments, ID: idText(w.toolMalformed)},
		{Kind: PayloadToolResult, ID: idText(w.toolCancelled)},
	}...)
	for _, req := range payloads {
		got, found, err := store.Payload(ctx, ref, req)
		require.NoError(t, err)
		assert.False(t, found, "%s %s", req.Kind, req.ID)
		assert.Equal(t, Text{}, got)
	}
}

func TestDetailReadsRejectMalformedInput(t *testing.T) {
	store, seed := openStore(t)
	w := seedDetailWorld(seed)
	ctx := context.Background()
	ref := fullRef(w.conv)

	_, _, err := store.Transcript(ctx, ConversationRef{Instance: instanceFull, ConversationID: "c1"}, PageRequest{})
	assert.EqualError(t, err, "invalid argument: conversationId is not a UUID")
	_, _, err = store.Turns(ctx, ConversationRef{ConversationID: w.conv}, PageRequest{})
	assert.EqualError(t, err, "invalid argument: instance is required")
	_, _, err = store.TurnCalls(ctx, ref, "t1", PageRequest{})
	assert.EqualError(t, err, "invalid argument: turnId is not a UUID")
	_, _, err = store.CallInput(ctx, ref, "01", PageRequest{})
	assert.EqualError(t, err, "invalid argument: callId is not an ID")
	_, _, err = store.ToolCall(ctx, ref, "-3")
	assert.EqualError(t, err, "invalid argument: toolCallId is not an ID")
	_, _, err = store.Payload(ctx, ref, PayloadRequest{Kind: "body", ID: "1"})
	assert.EqualError(t, err, `invalid argument: unknown payload kind "body"`)
	_, _, err = store.Payload(ctx, ref, PayloadRequest{Kind: PayloadMessageContent, ID: "1"})
	assert.EqualError(t, err, "invalid argument: id is not a UUID")
	_, _, err = store.Payload(ctx, ref, PayloadRequest{Kind: PayloadToolResult, ID: w.msgUser})
	assert.EqualError(t, err, "invalid argument: id is not an ID")
	_, _, err = store.Payload(ctx, ref, PayloadRequest{Kind: PayloadToolResult, ID: "1", Limit: MaxPayloadRunes + 1})
	assert.EqualError(t, err, "invalid argument: limit 262145 is outside 1..262144")
	_, _, err = store.Transcript(ctx, ref, PageRequest{Limit: MaxLimit + 1})
	assert.EqualError(t, err, "invalid argument: limit 201 is outside 1..200")

	turns, _, err := store.Turns(ctx, ref, PageRequest{Limit: 1})
	require.NoError(t, err)
	_, _, err = store.Transcript(ctx, ref, PageRequest{Cursor: turns.NextCursor})
	assert.ErrorIs(t, err, ErrInvalidCursor, "another list's cursor")
	_, _, err = store.Turns(ctx, fullRef(w.deleted), PageRequest{Cursor: turns.NextCursor})
	assert.ErrorIs(t, err, ErrInvalidCursor, "another conversation's cursor")
}
