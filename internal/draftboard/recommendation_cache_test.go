package draftboard

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draftrecommend"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const recommendationStartTimeout = time.Second

func TestRecommendationCacheEvictsLeastRecentlyUsed(t *testing.T) {
	const capacity = 2
	cache := newRecommendationCache(capacity)
	first, second, third := "first", "second", "third"
	cache.put(first, storedRun())
	cache.put(second, storedRun())
	_, cached := cache.get(first)
	require.True(t, cached)
	cache.put(third, storedRun())

	_, cached = cache.get(second)
	assert.False(t, cached, "second was least recently used")
	_, cached = cache.get(first)
	assert.True(t, cached)
	_, cached = cache.get(third)
	assert.True(t, cached)
	assert.Len(t, cache.order, capacity)

	cache.put(third, storedRun())
	assert.Len(t, cache.order, capacity, "re-putting an entry does not grow the cache")
}

func TestConcurrentRecommendationRequestsShareOneWrite(t *testing.T) {
	const callers = 8
	engine := newBlockingRecommendationEngine(callers)
	service := NewService(nil, engine, nil, Options{})
	input := recommendationInput(1)
	results := make(chan draftrecommend.StoredRun, callers)
	errors := make(chan error, callers)
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(callers)
	for range callers {
		go recommendAfter(start, &ready, service, input, results, errors)
	}
	ready.Wait()
	close(start)
	engine.waitForEntry(t)
	engine.release()

	var first uuid.UUID
	for range callers {
		run := <-results
		require.NoError(t, <-errors)
		if first == uuid.Nil {
			first = run.ID
		}
		assert.Equal(t, first, run.ID)
	}
	assert.Equal(t, 1, engine.callCount())
}

func TestRecommendationWritesForDifferentKeysOverlap(t *testing.T) {
	const concurrentWrites = 2
	engine := newBlockingRecommendationEngine(concurrentWrites)
	service := NewService(nil, engine, nil, Options{})
	results := make(chan draftrecommend.StoredRun, concurrentWrites)
	errors := make(chan error, concurrentWrites)
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(concurrentWrites)
	go recommendAfter(start, &ready, service, recommendationInput(1), results, errors)
	go recommendAfter(start, &ready, service, recommendationInput(2), results, errors)
	ready.Wait()
	close(start)

	engine.waitForEntry(t)
	engine.waitForEntry(t)
	engine.release()
	for range concurrentWrites {
		<-results
		require.NoError(t, <-errors)
	}
	assert.Equal(t, 2, engine.callCount(), "cache locking must not span recommendation database writes")
}

func recommendAfter(start <-chan struct{}, ready *sync.WaitGroup, service *Service,
	input draftrecommend.Input, results chan<- draftrecommend.StoredRun, errors chan<- error) {
	ready.Done()
	<-start
	run, err := service.recommend(context.Background(), input)
	results <- run
	errors <- err
}

func recommendationInput(version uint64) draftrecommend.Input {
	return draftrecommend.Input{
		SessionLeagueKey: "500.l.5621",
		Session:          draftsession.State{Version: version},
		Ranking:          boardRanking("500.l.5621"),
		OurTeamID:        3,
		SessionSafe:      true,
	}
}

func storedRun() draftrecommend.StoredRun {
	return draftrecommend.StoredRun{ID: uuid.New()}
}

type blockingRecommendationEngine struct {
	mu      sync.Mutex
	calls   int
	entered chan struct{}
	done    chan struct{}
	once    sync.Once
}

func newBlockingRecommendationEngine(callers int) *blockingRecommendationEngine {
	return &blockingRecommendationEngine{
		entered: make(chan struct{}, callers),
		done:    make(chan struct{}),
	}
}

func (e *blockingRecommendationEngine) Recommend(_ context.Context,
	input draftrecommend.Input) (draftrecommend.StoredRun, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	e.entered <- struct{}{}
	<-e.done
	return draftrecommend.StoredRun{ID: uuid.New(), Input: input}, nil
}

func (e *blockingRecommendationEngine) waitForEntry(t *testing.T) {
	t.Helper()
	select {
	case <-e.entered:
	case <-time.After(recommendationStartTimeout):
		e.release()
		t.Fatal("recommendation write did not start concurrently")
	}
}

func (e *blockingRecommendationEngine) release() {
	e.once.Do(func() { close(e.done) })
}

func (e *blockingRecommendationEngine) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}
