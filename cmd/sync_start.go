package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/graph"
)

// errStillAlreadyRunning is start's error once its workflow was found
// already running more than config.MaxAlreadyRunningWaits times.
var errStillAlreadyRunning = errors.New("workflow still already running")

// start triggers this sync's own run of r's workflow. A run already going
// (another sync, or the run a retried CronJob pod started) may have other
// inputs, such as another season range, so it is waited for with wait and
// this sync's run is started after it; while waiting, r is not active in
// state, so an interrupt does not cancel a run this sync did not start.
func (r workflowRunner) start(ctx context.Context, state *syncState, wait func(context.Context) error) error {
	for waits := 0; ; waits++ {
		started, err := r.trigger()
		if !isWorkflowAlreadyRunning(err) {
			return r.started(started, err)
		}
		state.clearActive(r.workflowType)
		if waits == config.MaxAlreadyRunningWaits {
			return fmt.Errorf("%s: %w after waiting for %d runs: %w",
				r.workflowType, errStillAlreadyRunning, waits, err)
		}
		log.Warn().Str("workflow", r.workflowType.String()).
			Msg("Workflow already running, waiting for that run before starting this sync's (an interrupt leaves it running)")
		if err := wait(ctx); err != nil {
			return err
		}
		state.setActive(r.workflowType)
	}
}

// started is start's outcome for a trigger not refused as already running.
func (r workflowRunner) started(started bool, err error) error {
	if err != nil {
		return err
	}
	if !started {
		log.Warn().Str("workflow", r.workflowType.String()).Msg("Workflow was not started")
	}
	return nil
}

// waitForOtherRun returns start's wait for r: it monitors the running
// workflow on its own display on out until it ends. That run's failure is
// not this sync's, so it is logged and the wait succeeds; only an interrupt
// fails it.
func (r workflowRunner) waitForOtherRun(out io.Writer) func(context.Context) error {
	return func(ctx context.Context) error {
		return r.waitForRun(ctx, out, defaultMonitorConfig())
	}
}

func (r workflowRunner) waitForRun(ctx context.Context, out io.Writer, c monitorConfig) error {
	sp := newThemedSpinner(out)
	err := c.monitorWorkflow(ctx, sp, r.getStatus, r.workflowType)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		log.Warn().Err(err).Str("workflow", r.workflowType.String()).
			Msg("The run waited for did not succeed; starting this sync's run")
	}
	return nil
}

// isWorkflowAlreadyRunning reports whether err is a start mutation refused
// because its workflow is already running.
func isWorkflowAlreadyRunning(err error) bool {
	var gqlErr *graphQLError
	return errors.As(err, &gqlErr) && gqlErr.Code() == graph.ErrorCodeWorkflowAlreadyRunning
}
