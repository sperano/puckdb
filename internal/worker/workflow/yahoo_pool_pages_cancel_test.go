package workflow

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const (
	// shrunkPoolPlayers is a pool of two full pages and a short third one,
	// smaller than the poolTestPlayers snapshot before it.
	shrunkPoolPlayers = 2*poolPageSize + poolTestLastPage
	// shrunkPoolLastPage is the short page that ends the shrunk pool.
	shrunkPoolLastPage = 2
	// pagesPastShrunkEnd are the pages in flight past the shrunk pool's end
	// when its short page returns: the first concurrent batch runs pages 1
	// to yahooPoolPageConcurrency.
	pagesPastShrunkEnd = yahooPoolPageConcurrency - shrunkPoolLastPage
)

// pageGate holds the pages of held until open is called, or poolTestBarrierWait
// has passed, so they are still in flight when another page ends the pool.
type pageGate struct {
	held    map[int]bool
	release chan struct{}
	once    sync.Once
}

func newPageGate(held map[int]bool) *pageGate {
	return &pageGate{held: held, release: make(chan struct{})}
}

func (g *pageGate) wait(start int) {
	if !g.held[start] {
		return
	}
	select {
	case <-g.release:
	case <-time.After(poolTestBarrierWait):
	}
}

func (g *pageGate) open() { g.once.Do(func() { close(g.release) }) }

// heldPastShrunkEnd holds page 1, before the shrunk pool's short page, and
// every page of the first concurrent batch after it.
func heldPastShrunkEnd() map[int]bool {
	held := map[int]bool{poolPageSize: true}
	for page := shrunkPoolLastPage + 1; page <= yahooPoolPageConcurrency; page++ {
		held[page*poolPageSize] = true
	}
	return held
}

// cancelCounter counts the activities a workflow cancels and opens gate once
// it has counted expected of them.
type cancelCounter struct {
	mu       sync.Mutex
	canceled int
}

func (c *cancelCounter) listen(env *testsuite.TestWorkflowEnvironment, gate *pageGate, expected int) {
	env.SetOnActivityCanceledListener(func(*activity.Info) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.canceled++
		if c.canceled == expected {
			gate.open()
		}
	})
}

func (c *cancelCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.canceled
}

// Once a short page ends a pool that shrank, the pages in flight past it are
// cancelled, so a download still waiting on the Yahoo rate limiter gives its
// token back; a page before it still finishes and is committed.
func TestFetchYahooPoolPages_CancelsPagesInFlightPastTheEnd(t *testing.T) {
	pool := newFakePool(shrunkPoolPlayers, 0)
	pool.gate = newPageGate(heldPastShrunkEnd())
	defer pool.gate.open()
	var counter cancelCounter
	run := runPoolFetch(t, pool, poolTestPlayers, func(env *testsuite.TestWorkflowEnvironment) {
		counter.listen(env, pool.gate, pagesPastShrunkEnd)
	})

	require.NoError(t, run.env.GetWorkflowError())
	assert.Equal(t, pagesPastShrunkEnd, counter.count(), "every page in flight past the end, and only those")
	require.NotNil(t, run.commit)
	assert.Equal(t, pageStarts(shrunkPoolLastPage+1), run.commit.Starts)
	assert.Equal(t, shrunkPoolPlayers, run.commit.Players)
	assert.Empty(t, run.commit.Discarded, "a cancelled page wrote nothing to delete")
	for _, start := range pool.stats().requested {
		assert.LessOrEqual(t, start, yahooPoolPageConcurrency*poolPageSize, "no page starts after the first batch")
	}
}

// A run that recorded letting the pages past the end finish keeps replaying
// it: they are discarded, not cancelled.
func TestFetchYahooPoolPages_LegacyVersionLetsPagesPastTheEndFinish(t *testing.T) {
	pool := newFakePool(shrunkPoolPlayers, 0)
	var counter cancelCounter
	run := runPoolFetch(t, pool, poolTestPlayers, func(env *testsuite.TestWorkflowEnvironment) {
		env.OnGetVersion(yahooPoolCancelPastEndChange, workflow.DefaultVersion, yahooPoolCancelPastEndVersion).
			Return(workflow.DefaultVersion)
		counter.listen(env, newPageGate(nil), 0)
	})

	require.NoError(t, run.env.GetWorkflowError())
	assert.Zero(t, counter.count())
	require.NotNil(t, run.commit)
	assert.Equal(t, pageStarts(shrunkPoolLastPage+1), run.commit.Starts)
	require.NotEmpty(t, run.commit.Discarded, "the pages past the end finished and are deleted")
	for _, start := range run.commit.Discarded {
		assert.Greater(t, start, shrunkPoolLastPage*poolPageSize)
	}
}
