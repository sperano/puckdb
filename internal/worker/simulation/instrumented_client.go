package simulation

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/metrics"
)

// instrumentedLLMClient wraps an llm.Client and observes
// puckdb_sim_llm_call_duration_seconds + puckdb_sim_llm_failures_total
// around every Complete call.
//
// One wrapper per (pool, agent) — constructed inside NewAgent so
// every cached *Agent already carries instrumentation, and every
// Complete call (whether from the draft phase, the daily agentloop,
// or any future call site) reports through the same labels.
//
// Failures detected by the wrapper:
//   - context.DeadlineExceeded → "timeout"
//   - any other non-nil error from inner.Complete → "api_error"
//
// "tool_use_failure" / "parse_error" / "validation" classifications
// happen at the activity layer (DraftPickActivity, ManageRosterActivity)
// because they're properties of the response, not the transport.
type instrumentedLLMClient struct {
	inner     llm.Client
	provider  string
	model     string
	agentName string
}

// newInstrumentedLLMClient wraps an llm.Client with metrics
// observation. The labels are baked in at construction so the
// Complete hot path doesn't pay for label lookup on every call.
func newInstrumentedLLMClient(inner llm.Client, provider, model, agentName string) llm.Client {
	return &instrumentedLLMClient{
		inner:     inner,
		provider:  provider,
		model:     model,
		agentName: agentName,
	}
}

// Complete delegates to the wrapped client and reports the call's
// duration + classification. The metrics observation runs even on
// error so dashboards see the full latency distribution including
// timeouts.
func (c *instrumentedLLMClient) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	start := time.Now()
	resp, err := c.inner.Complete(ctx, req)
	metrics.ObserveSimLLMCallDuration(c.provider, c.model, c.agentName, time.Since(start))
	if err != nil {
		metrics.IncSimLLMFailure(c.provider, c.model, classifyTransportError(err))
	}
	return resp, err
}

// classifyTransportError maps a Complete-call error to one of the
// SimFailure* labels in the metrics package. The string-match path
// is a fallback for SDKs that don't wrap with errors.Is-friendly
// types — most provider clients return wrapped errors that contain
// the underlying cause's message.
func classifyTransportError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return metrics.SimFailureTimeout
	}
	if errors.Is(err, context.Canceled) {
		// Cancel is a user-initiated abort, not a provider failure;
		// classify as timeout (close enough for dashboards) rather
		// than introduce a fifth bucket.
		return metrics.SimFailureTimeout
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline") {
		return metrics.SimFailureTimeout
	}
	return metrics.SimFailureAPIError
}
