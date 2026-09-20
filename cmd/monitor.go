package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/graph/model"
)

// monitorConfig groups the polling knobs shared by the single- and
// multi-workflow monitors. Production paths use defaultMonitorConfig; tests
// shrink the delays to keep polling loops fast.
type monitorConfig struct {
	startupDelay time.Duration
	pollInterval time.Duration
	maxBackoff   time.Duration
	maxFailures  int
}

func defaultMonitorConfig() monitorConfig {
	return monitorConfig{
		startupDelay: config.DefaultWorkflowStartupDelay,
		pollInterval: config.DefaultWorkflowPollInterval,
		maxBackoff:   config.MaxWorkflowPollBackoff,
		maxFailures:  config.MaxConsecutiveQueryFailures,
	}
}

// pollDelay returns the poll interval, backing off exponentially on consecutive failures.
func (c monitorConfig) pollDelay(consecutiveFailures int) time.Duration {
	if consecutiveFailures == 0 {
		return c.pollInterval
	}
	delay := c.pollInterval << consecutiveFailures
	if delay > c.maxBackoff {
		return c.maxBackoff
	}
	return delay
}

// runnerPollState tracks one monitored workflow's polling bookkeeping.
// Failure counters live here — per runner — so one endpoint failing on every
// poll reaches the limit even while queries to another endpoint succeed.
type runnerPollState struct {
	consecutiveFailures int
	done                bool
	status              *WorkflowStatus
}

// recordFailure increments this runner's counter and reports whether the
// consecutive-failure limit has been reached.
func (s *runnerPollState) recordFailure(limit int) bool {
	s.consecutiveFailures++
	return s.consecutiveFailures >= limit
}

// recordSuccess resets this runner's failure counter (only this runner's) and
// caches the latest status.
func (s *runnerPollState) recordSuccess(status *WorkflowStatus) {
	s.consecutiveFailures = 0
	s.status = status
}

// classifyWorkflowStatus is the single source of truth for which Temporal
// workflow statuses are terminal. It returns terminal=false while the
// workflow should keep being polled; for terminal statuses, err is nil only
// for COMPLETED.
func classifyWorkflowStatus(status *WorkflowStatus) (terminal bool, err error) {
	switch status.Result.Status {
	case model.TemporalWorkflowStatusCompleted:
		return true, nil
	case model.TemporalWorkflowStatusFailed:
		if status.Result.FailureReason != nil {
			return true, fmt.Errorf("workflow failed: %s", *status.Result.FailureReason)
		}
		return true, fmt.Errorf("workflow failed")
	case model.TemporalWorkflowStatusCanceled:
		return true, fmt.Errorf("workflow was canceled")
	case model.TemporalWorkflowStatusTerminated:
		return true, fmt.Errorf("workflow was terminated")
	case model.TemporalWorkflowStatusTimedOut:
		return true, fmt.Errorf("workflow timed out")
	case model.TemporalWorkflowStatusRunning, model.TemporalWorkflowStatusContinuedAsNew:
		return false, nil
	case model.TemporalWorkflowStatusUnspecified:
		// Workflow might not have started yet or doesn't exist.
		log.Debug().Msg("Workflow status unspecified")
		return false, nil
	default:
		log.Debug().Str("status", string(status.Result.Status)).Msg("Unknown workflow status, continuing to poll")
		return false, nil
	}
}

// monitorWorkflow polls a single workflow's status until it reaches a
// terminal state.
func monitorWorkflow(ctx context.Context, sp *spinner, getStatus statusFetcher, wt workflowType) error {
	return defaultMonitorConfig().monitorWorkflow(ctx, sp, getStatus, wt)
}

