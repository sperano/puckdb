package maurice

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sperano/puckdb/internal/llm"
)

// testListLimit is the /history limit the REPL tests configure.
const testListLimit = 7

var testREPLModels = []ModelEntry{
	{ID: "model-a", Description: "first model", Provider: llm.ProviderAnthropic},
	{ID: "model-b", Description: "second model", Provider: llm.ProviderOllama},
}

// fakeService is a Service recording what the REPL asks of it.
type fakeService struct {
	model string

	convs    []*Conversation
	listErr  error
	getErr   error
	chatResp *ChatResponse
	chatErr  error

	listLimits []int
	chats      []ChatRequest
	closed     int
}

func (f *fakeService) Chat(_ context.Context, req ChatRequest) (*ChatResponse, error) {
	f.chats = append(f.chats, req)
	if f.chatErr != nil {
		return nil, f.chatErr
	}
	if f.chatResp != nil {
		return f.chatResp, nil
	}
	return &ChatResponse{ConversationID: "chat-conv", Content: "answer"}, nil
}

func (f *fakeService) GetConversation(_ context.Context, userID, id string) (*Conversation, []*Message, error) {
	if userID != LocalUserID {
		return nil, nil, errors.New("unexpected user " + userID)
	}
	if f.getErr != nil {
		return nil, nil, f.getErr
	}
	for _, c := range f.convs {
		if c.ID == id {
			return c, nil, nil
		}
	}
	return nil, nil, ErrConversationNotFound
}

func (f *fakeService) ListConversations(_ context.Context, userID string, limit int) ([]*Conversation, error) {
	if userID != LocalUserID {
		return nil, errors.New("unexpected user " + userID)
	}
	f.listLimits = append(f.listLimits, limit)
	return f.convs, f.listErr
}

func (f *fakeService) DeleteConversation(context.Context, string, string) error { return nil }

func (f *fakeService) Close() error {
	f.closed++
	return nil
}

// serviceFactory builds fakeServices for the REPL and keeps every one built.
type serviceFactory struct {
	prototype fakeService
	built     []*fakeService
}

func (sf *serviceFactory) build(entry ModelEntry) Service {
	svc := sf.prototype
	svc.model = entry.ID
	sf.built = append(sf.built, &svc)
	return &svc
}

// last returns the service the REPL currently uses.
func (sf *serviceFactory) last(t *testing.T) *fakeService {
	t.Helper()
	require.NotEmpty(t, sf.built, "no service built")
	return sf.built[len(sf.built)-1]
}

type testREPL struct {
	*repl
	out, errOut *bytes.Buffer
	services    *serviceFactory
}

// newTestREPL returns a REPL on testREPLModels[0] whose services start as
// copies of prototype.
func newTestREPL(t *testing.T, prototype fakeService, providerConfigs map[llm.Provider]llm.ProviderConfig) *testREPL {
	t.Helper()
	md, err := glamour.NewTermRenderer(glamour.WithStandardStyle("notty"))
	require.NoError(t, err)
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	services := &serviceFactory{prototype: prototype}
	r := &repl{
		models:          testREPLModels,
		providerConfigs: providerConfigs,
		listLimit:       testListLimit,
		out:             out,
		errOut:          errOut,
		markdown:        md,
		spin:            newSpinner(out),
		newService:      services.build,
		entry:           testREPLModels[0],
	}
	r.svc = services.build(r.entry)
	return &testREPL{repl: r, out: out, errOut: errOut, services: services}
}

func strPtr(s string) *string { return &s }

func TestREPLHistory_ListsConversations(t *testing.T) {
	updated := time.Date(2026, 11, 8, 14, 30, 0, 0, time.UTC)
	r := newTestREPL(t, fakeService{convs: []*Conversation{
		{ID: "conv-1", Title: strPtr("Leafs goalies"), UpdatedAt: updated},
		{ID: "conv-2", UpdatedAt: updated.Add(time.Hour)},
	}}, nil)

	assert.False(t, r.handle(context.Background(), "/history"))

	out := r.out.String()
	assert.Contains(t, out, "conv-1")
	assert.Contains(t, out, "2026-11-08 14:30")
	assert.Contains(t, out, "Leafs goalies")
	assert.Contains(t, out, "conv-2")
	assert.Contains(t, out, "2026-11-08 15:30")
	assert.Contains(t, out, untitledConversation)
	assert.Empty(t, r.errOut.String())
	assert.Equal(t, []int{testListLimit}, r.services.last(t).listLimits)
}

func TestREPLHistory_Empty(t *testing.T) {
	r := newTestREPL(t, fakeService{}, nil)

	r.handle(context.Background(), "/history")

	assert.Contains(t, r.out.String(), "No conversations yet.")
	assert.Empty(t, r.errOut.String())
}

func TestREPLHistory_ErrorGoesToErrOut(t *testing.T) {
	r := newTestREPL(t, fakeService{listErr: errors.New("db locked")}, nil)

	r.handle(context.Background(), "/history")

	assert.Contains(t, r.errOut.String(), "Error: db locked")
	assert.Empty(t, r.out.String())
}

