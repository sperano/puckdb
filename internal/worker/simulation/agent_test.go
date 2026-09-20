package simulation

import (
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// resolveProvider
// ============================================================================

func TestResolveProvider_RecognizedNames(t *testing.T) {
	cases := map[string]llm.Provider{
		"anthropic":  llm.ProviderAnthropic,
		"Anthropic":  llm.ProviderAnthropic,
		"ANTHROPIC":  llm.ProviderAnthropic,
		"openai":     llm.ProviderOpenAI,
		"OpenAI":     llm.ProviderOpenAI,
		"ollama":     llm.ProviderOllama,
		"google":     llm.ProviderOpenAI, // Gemini speaks OpenAI-compatible
		"gemini":     llm.ProviderOpenAI,
		"  openai  ": llm.ProviderOpenAI, // whitespace tolerated
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got, err := resolveProvider(in)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}

func TestResolveProvider_RejectsEmpty(t *testing.T) {
	_, err := resolveProvider("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestResolveProvider_RejectsUnknown(t *testing.T) {
	_, err := resolveProvider("cohere")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cohere")
}

// ============================================================================
// NewAgent
// ============================================================================

func defaultProviderConfigs() map[llm.Provider]llm.ProviderConfig {
	return map[llm.Provider]llm.ProviderConfig{
		llm.ProviderAnthropic: {BaseURL: llm.AnthropicBaseURL, APIKey: "test-anthropic-key"},
		llm.ProviderOpenAI:    {BaseURL: llm.OpenAIBaseURL, APIKey: "test-openai-key"},
		llm.ProviderOllama:    {BaseURL: "http://localhost:11434/v1"},
	}
}

func TestNewAgent_CachesSystemPromptAndTools(t *testing.T) {
	cfg := AgentConfig{
		Provider: "anthropic",
		Model:    "claude-sonnet-4-7",
		Strategy: "punt PIM",
	}
	agent, err := NewAgent(1, cfg, defaultProviderConfigs(), 5)
	require.NoError(t, err)

	assert.Contains(t, agent.SystemPrompt(), "You are an AI fantasy hockey manager")
	assert.Contains(t, agent.SystemPrompt(), "5-team rotisserie")
	assert.Equal(t, len(DraftTools()), len(agent.DraftTools()))
	assert.Equal(t, len(DailyTools()), len(agent.DailyTools()))
	assert.NotNil(t, agent.Client)
}

// agentTimeout is the seam between AgentConfig and llm.WithTimeout.
// Test it directly because the resulting *http.Client's Timeout field
// lives in an unexported llm-package struct (openaiClient.httpClient)
// and isn't reachable from this test package. The wire-up of
// llm.WithTimeout → http.Client.Timeout is covered by tests in the
// llm package; here we just pin the AgentConfig → duration mapping
// so a future refactor can't silently turn a 45-second config into
// 45-millisecond by misreading the unit.
func TestAgentTimeout_Mapping(t *testing.T) {
	cases := []struct {
		seconds int
		want    time.Duration
	}{
		{0, 0},
		{-1, 0},
		{45, 45 * time.Second},
		{120, 120 * time.Second},
	}
	for _, tc := range cases {
		got := agentTimeout(AgentConfig{TimeoutSeconds: tc.seconds})
		assert.Equal(t, tc.want, got, "seconds=%d", tc.seconds)
	}
}

// Without a per-agent timeout, the agent's client uses the package
// default — pin construction succeeds with TimeoutSeconds=0 so a
// future refactor can't silently treat zero as "fail at construction"
// or "infinite timeout".
func TestNewAgent_NoTimeoutOverride_UsesDefault(t *testing.T) {
	cfg := AgentConfig{Provider: "anthropic", Model: "claude-haiku-4-5"}
	agent, err := NewAgent(1, cfg, defaultProviderConfigs(), 5)
	require.NoError(t, err)
	require.NotNil(t, agent.Client)
}

// APIBase override is the Gemini path: provider says "google" → routes
// through OpenAI-compatible client, and APIBase replaces the global
// OpenAI URL with Google's endpoint. Without the override, requests
// would go to api.openai.com and 401.
func TestNewAgent_APIBaseOverridesProviderURL(t *testing.T) {
	cfg := AgentConfig{
		Provider: "google",
		Model:    "gemini-2.0-flash",
		APIBase:  "https://generativelanguage.googleapis.com/v1beta/openai",
	}
	// Don't crash — that's the contract being pinned. Construction must
	// succeed even though the global OpenAI BaseURL isn't where requests
	// are actually going.
	agent, err := NewAgent(1, cfg, defaultProviderConfigs(), 5)
	require.NoError(t, err)
	require.NotNil(t, agent.Client)
}

func TestNewAgent_RejectsUnknownProvider(t *testing.T) {
	cfg := AgentConfig{Provider: "cohere", Model: "command-r"}
	_, err := NewAgent(1, cfg, defaultProviderConfigs(), 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cohere")
}

func TestNewAgent_RejectsMissingProviderConfig(t *testing.T) {
	// providerConfigs lacks Anthropic; an Anthropic agent must surface
	// a clear error rather than panic on a nil ProviderConfig.
	cfg := AgentConfig{Provider: "anthropic", Model: "claude-haiku-4-5"}
	bare := map[llm.Provider]llm.ProviderConfig{
		llm.ProviderOpenAI: {BaseURL: llm.OpenAIBaseURL, APIKey: "k"},
	}
	_, err := NewAgent(1, cfg, bare, 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no provider config")
	assert.Contains(t, err.Error(), "Anthropic")
}

// ============================================================================
// DraftMessages / DailyMessages — system message must be Cacheable.
// ============================================================================

func TestDraftMessages_SystemBlockIsCacheable(t *testing.T) {
	cfg := AgentConfig{Provider: "anthropic", Model: "claude-haiku-4-5"}
	agent, err := NewAgent(1, cfg, defaultProviderConfigs(), 5)
	require.NoError(t, err)

	msgs, err := agent.DraftMessages(DraftPromptInput{DraftInfo: DraftInfo{Round: 1, Pick: 1}})
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, "system", msgs[0].Role)
	assert.True(t, msgs[0].Cacheable, "system message must be Cacheable so the Anthropic translator marks the cache breakpoint")
	assert.Equal(t, "user", msgs[1].Role)
	assert.False(t, msgs[1].Cacheable, "the per-turn user payload must NOT be marked Cacheable")
}

func TestDailyMessages_SystemBlockIsCacheable(t *testing.T) {
	cfg := AgentConfig{Provider: "anthropic", Model: "claude-haiku-4-5"}
	agent, err := NewAgent(1, cfg, defaultProviderConfigs(), 5)
	require.NoError(t, err)

	msgs, err := agent.DailyMessages(DailyPromptInput{Day: "2025-11-15"})
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.True(t, msgs[0].Cacheable)
	assert.False(t, msgs[1].Cacheable)
}

// ============================================================================
// ParseAction — type-switchable dispatch + error path
// ============================================================================

func TestParseAction_AllSixTools(t *testing.T) {
	cases := []struct {
		name    string
		call    llm.ToolCall
		wantTyp Action
	}{
		{"draft_player", callOf(ToolDraftPlayer, `{"player_id":1}`), DraftPlayerArgs{PlayerID: 1}},
		{"set_lineup", callOf(ToolSetLineup, `{"moves":[]}`), SetLineupArgs{Moves: []LineupMoveArg{}}},
		{"add_player", callOf(ToolAddPlayer, `{"player_id":1}`), AddPlayerArgs{PlayerID: 1}},
		{"claim_player", callOf(ToolClaimPlayer, `{"player_id":1}`), ClaimPlayerArgs{PlayerID: 1}},
		{"drop_player", callOf(ToolDropPlayer, `{"player_id":1}`), DropPlayerArgs{PlayerID: 1}},
		{"update_notes", callOf(ToolUpdateNotes, `{"notes":"hi"}`), UpdateNotesArgs{Notes: "hi"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseAction(tc.call)
			require.NoError(t, err)
			assert.Equal(t, tc.wantTyp, got)
			assert.Equal(t, tc.name, got.ToolName(), "ToolName() must match the tool constant")
		})
	}
}

func TestParseAction_UnknownToolErrors(t *testing.T) {
	_, err := ParseAction(callOf("not_a_real_tool", "{}"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not_a_real_tool")
}

func TestParseAction_PropagatesParseErrors(t *testing.T) {
	_, err := ParseAction(callOf(ToolDraftPlayer, `{not json}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), ToolDraftPlayer)
}

// ToolName() must be defined on the Args structs so callers can
// type-switch and ALSO use a name-based map. Pin every implementation.
func TestActionToolNames(t *testing.T) {
	pairs := []struct {
		got  string
		want string
	}{
		{DraftPlayerArgs{}.ToolName(), ToolDraftPlayer},
		{SetLineupArgs{}.ToolName(), ToolSetLineup},
		{AddPlayerArgs{}.ToolName(), ToolAddPlayer},
		{ClaimPlayerArgs{}.ToolName(), ToolClaimPlayer},
		{DropPlayerArgs{}.ToolName(), ToolDropPlayer},
		{UpdateNotesArgs{}.ToolName(), ToolUpdateNotes},
	}
	for _, p := range pairs {
		assert.Equal(t, p.want, p.got)
	}
}

// callOf is a tiny test-helper to keep the table cases readable.
func callOf(name, args string) llm.ToolCall {
	return llm.ToolCall{
		ID:       "test-call",
		Type:     "function",
		Function: llm.ToolCallFunction{Name: name, Arguments: args},
	}
}
