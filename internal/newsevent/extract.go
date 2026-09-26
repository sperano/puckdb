package newsevent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sperano/puckdb/internal/llm"
)

const (
	// extractorKeySeparator joins the parts of an extractor key.
	extractorKeySeparator = "/"
	// finishReasonLength and stopReasonMaxTokens mark a reply cut by the
	// token limit (OpenAI-compatible and Anthropic wording).
	finishReasonLength  = "length"
	stopReasonMaxTokens = "max_tokens"
	// maxRawOutputRunes bounds the reply text kept for audit.
	maxRawOutputRunes = 20_000
)

// ErrTransport marks a failed model call (network, provider error): the
// extraction may be retried.
var ErrTransport = errors.New("model call failed")

// Extractor calls one model with the extraction prompt.
type Extractor struct {
	Client llm.Client
	// Provider and Model identify the model in the extractor key.
	Provider string
	Model    string
	// MaxOutputTokens bounds each reply.
	MaxOutputTokens int
}

// Key identifies the extractor: provider, model, prompt and schema version.
// Stored extractions are keyed by it, so changing any part re-extracts and
// keeps the earlier outputs as the audit trail.
func (x Extractor) Key() string {
	return ExtractorKey(x.Provider, x.Model)
}

// ExtractorKey is the key of a provider and model under the current prompt
// and schema; the provider name is case-insensitive.
func ExtractorKey(provider, model string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	return strings.Join([]string{provider, strings.TrimSpace(model), PromptVersion, SchemaVersion}, extractorKeySeparator)
}

// Reply is what one call returned.
type Reply struct {
	Content string
	Usage   llm.Usage
	// Rejected is set when the reply cannot be used at all (tool calls, cut
	// by the token limit); the extraction is invalid, not retried.
	Rejected error
}

// Call sends the extraction request. The request carries no tools: an
// article cannot make the model call anything, and a reply that tries is
// rejected. A transport failure is returned as ErrTransport.
func (x Extractor) Call(ctx context.Context, in Input) (Reply, error) {
	temperature := 0.0
	resp, err := x.Client.Complete(ctx, &llm.Request{
		Messages: []llm.Message{
			{Role: "system", Content: SystemPrompt(), Cacheable: true},
			{Role: "user", Content: UserMessage(in)},
		},
		MaxTokens:   x.MaxOutputTokens,
		Temperature: &temperature,
	})
	if err != nil {
		return Reply{}, fmt.Errorf("%w: %w", ErrTransport, err)
	}
	reply := Reply{Content: truncateRunes(resp.Content, maxRawOutputRunes)}
	if resp.Usage != nil {
		reply.Usage = *resp.Usage
	}
	switch {
	case resp.HasToolCalls():
		reply.Rejected = fmt.Errorf("%w: the reply requested %d tool calls; no tools are offered", ErrInvalidOutput, len(resp.ToolCalls))
	case resp.FinishReason == finishReasonLength || resp.FinishReason == stopReasonMaxTokens:
		reply.Rejected = fmt.Errorf("%w: the reply was cut at %d output tokens", ErrInvalidOutput, x.MaxOutputTokens)
	}
	return reply, nil
}
