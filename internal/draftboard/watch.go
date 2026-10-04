package draftboard

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sperano/puckdb/internal/draftwatch"
)

// WatchRunner is the process-local transport to Yahoo's shared draft watcher.
type WatchRunner interface {
	Watch(context.Context, draftwatch.Identity, draftwatch.WatchOptions) error
}

// IdentityResolver gives the controller season-safe session keys.
type IdentityResolver interface {
	ResolveIdentity(context.Context, string, int) (draftwatch.Identity, error)
}

// WatchStatus describes process-scoped polling ownership. Durable board state
// remains in PostgreSQL and survives when this controller process exits.
type WatchStatus struct {
	LeagueKey string
	Running   bool
	StartedAt time.Time
	StoppedAt *time.Time
	LastError string
}

type watchEntry struct {
	status WatchStatus
	cancel context.CancelFunc
	done   chan struct{}
}

// WatchController owns at most one active watch per full league key.
type WatchController struct {
	resolver IdentityResolver
	factory  func() WatchRunner
	options  draftwatch.WatchOptions
	mu       sync.Mutex
	entries  map[string]*watchEntry
}

func NewWatchController(resolver IdentityResolver, factory func() WatchRunner, options draftwatch.WatchOptions) *WatchController {
	return &WatchController{resolver: resolver, factory: factory, options: options,
		entries: make(map[string]*watchEntry)}
}

// Start starts a process-scoped watcher. Repeated calls while active return
// its existing status without launching another poller.
func (c *WatchController) Start(ctx context.Context, league string, season int) (WatchStatus, error) {
	identity, err := c.resolver.ResolveIdentity(ctx, league, season)
	if err != nil {
		return WatchStatus{}, err
	}
	if c.factory == nil {
		return WatchStatus{}, errors.New("draft watch runner is unavailable")
	}
	c.mu.Lock()
	if current := c.entries[identity.LeagueKey]; current != nil && current.status.Running {
		status := current.status
		c.mu.Unlock()
		return status, nil
	}
	watchCtx, cancel := context.WithCancel(context.Background())
	entry := &watchEntry{status: WatchStatus{LeagueKey: identity.LeagueKey, Running: true, StartedAt: time.Now().UTC()},
		cancel: cancel, done: make(chan struct{})}
	c.entries[identity.LeagueKey] = entry
	status := entry.status
	c.mu.Unlock()
	go c.run(watchCtx, identity, entry)
	return status, nil
}

func (c *WatchController) run(ctx context.Context, identity draftwatch.Identity, entry *watchEntry) {
	err := c.factory().Watch(ctx, identity, c.options)
	c.mu.Lock()
	entry.status.Running = false
	stopped := time.Now().UTC()
	entry.status.StoppedAt = &stopped
	if err != nil && !errors.Is(err, context.Canceled) {
		entry.status.LastError = err.Error()
	}
	close(entry.done)
	c.mu.Unlock()
}

// Stop cancels and waits for a watch. If ctx has no deadline, it applies the
// controller's configured final timeout as the upper bound.
func (c *WatchController) Stop(ctx context.Context, leagueKey string) (WatchStatus, error) {
	c.mu.Lock()
	entry := c.entries[leagueKey]
	if entry == nil {
		c.mu.Unlock()
		return WatchStatus{LeagueKey: leagueKey}, nil
	}
	entry.cancel()
	done := entry.done
	c.mu.Unlock()
	waitCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		timeout := c.options.FinalTimeout
		if timeout <= 0 {
			timeout = defaultWatchStopTimeout
		}
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	select {
	case <-done:
		status, _ := c.Status(leagueKey)
		return status, nil
	case <-waitCtx.Done():
		return c.status(leagueKey), fmt.Errorf("wait for draft watcher stop: %w", waitCtx.Err())
	}
}

// Status returns the last process-local watch state for one full league key.
func (c *WatchController) Status(leagueKey string) (WatchStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[leagueKey]
	if entry == nil {
		return WatchStatus{}, false
	}
	return entry.status, true
}

func (c *WatchController) status(leagueKey string) WatchStatus {
	status, _ := c.Status(leagueKey)
	return status
}

// Close cancels every active watcher and waits for each final reconciliation.
func (c *WatchController) Close(ctx context.Context) error {
	c.mu.Lock()
	keys := make([]string, 0, len(c.entries))
	for key, entry := range c.entries {
		if entry.status.Running {
			keys = append(keys, key)
		}
	}
	c.mu.Unlock()
	var result error
	for _, key := range keys {
		if _, err := c.Stop(ctx, key); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

const defaultWatchStopTimeout = 5 * time.Second
