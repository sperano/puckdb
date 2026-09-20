package simulation

import (
	"context"

	"go.temporal.io/sdk/client"
)

// WorkflowSignaler is the narrow Temporal-client surface the simulation
// activities need to send signals back to their parent workflow. The
// only signal V1 sends is "pause" — emitted by DraftPickActivity (and
// later ManageRosterActivity) when the cost cap is reached.
//
// Wrapping client.Client in a single-method interface keeps activity
// unit tests independent of a real Temporal dev server: tests inject a
// stub WorkflowSignaler that just records the call.
//
// runID is intentionally part of the signature even though all sim
// callsites pass "" (meaning "latest run"). Keeping the parameter
// matches client.Client.SignalWorkflow byte-for-byte so the
// temporalSignaler adapter is a one-liner forward.
type WorkflowSignaler interface {
	SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, arg any) error
}

// TemporalSignaler is the production WorkflowSignaler backed by the
// real Temporal client.Client. Constructed at worker startup in
// cmd/worker.go alongside the activity worker; the same Temporal
// client that registers activities also serves as their signal
// channel back to the workflow.
type TemporalSignaler struct {
	Client client.Client
}

// NewTemporalSignaler wraps a Temporal client in the WorkflowSignaler
// interface.
func NewTemporalSignaler(c client.Client) *TemporalSignaler {
	return &TemporalSignaler{Client: c}
}

// SignalWorkflow forwards directly to client.Client.SignalWorkflow.
// The wrapper exists for the interface, not for any signal-side logic.
func (s *TemporalSignaler) SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, arg any) error {
	return s.Client.SignalWorkflow(ctx, workflowID, runID, signalName, arg)
}
