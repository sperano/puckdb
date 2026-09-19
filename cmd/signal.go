package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// SignalContext returns a copy of parent that is canceled on the first
// SIGINT or SIGTERM. Once the context is canceled (by signal or by parent),
// the signal handler is removed, so a second signal falls back to Go's
// default disposition and force-quits the process — unless another handler
// (e.g. Temporal's worker.InterruptCh in the worker command) is still
// registered. The returned CancelFunc is idempotent and also removes the
// handler.
func SignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	context.AfterFunc(ctx, stop)
	return ctx, stop
}