func (c monitorConfig) monitorWorkflow(ctx context.Context, sp *spinner, getStatus statusFetcher, wt workflowType) error {
	// Wait briefly for workflow to start and register query handlers
	time.Sleep(c.startupDelay)

	var state runnerPollState

	for {
		status, err := getStatus(ctx)
		if err != nil {
			if state.recordFailure(c.maxFailures) {
				sp.Cancel()
				return fmt.Errorf("workflow query failed %d times consecutively: %w", state.consecutiveFailures, err)
			}
			sp.PrintAbove(func() {
				log.Warn().Err(err).Int("attempt", state.consecutiveFailures).Msg("Failed to get status, retrying...")
			})
		} else {
			state.recordSuccess(status)
			msg := formatStatusMessage(status, wt)
			if w := yahooWarning(status); w != "" {
				msg += "\n" + w
			}
			sp.SetMessage(msg)

			if terminal, terminalErr := classifyWorkflowStatus(status); terminal {
				if terminalErr == nil {
					sp.Stop()
					return nil
				}
				sp.Cancel()
				return terminalErr
			}
		}

		select {
		case <-ctx.Done():
			sp.Cancel()
			return ctx.Err()
		case <-time.After(c.pollDelay(state.consecutiveFailures)):
		}
	}
}

// monitorWorkflows polls multiple status endpoints and combines their display.
// Takes []workflowRunner (rather than []statusFetcher) so each slot's
// workflowType is available for the per-line "Workflow X is starting..."
// fallback rendered when progress isn't yet queryable, and for
// workflow-specific error context.
func monitorWorkflows(ctx context.Context, sp *spinner, runners []workflowRunner) error {
	return defaultMonitorConfig().monitorWorkflows(ctx, sp, runners)
}

func (c monitorConfig) monitorWorkflows(ctx context.Context, sp *spinner, runners []workflowRunner) error {
	time.Sleep(c.startupDelay)

	states := make([]runnerPollState, len(runners))

	for {
		allDone := true
		var messages []string

		for i, r := range runners {
			state := &states[i]
			if state.done {
				// Already completed, use cached final message
				if state.status != nil {
					messages = append(messages, formatStatusMessage(state.status, r.workflowType))
				}
				continue
			}

			msg, err := c.pollRunner(ctx, r, state)
			if err != nil {
				sp.Cancel()
				return err
			}
			if msg != "" {
				messages = append(messages, msg)
			}
			if !state.done {
				allDone = false
			}
		}

		sp.SetMessage(combineStatusMessages(messages, states))

		if allDone {
			sp.Stop()
			return nil
		}

		select {
		case <-ctx.Done():
			sp.Cancel()
			return ctx.Err()
		case <-time.After(c.pollDelay(maxActiveFailures(states))):
		}
	}
}

// pollRunner queries one still-running workflow and updates its state. It
// returns the runner's display message — empty when the query failed — and a
// terminal error when the workflow ended unsuccessfully or reached the
// consecutive-query-failure limit. A COMPLETED workflow marks state.done and
// still contributes its final message.
func (c monitorConfig) pollRunner(ctx context.Context, r workflowRunner, state *runnerPollState) (string, error) {
	status, err := r.getStatus(ctx)
	if err != nil {
		if state.recordFailure(c.maxFailures) {
			return "", fmt.Errorf("workflow %s query failed %d times consecutively: %w",
				r.workflowType, state.consecutiveFailures, err)
		}
		return "", nil
	}

	state.recordSuccess(status)

	terminal, terminalErr := classifyWorkflowStatus(status)
	switch {
	case terminal && terminalErr == nil:
		state.done = true
	case terminal:
		return "", fmt.Errorf("workflow %s: %w", r.workflowType, terminalErr)
	}
	return formatStatusMessage(status, r.workflowType), nil
}

// combineStatusMessages joins per-workflow messages, appending the Yahoo
// token warning once at the end. Only statuses of still-running workflows are
// checked — completed workflows use cached statuses that may have stale token
// info.
func combineStatusMessages(messages []string, states []runnerPollState) string {
	var b strings.Builder
	b.WriteString(strings.Join(messages, "\n"))
	for _, state := range states {
		if state.done || state.status == nil {
			continue
		}
		if w := yahooWarning(state.status); w != "" {
			b.WriteString("\n")
			b.WriteString(w)
			break
		}
	}
	return b.String()
}

// maxActiveFailures returns the highest consecutive-failure count among
// still-running workflows, so the shared poll loop backs off while any
// endpoint is failing.
func maxActiveFailures(states []runnerPollState) int {
	maxFailures := 0
	for _, state := range states {
		if state.done {
			continue
		}
		if state.consecutiveFailures > maxFailures {
			maxFailures = state.consecutiveFailures
		}
	}
	return maxFailures
}