func TestREPLLoad_ResumesConversation(t *testing.T) {
	r := newTestREPL(t, fakeService{convs: []*Conversation{
		{ID: "conv-1", Title: strPtr("Leafs goalies")},
		{ID: "conv-2"},
	}}, nil)

	r.handle(context.Background(), "/load conv-1")
	assert.Contains(t, r.out.String(), "Resumed: Leafs goalies")
	require.NotNil(t, r.conversationID)
	assert.Equal(t, "conv-1", *r.conversationID)

	r.handle(context.Background(), "/load conv-2")
	assert.Contains(t, r.out.String(), "Resumed: "+untitledConversation)
	assert.Equal(t, "conv-2", *r.conversationID)

	r.handle(context.Background(), "next question")
	chats := r.services.last(t).chats
	require.Len(t, chats, 1)
	require.NotNil(t, chats[0].ConversationID)
	assert.Equal(t, "conv-2", *chats[0].ConversationID)
	assert.Empty(t, r.errOut.String())
}

func TestREPLLoad_WithoutIDPrintsUsage(t *testing.T) {
	// The REPL trims each line, so "/load " arrives as "/load": it must
	// print the usage instead of going to the LLM as a prompt.
	for _, input := range []string{"/load", "/load   "} {
		t.Run(input, func(t *testing.T) {
			r := newTestREPL(t, fakeService{}, nil)

			r.handle(context.Background(), input)

			assert.Contains(t, r.out.String(), "Usage: /load <conversation-id>")
			assert.Empty(t, r.services.last(t).chats)
			assert.Nil(t, r.conversationID)
		})
	}
}

func TestREPLLoad_UnknownConversationKeepsCurrent(t *testing.T) {
	r := newTestREPL(t, fakeService{convs: []*Conversation{{ID: "conv-1"}}}, nil)
	r.handle(context.Background(), "/load conv-1")
	r.out.Reset()

	r.handle(context.Background(), "/load missing")

	assert.Contains(t, r.errOut.String(), "Error: "+ErrConversationNotFound.Error())
	assert.NotContains(t, r.out.String(), "Resumed")
	require.NotNil(t, r.conversationID)
	assert.Equal(t, "conv-1", *r.conversationID)
}

func TestREPLModel_ShowsMenu(t *testing.T) {
	r := newTestREPL(t, fakeService{}, nil)

	r.handle(context.Background(), "/model")

	out := r.out.String()
	assert.Contains(t, out, "Current: model-a")
	assert.Contains(t, out, "1)")
	assert.Contains(t, out, "model-a")
	assert.Contains(t, out, "first model")
	assert.Contains(t, out, "2)")
	assert.Contains(t, out, "model-b")
	assert.Contains(t, out, "second model")
	assert.Contains(t, out, "/model <number>")
	assert.Len(t, r.services.built, 1, "showing the menu must not rebuild the service")
}

func TestREPLModel_InvalidSelectionKeepsModel(t *testing.T) {
	for _, arg := range []string{"0", "3", "-1", "two", "1.5"} {
		t.Run(arg, func(t *testing.T) {
			r := newTestREPL(t, fakeService{}, nil)

			r.handle(context.Background(), "/model "+arg)

			out := r.out.String()
			assert.Contains(t, out, "Invalid selection: "+arg)
			assert.Contains(t, out, "Current: model-a", "the menu follows the error")
			assert.Equal(t, testREPLModels[0], r.entry)
			assert.Len(t, r.services.built, 1)
			assert.Zero(t, r.services.last(t).closed)
		})
	}
}

func TestREPLModel_SwitchesServiceAndKeepsConversation(t *testing.T) {
	r := newTestREPL(t, fakeService{}, nil)
	r.handle(context.Background(), "first question")
	first := r.services.last(t)

	r.handle(context.Background(), "/model 2")

	require.Len(t, r.services.built, 2)
	assert.Equal(t, 1, first.closed, "the previous service is closed")
	second := r.services.last(t)
	assert.Equal(t, "model-b", second.model)
	assert.Zero(t, second.closed)
	assert.Equal(t, testREPLModels[1], r.entry)
	out := r.out.String()
	assert.Contains(t, out, "Switched to model-b")
	assert.Contains(t, out, "("+llm.ProviderOllama.String()+")")
	assert.NotContains(t, out, "no API key", "Ollama needs no API key")

	r.handle(context.Background(), "second question")
	require.Len(t, second.chats, 1)
	require.NotNil(t, second.chats[0].ConversationID)
	assert.Equal(t, "chat-conv", *second.chats[0].ConversationID)
}

