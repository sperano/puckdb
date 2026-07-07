package llm

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// applyOptions returns the http.Client used by both client implementations.
// We test it directly because the constructed client.httpClient is the
// only concrete signal that the option chain produced the right state.

func TestApplyOptions_DefaultTimeout(t *testing.T) {
	hc := applyOptions(nil)
	require.NotNil(t, hc)
	assert.Equal(t, defaultHTTPTimeout, hc.Timeout)
}

func TestApplyOptions_WithTimeout(t *testing.T) {
	hc := applyOptions([]Option{WithTimeout(45 * time.Second)})
	assert.Equal(t, 45*time.Second, hc.Timeout)
}

// WithTimeout(0) — and negative values — fall through to http.Client's
// zero-value semantics, which means "no timeout". Pin this so a future
// "clamp to default if non-positive" change is an explicit decision.
func TestApplyOptions_WithTimeoutZero_NoTimeout(t *testing.T) {
	hc := applyOptions([]Option{WithTimeout(0)})
	assert.Equal(t, time.Duration(0), hc.Timeout)
}

// Last option wins — standard functional-options semantics.
func TestApplyOptions_LastOptionWins(t *testing.T) {
	hc := applyOptions([]Option{
		WithTimeout(10 * time.Second),
		WithTimeout(99 * time.Second),
	})
	assert.Equal(t, 99*time.Second, hc.Timeout)
}

// WithHTTPClient supersedes WithTimeout regardless of order. Pin this so a
// caller wiring a custom transport (test or shared connection pool) doesn't
// have a stray WithTimeout silently overriding their http.Client.
func TestApplyOptions_WithHTTPClientSupersedesTimeout(t *testing.T) {
	custom := &http.Client{Timeout: 7 * time.Second}

	t.Run("custom client first, timeout after", func(t *testing.T) {
		hc := applyOptions([]Option{
			WithHTTPClient(custom),
			WithTimeout(99 * time.Second),
		})
		assert.Same(t, custom, hc, "WithHTTPClient must win even when WithTimeout follows")
	})
	t.Run("timeout first, custom client after", func(t *testing.T) {
		hc := applyOptions([]Option{
			WithTimeout(99 * time.Second),
			WithHTTPClient(custom),
		})
		assert.Same(t, custom, hc)
	})
}

// Constructors must thread options through to the http.Client they hold.
// This is the integration assertion that ties the option chain to the
// concrete client struct.
func TestNewOpenAIClient_AppliesTimeoutOption(t *testing.T) {
	c := NewOpenAIClient("http://x", "", "m", WithTimeout(33*time.Second))
	oc, ok := c.(*openaiClient)
	require.True(t, ok)
	assert.Equal(t, 33*time.Second, oc.httpClient.Timeout)
}

func TestNewAnthropicClient_AppliesTimeoutOption(t *testing.T) {
	c := NewAnthropicClient("http://x", "k", "m", WithTimeout(33*time.Second))
	ac, ok := c.(*anthropicClient)
	require.True(t, ok)
	assert.Equal(t, 33*time.Second, ac.httpClient.Timeout)
}

func TestNewOpenAIClient_DefaultTimeoutWhenNoOpts(t *testing.T) {
	c := NewOpenAIClient("http://x", "", "m")
	oc := c.(*openaiClient)
	assert.Equal(t, defaultHTTPTimeout, oc.httpClient.Timeout)
}

func TestNewAnthropicClient_DefaultTimeoutWhenNoOpts(t *testing.T) {
	c := NewAnthropicClient("http://x", "k", "m")
	ac := c.(*anthropicClient)
	assert.Equal(t, defaultHTTPTimeout, ac.httpClient.Timeout)
}

// NewClientForProvider must forward options. Pin both branches
// (Anthropic / OpenAI-compatible) so a future refactor can't silently
// drop the variadic.
func TestNewClientForProvider_ForwardsOptions(t *testing.T) {
	cfg := ProviderConfig{BaseURL: "http://x", APIKey: "k"}

	t.Run("anthropic branch", func(t *testing.T) {
		c := NewClientForProvider(ProviderAnthropic, cfg, "m", WithTimeout(7*time.Second))
		ac := c.(*anthropicClient)
		assert.Equal(t, 7*time.Second, ac.httpClient.Timeout)
	})
	t.Run("openai-compatible branch", func(t *testing.T) {
		c := NewClientForProvider(ProviderOpenAI, cfg, "m", WithTimeout(7*time.Second))
		oc := c.(*openaiClient)
		assert.Equal(t, 7*time.Second, oc.httpClient.Timeout)
	})
}
