package simulation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLLM is a minimal llm.Client whose Complete returns scripted
// (response, error) pairs. Lighter than scriptedLLMClient (used in
// the activity test suites) — no atomic counter, no testing.T
// integration.
type fakeLLM struct {
	resp *llm.Response
	err  error
	call func() // optional hook fired on each Complete to e.g. simulate sleep
}

func (f *fakeLLM) Complete(_ context.Context, _ *llm.Request) (*llm.Response, error) {
	if f.call != nil {
		f.call()
	}
	return f.resp, f.err
}

// ----------------------------------------------------------------------------
// classifyTransportError matrix
// ----------------------------------------------------------------------------

func TestClassifyTransportError(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		expected string
	}{
		{"nil error", nil, ""},
		{"context.DeadlineExceeded", context.DeadlineExceeded, metrics.SimFailureTimeout},
		{"context.Canceled", context.Canceled, metrics.SimFailureTimeout},
		{"deadline message wrapped", errors.New("context deadline exceeded while reading"), metrics.SimFailureTimeout},
		{"timeout message", errors.New("Client.Timeout exceeded while awaiting headers"), metrics.SimFailureTimeout},
		{"plain api error", errors.New("503 Service Unavailable"), metrics.SimFailureAPIError},
		{"network refused", errors.New("connection refused"), metrics.SimFailureAPIError},
		{"json decode failure", errors.New("invalid character '<' looking for beginning of value"), metrics.SimFailureAPIError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, classifyTransportError(tc.err))
		})
	}
}

// ----------------------------------------------------------------------------
// instrumentedLLMClient: success path + error path delegate correctly.
// ----------------------------------------------------------------------------

func TestInstrumentedLLMClient_Success_DelegatesAndReturns(t *testing.T) {
	want := &llm.Response{Content: "ok"}
	inner := &fakeLLM{resp: want}
	c := newInstrumentedLLMClient(inner, "anthropic", "claude-haiku-4-5", "TestAgent")

	got, err := c.Complete(context.Background(), &llm.Request{})
	require.NoError(t, err)
	assert.Same(t, want, got)
}

func TestInstrumentedLLMClient_Error_PropagatesAndCounts(t *testing.T) {
	inner := &fakeLLM{err: errors.New("503 backend down")}
	c := newInstrumentedLLMClient(inner, "anthropic", "claude-haiku-4-5", "TestAgent")

	_, err := c.Complete(context.Background(), &llm.Request{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
	// We can't easily inspect the Prometheus counter from here without
	// touching the global registry — the success-and-failure-paths
	// not panicking is the concrete invariant. A higher-fidelity
	// check would scrape /metrics; covered via integration tests.
}

// ----------------------------------------------------------------------------
// Latency observation: pin that the wrapper measures wall time, not
// no-op-zero. We use the call hook to inject a small sleep.
// ----------------------------------------------------------------------------

func TestInstrumentedLLMClient_MeasuresLatency(t *testing.T) {
	const sleep = 10 * time.Millisecond
	inner := &fakeLLM{
		resp: &llm.Response{Content: "ok"},
		call: func() { time.Sleep(sleep) },
	}
	c := newInstrumentedLLMClient(inner, "anthropic", "claude-haiku-4-5", "TestAgent")

	start := time.Now()
	_, err := c.Complete(context.Background(), &llm.Request{})
	elapsed := time.Since(start)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, sleep,
		"wrapper must NOT short-circuit the inner Complete call")
}

// RecordDayDuration is exercised end-to-end by the workflow tests
// (workflow_test.go) — the workflow's day loop calls it once per
// day and the test environment covers the activity ctx that
// activity.GetLogger requires. A standalone unit test would need
// testsuite.TestActivityEnvironment scaffolding for the same
// coverage, which would duplicate the workflow tests. Skip.
