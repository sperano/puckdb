package draftranking

import (
	"context"
	"sync"
	"time"
)

const (
	// heartbeatInterval is how often a running refresh re-sends its current
	// step. It must stay well under the workflow's heartbeat timeout
	// (draftRankingHeartbeatTimeout): one step can spend longer than that
	// timeout in a single database query, and a heartbeat sent only when a
	// step starts would let Temporal time the attempt out and retry it while
	// the first attempt's query is still running.
	heartbeatInterval = 30 * time.Second
	// initialStep is the heartbeat detail before the refresh names a step
	// (loading the league's rules and pool).
	initialStep = "start"
)

// stepPulse heartbeats a refresh's current step: at once when a step starts,
// then every interval until stopped. A long step thus keeps the attempt alive
// and still receives a workflow cancellation, which Temporal delivers on a
// heartbeat.
type stepPulse struct {
	record func(step string)
	mu     sync.Mutex
	step   string
}

// startPulse starts heartbeating through record every interval until ctx is
// done or stop is called. step names the current step; stop waits for the
// pulse to end, so nothing is recorded once it returns.
func startPulse(ctx context.Context, interval time.Duration, record func(step string)) (step func(string), stop func()) {
	p := &stepPulse{record: record, step: initialStep}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.run(ctx, interval)
	}()
	return p.Step, func() {
		cancel()
		<-done
	}
}

// Step makes step the current step and records it at once.
func (p *stepPulse) Step(step string) {
	p.mu.Lock()
	p.step = step
	p.mu.Unlock()
	p.record(step)
}

func (p *stepPulse) current() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.step
}

func (p *stepPulse) run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.record(p.current())
		}
	}
}