func TestREPLModel_WarnsWithoutAPIKey(t *testing.T) {
	tests := []struct {
		name     string
		configs  map[llm.Provider]llm.ProviderConfig
		wantWarn bool
	}{
		{name: "no config", configs: nil, wantWarn: true},
		{name: "empty key", configs: map[llm.Provider]llm.ProviderConfig{llm.ProviderAnthropic: {}}, wantWarn: true},
		{name: "key set", configs: map[llm.Provider]llm.ProviderConfig{llm.ProviderAnthropic: {APIKey: "k"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTestREPL(t, fakeService{}, tt.configs)

			r.handle(context.Background(), "/model 1")

			out := r.out.String()
			assert.Contains(t, out, "Switched to model-a")
			if tt.wantWarn {
				assert.Contains(t, out, "Warning: no API key configured for "+llm.ProviderAnthropic.String())
			} else {
				assert.NotContains(t, out, "no API key")
			}
		})
	}
}

func TestREPLNew_ClearsConversation(t *testing.T) {
	r := newTestREPL(t, fakeService{}, nil)
	r.handle(context.Background(), "first question")
	require.NotNil(t, r.conversationID)

	r.handle(context.Background(), "/new")
	r.handle(context.Background(), "fresh question")

	assert.Contains(t, r.out.String(), "Started new conversation.")
	chats := r.services.last(t).chats
	require.Len(t, chats, 2)
	assert.Nil(t, chats[1].ConversationID)
}

func TestREPLChat_PrintsAnswerWarningsAndTools(t *testing.T) {
	r := newTestREPL(t, fakeService{chatResp: &ChatResponse{
		ConversationID: "conv-9",
		Content:        "Goal leaders:\n\n- Matthews\n- Draisaitl\n",
		ToolsUsed:      []string{"get_skater_stats", "get_standings"},
		Warnings:       []string{"MCP server news unavailable"},
	}}, nil)

	r.handle(context.Background(), "who leads in goals?")

	chats := r.services.last(t).chats
	require.Len(t, chats, 1)
	assert.Equal(t, ChatRequest{UserID: LocalUserID, Message: "who leads in goals?"}, chats[0])
	out := r.out.String()
	assert.Contains(t, out, "Warning: MCP server news unavailable")
	assert.Contains(t, out, "tools: get_skater_stats, get_standings")
	// Rendered Markdown, not the raw answer: list items get bullets.
	assert.Contains(t, out, "• Matthews")
	assert.Contains(t, out, "• Draisaitl")
	assert.NotContains(t, out, "- Matthews")
	require.NotNil(t, r.conversationID)
	assert.Equal(t, "conv-9", *r.conversationID)
	assert.Empty(t, r.errOut.String())
}

func TestREPLChat_ErrorKeepsConversation(t *testing.T) {
	r := newTestREPL(t, fakeService{convs: []*Conversation{{ID: "conv-1"}}}, nil)
	r.handle(context.Background(), "/load conv-1")
	svc := r.services.last(t)
	svc.chatErr = errors.New("rate limited")

	r.handle(context.Background(), "who leads in goals?")

	assert.Contains(t, r.errOut.String(), "Error: rate limited")
	require.NotNil(t, r.conversationID)
	assert.Equal(t, "conv-1", *r.conversationID)

	svc.chatErr = nil
	r.handle(context.Background(), "who leads in assists?")
	require.Len(t, svc.chats, 2)
	for _, chat := range svc.chats {
		require.NotNil(t, chat.ConversationID, chat.Message)
		assert.Equal(t, "conv-1", *chat.ConversationID, chat.Message)
	}
}

func TestREPLHandle_CommandArgumentAfterAnyWhitespace(t *testing.T) {
	separators := map[string]string{
		"tab":            "\t",
		"vertical tab":   "\v",
		"form feed":      "\f",
		"no-break space": "\u00a0",
	}
	for name, sep := range separators {
		t.Run(name, func(t *testing.T) {
			r := newTestREPL(t, fakeService{convs: []*Conversation{{ID: "conv-1"}}}, nil)

			r.handle(context.Background(), "/load"+sep+"conv-1")
			r.handle(context.Background(), "/model"+sep+"2")

			require.NotNil(t, r.conversationID)
			assert.Equal(t, "conv-1", *r.conversationID)
			assert.Equal(t, testREPLModels[1], r.entry)
			for _, svc := range r.services.built {
				assert.Empty(t, svc.chats, "a command is not a prompt")
			}
		})
	}
}

func TestREPLHandle_CommandPrefixIsAPrompt(t *testing.T) {
	r := newTestREPL(t, fakeService{}, nil)

	r.handle(context.Background(), "/loadconv-1")

	chats := r.services.last(t).chats
	require.Len(t, chats, 1)
	assert.Equal(t, "/loadconv-1", chats[0].Message)
	assert.NotContains(t, r.out.String(), "Usage")
}

func TestREPLHandle_QuitAndEmptyInput(t *testing.T) {
	r := newTestREPL(t, fakeService{}, nil)

	assert.True(t, r.handle(context.Background(), "/quit"))
	assert.True(t, r.handle(context.Background(), "/exit"))
	assert.False(t, r.handle(context.Background(), ""))
	assert.Empty(t, r.services.last(t).chats, "empty input is not a prompt")
	assert.Empty(t, r.out.String())
}
