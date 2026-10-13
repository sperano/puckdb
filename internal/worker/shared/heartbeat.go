package shared

import (
	"context"
	"sync"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/interceptor"
)

// lastHeartbeatKey is the activity context key of an activity's *lastHeartbeat.
type lastHeartbeatKey struct{}

// lastHeartbeat holds the details of an activity's latest heartbeat.
type lastHeartbeat struct {
	mu      sync.Mutex
	details []any
}

func (h *lastHeartbeat) set(details []any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.details = details
}

func (h *lastHeartbeat) get() []any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.details
}

// HeartbeatDetailsInterceptor remembers the details of each activity's latest
// heartbeat, so a heartbeat sent only to show liveness (RepeatHeartbeat) does
// not replace the activity's progress with empty details. Install it on every
// worker that runs Yahoo downloads.
type HeartbeatDetailsInterceptor struct {
	interceptor.WorkerInterceptorBase
}

// InterceptActivity implements interceptor.WorkerInterceptor.
func (*HeartbeatDetailsInterceptor) InterceptActivity(
	_ context.Context, next interceptor.ActivityInboundInterceptor,
) interceptor.ActivityInboundInterceptor {
	in := &heartbeatInbound{}
	in.Next = next
	return in
}

type heartbeatInbound struct {
	interceptor.ActivityInboundInterceptorBase
}

func (i *heartbeatInbound) Init(outbound interceptor.ActivityOutboundInterceptor) error {
	out := &heartbeatOutbound{}
	out.Next = outbound
	return i.Next.Init(out)
}

func (i *heartbeatInbound) ExecuteActivity(ctx context.Context, in *interceptor.ExecuteActivityInput) (any, error) {
	return i.Next.ExecuteActivity(context.WithValue(ctx, lastHeartbeatKey{}, &lastHeartbeat{}), in)
}

type heartbeatOutbound struct {
	interceptor.ActivityOutboundInterceptorBase
}

func (o *heartbeatOutbound) RecordHeartbeat(ctx context.Context, details ...any) {
	if h, ok := ctx.Value(lastHeartbeatKey{}).(*lastHeartbeat); ok {
		h.set(details)
	}
	o.Next.RecordHeartbeat(ctx, details...)
}

// RepeatHeartbeat records a heartbeat for the activity in ctx that repeats
// the details of its latest heartbeat, keeping the progress it reported.
// Without HeartbeatDetailsInterceptor on the worker it sends no details.
func RepeatHeartbeat(ctx context.Context) {
	var details []any
	if h, ok := ctx.Value(lastHeartbeatKey{}).(*lastHeartbeat); ok {
		details = h.get()
	}
	activity.RecordHeartbeat(ctx, details...)
}
