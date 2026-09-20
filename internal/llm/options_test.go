package llm

import (
	"errors"
	"net/http"
	"net/http/cookiejar"
	"sync"
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
		assert.NotSame(t, custom, hc, "caller-owned clients must be copied")
		assert.Equal(t, custom.Timeout, hc.Timeout)
	})
	t.Run("timeout first, custom client after", func(t *testing.T) {
		hc := applyOptions([]Option{
			WithTimeout(99 * time.Second),
			WithHTTPClient(custom),
		})
		assert.NotSame(t, custom, hc)
		assert.Equal(t, custom.Timeout, hc.Timeout)
	})
}

func TestConstructors_CopyCallerOwnedHTTPClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		construct func(*http.Client) *http.Client
	}{
		{
			name: "OpenAI",
			construct: func(client *http.Client) *http.Client {
				return NewOpenAIClient("http://example.com", "", "model", WithHTTPClient(client)).(*openaiClient).httpClient
			},
		},
		{
			name: "Anthropic",
			construct: func(client *http.Client) *http.Client {
				return NewAnthropicClient("http://example.com", "key", "model", WithHTTPClient(client)).(*anthropicClient).httpClient
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			transport := &http.Transport{}
			jar, err := cookiejar.New(nil)
			require.NoError(t, err)
			redirectCalls := 0
			originalRedirect := func(_ *http.Request, _ []*http.Request) error {
				redirectCalls++
				return nil
			}
			original := &http.Client{
				Transport:     transport,
				CheckRedirect: originalRedirect,
				Jar:           jar,
				Timeout:       17 * time.Second,
			}

			constructed := test.construct(original)

			assert.NotSame(t, original, constructed)
			assert.Same(t, transport, constructed.Transport)
			assert.Same(t, jar, constructed.Jar)
			assert.Equal(t, original.Timeout, constructed.Timeout)
			require.ErrorIs(t, constructed.CheckRedirect(nil, nil), http.ErrUseLastResponse)
			require.NoError(t, original.CheckRedirect(nil, nil))
			assert.Equal(t, 1, redirectCalls)
		})
	}
}

func TestConstructors_ConcurrentUseDoesNotMutateCallerClient(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("caller redirect policy")
	original := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return sentinel
	}}

	const goroutineCount = 32
	var waitGroup sync.WaitGroup
	waitGroup.Add(goroutineCount)
	for index := 0; index < goroutineCount; index++ {
		go func(index int) {
			defer waitGroup.Done()
			if index%2 == 0 {
				NewOpenAIClient("http://example.com", "", "model", WithHTTPClient(original))
			} else {
				NewAnthropicClient("http://example.com", "key", "model", WithHTTPClient(original))
			}
		}(index)
	}
	waitGroup.Wait()

	require.ErrorIs(t, original.CheckRedirect(nil, nil), sentinel)
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
