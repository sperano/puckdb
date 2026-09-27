package newsevent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/news"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const extractTestMaxTokens = 2048

// extractFakeClient is a scripted llm.Client: it records the request it
// received and returns a canned response or error.
type extractFakeClient struct {
	resp *llm.Response
	err  error
	req  *llm.Request
}

func (f *extractFakeClient) Complete(_ context.Context, req *llm.Request) (*llm.Response, error) {
	f.req = req
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func extractTestInput() Input {
	return Input{
		Players: []Player{{Ref: "P1", Identity: news.Identity{NHLPlayerID: 1, Name: "Test Player"}}},
		Documents: []Document{{
			Ref: "E1", Publisher: "NHL.com", Kind: news.KindOfficial, Title: "Title", Text: "Body text.",
			ReportedAt: time.Date(2026, time.October, 6, 0, 0, 0, 0, time.UTC),
		}},
	}
}

func TestExtractorCallRequestShape(t *testing.T) {
	client := &extractFakeClient{resp: &llm.Response{Content: `{"events": []}`, FinishReason: "stop"}}
	x := Extractor{Client: client, Provider: "anthropic", Model: "claude", MaxOutputTokens: extractTestMaxTokens}
	in := extractTestInput()

	reply, err := x.Call(context.Background(), in)
	require.NoError(t, err)
	require.NoError(t, reply.Rejected)
	assert.Equal(t, `{"events": []}`, reply.Content)

	require.NotNil(t, client.req)
	assert.Empty(t, client.req.Tools, "the request must carry no tools")
	require.NotNil(t, client.req.Temperature)
	assert.Equal(t, 0.0, *client.req.Temperature)
	assert.Equal(t, extractTestMaxTokens, client.req.MaxTokens)

	require.Len(t, client.req.Messages, 2)
	assert.Equal(t, "system", client.req.Messages[0].Role)
	assert.Equal(t, SystemPrompt(), client.req.Messages[0].Content)
	assert.Equal(t, "user", client.req.Messages[1].Role)
	assert.Equal(t, UserMessage(in), client.req.Messages[1].Content)
}

func TestExtractorCallRejectsToolCalls(t *testing.T) {
	client := &extractFakeClient{resp: &llm.Response{
		Content:      "",
		ToolCalls:    []llm.ToolCall{{ID: "1", Type: "function", Function: llm.ToolCallFunction{Name: "lookup"}}},
		FinishReason: "tool_calls",
	}}
	x := Extractor{Client: client, MaxOutputTokens: extractTestMaxTokens}

	reply, err := x.Call(context.Background(), extractTestInput())
	require.NoError(t, err)
	require.Error(t, reply.Rejected)
	assert.ErrorIs(t, reply.Rejected, ErrInvalidOutput)
}

func TestExtractorCallRejectsTruncatedReply(t *testing.T) {
	for _, finish := range []string{finishReasonLength, stopReasonMaxTokens} {
		t.Run(finish, func(t *testing.T) {
			client := &extractFakeClient{resp: &llm.Response{Content: "partial", FinishReason: finish}}
			x := Extractor{Client: client, MaxOutputTokens: extractTestMaxTokens}

			reply, err := x.Call(context.Background(), extractTestInput())
			require.NoError(t, err)
			require.Error(t, reply.Rejected)
			assert.ErrorIs(t, reply.Rejected, ErrInvalidOutput)
		})
	}
}

func TestExtractorCallTransportError(t *testing.T) {
	client := &extractFakeClient{err: errors.New("connection refused")}
	x := Extractor{Client: client, MaxOutputTokens: extractTestMaxTokens}

	_, err := x.Call(context.Background(), extractTestInput())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTransport)
}

func TestExtractorCallCopiesUsage(t *testing.T) {
	usage := llm.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150}
	client := &extractFakeClient{resp: &llm.Response{Content: `{"events": []}`, FinishReason: "stop", Usage: &usage}}
	x := Extractor{Client: client, MaxOutputTokens: extractTestMaxTokens}

	reply, err := x.Call(context.Background(), extractTestInput())
	require.NoError(t, err)
	assert.Equal(t, usage, reply.Usage)
}

func TestExtractorKeyNormalizesProvider(t *testing.T) {
	x := Extractor{Provider: "  Anthropic  ", Model: "claude-x"}
	key := x.Key()

	assert.Equal(t, ExtractorKey("anthropic", "claude-x", ""), key)
	assert.Contains(t, key, "anthropic")
	assert.Contains(t, key, PromptVersion)
	assert.Contains(t, key, SchemaVersion)
	assert.Equal(t, ExtractorKey("ANTHROPIC", "m", ""), ExtractorKey(" anthropic ", "m", ""),
		"provider case and surrounding whitespace must not matter")
}

func TestExtractorKeyWithoutReasoningEffortKeepsEarlierKeys(t *testing.T) {
	key := ExtractorKey("anthropic", "claude-x", "  ")

	assert.Equal(t, "anthropic/claude-x/"+PromptVersion+"/"+SchemaVersion, key,
		"an unset effort must leave the keys of stored extractions and evaluations unchanged")
}

func TestExtractorKeyIncludesReasoningEffort(t *testing.T) {
	x := Extractor{Provider: "ollama", Model: "qwen", ReasoningEffort: " None "}

	assert.Equal(t, "ollama/qwen/reasoning-none/"+PromptVersion+"/"+SchemaVersion, x.Key())
	assert.NotEqual(t, ExtractorKey("ollama", "qwen", ""), x.Key(),
		"an evaluation without the effort must not release the extractor that sets it")
}

func TestCallSendsReasoningEffort(t *testing.T) {
	client := &extractFakeClient{resp: &llm.Response{Content: `{"events": []}`, FinishReason: "stop"}}
	x := Extractor{Client: client, ReasoningEffort: " None ", MaxOutputTokens: extractTestMaxTokens}

	_, err := x.Call(context.Background(), extractTestInput())
	require.NoError(t, err)
	assert.Equal(t, "none", client.req.ReasoningEffort)
}

func TestParseReasoningEffort(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty_keeps_provider_default", "", "", false},
		{"normalized", " None ", "none", false},
		{"high", "high", "high", false},
		{"typo_rejected", "off", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseReasoningEffort(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
