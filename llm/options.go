package llm

import (
	"net/http"
	"time"
)

// Option configures a client at construction time.
//
// The functional-options pattern lets callers tune knobs that are
// orthogonal to provider/url/key/model without growing the constructor
// signature. Today the only knob is request timeout; future additions
// (custom http.Client, retry policy, request hooks) plug in here without
// breaking existing call sites.
type Option func(*clientOptions)

// clientOptions is the internal struct that an Option mutates.
type clientOptions struct {
	timeout    time.Duration
	httpClient *http.Client
}

// defaultHTTPTimeout is the per-request HTTP timeout when no
// WithTimeout option is supplied. Matches the previous package-wide
// constant — large enough for slow models (Anthropic Claude on long
// tool-use rounds), small enough that hung connections don't pin a
// worker forever.
const defaultHTTPTimeout = 120 * time.Second

// WithTimeout sets the per-request HTTP timeout. Pass a value <= 0 to
// disable the timeout (uses http.Client's zero value, which means no
// timeout — only do this for tests or background workers that own their
// own cancellation strategy).
func WithTimeout(d time.Duration) Option {
	return func(o *clientOptions) {
		o.timeout = d
	}
}

// WithHTTPClient lets the caller supply a fully-configured *http.Client.
// Useful for tests (httptest server with custom transport) and for
// production setups that need shared connection pooling. When set, this
// supersedes WithTimeout — the caller's http.Client governs the timeout.
func WithHTTPClient(c *http.Client) Option {
	return func(o *clientOptions) {
		o.httpClient = c
	}
}

// applyOptions resolves the option list into a usable *http.Client.
func applyOptions(opts []Option) *http.Client {
	co := &clientOptions{timeout: defaultHTTPTimeout}
	for _, opt := range opts {
		opt(co)
	}
	if co.httpClient != nil {
		return co.httpClient
	}
	return &http.Client{Timeout: co.timeout}
}
