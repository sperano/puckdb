package player

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redismock/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
)

// The Temporal test environment neither enforces heartbeat timeouts nor
// reports every heartbeat to its listener (it throttles them), so these
// tests run the batch on a simulated clock: the fakes advance it by the
// latency of the call they stand in for, and a worker interceptor stamps
// every RecordHeartbeat call with the simulated time. The batch stays alive
// when no span without a heartbeat reaches the configured heartbeat timeout.
const (
	// simulatedLandingLatency is one NHL landing request, slow but well
	// within a single heartbeat window.
	simulatedLandingLatency = 25 * time.Second
	// simulatedUpsertLatency is one player upsert round trip.
	simulatedUpsertLatency = 10 * time.Second

	heartbeatDownloadedID1 = int64(8478402)
	heartbeatCachedID      = int64(8476453)
	heartbeatMissingID     = int64(8499999)
	heartbeatDownloadedID2 = int64(8479318)
)

// heartbeatStamp is one RecordHeartbeat call at a simulated time.
type heartbeatStamp struct {
	details []any
	at      time.Duration
}

// simulatedBatchClock is the simulated time of one batch run plus the
// heartbeats recorded during it. The interceptor and the fakes may run on
// different goroutines, hence the mutex.
type simulatedBatchClock struct {
	mu         sync.Mutex
	now        time.Duration
	heartbeats []heartbeatStamp
}

func (c *simulatedBatchClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += d
}

func (c *simulatedBatchClock) recordHeartbeat(details []any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.heartbeats = append(c.heartbeats, heartbeatStamp{details: details, at: c.now})
}

func (c *simulatedBatchClock) snapshot() (time.Duration, []heartbeatStamp) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now, append([]heartbeatStamp(nil), c.heartbeats...)
}

// advanceOn returns a testify Run hook that advances the clock by d.
func (c *simulatedBatchClock) advanceOn(d time.Duration) func(mock.Arguments) {
	return func(mock.Arguments) { c.advance(d) }
}

// heartbeatInterceptor reports every activity heartbeat to a simulated clock.
type heartbeatInterceptor struct {
	interceptor.WorkerInterceptorBase
	clock *simulatedBatchClock
}

func (h *heartbeatInterceptor) InterceptActivity(
	_ context.Context,
	next interceptor.ActivityInboundInterceptor,
) interceptor.ActivityInboundInterceptor {
	in := &heartbeatInbound{clock: h.clock}
	in.Next = next
	return in
}

type heartbeatInbound struct {
	interceptor.ActivityInboundInterceptorBase
	clock *simulatedBatchClock
}

func (in *heartbeatInbound) Init(outbound interceptor.ActivityOutboundInterceptor) error {
	out := &heartbeatOutbound{clock: in.clock}
	out.Next = outbound
	return in.Next.Init(out)
}

type heartbeatOutbound struct {
	interceptor.ActivityOutboundInterceptorBase
	clock *simulatedBatchClock
}

func (out *heartbeatOutbound) RecordHeartbeat(ctx context.Context, details ...any) {
	out.clock.recordHeartbeat(details)
	out.Next.RecordHeartbeat(ctx, details...)
}

// seedCachedLanding stores a landing page so the batch reads it instead of
// downloading it.
func seedCachedLanding(t *testing.T, mem store.Storage, landing *nhl.PlayerLanding) {
	t.Helper()
	data, err := json.Marshal(landing)
	require.NoError(t, err)
	require.NoError(t, mem.Write(context.Background(), resource.PlayerLanding{PlayerID: landing.PlayerID}.Path(), data))
}

func heartbeatTestLanding(id int64, first, last string) *nhl.PlayerLanding {
	return &nhl.PlayerLanding{
		PlayerID:  nhl.PlayerID(id),
		FirstName: nhl.LocalizedString{Default: first},
		LastName:  nhl.LocalizedString{Default: last},
		Position:  "C",
		IsActive:  true,
	}
}

func TestProcessPlayerBatch_HeartbeatsKeepMultiPlayerBatchAlive(t *testing.T) {
	t.Parallel()

	heartbeatTimeout := shared.DefaultActivityOptions().HeartbeatTimeout
	clock := &simulatedBatchClock{}

	mem := store.NewMemStorage()
	seedCachedLanding(t, mem, heartbeatTestLanding(heartbeatCachedID, "Connor", "McDavid"))

	client := &MockNHLClient{}
	client.On("PlayerLanding", mock.Anything, nhl.PlayerID(heartbeatDownloadedID1)).
		Run(clock.advanceOn(simulatedLandingLatency)).
		Return(heartbeatTestLanding(heartbeatDownloadedID1, "Leon", "Draisaitl"), nil)
	client.On("PlayerLanding", mock.Anything, nhl.PlayerID(heartbeatMissingID)).
		Run(clock.advanceOn(simulatedLandingLatency)).
		Return(nil, nhl.ErrNotFound)
	client.On("PlayerLanding", mock.Anything, nhl.PlayerID(heartbeatDownloadedID2)).
		Run(clock.advanceOn(simulatedLandingLatency)).
		Return(heartbeatTestLanding(heartbeatDownloadedID2, "Mitch", "Marner"), nil)

	upserter := NewMockPlayerUpserter()
	upserter.On("UpsertPlayer", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertPlayerParams")).
		Run(clock.advanceOn(simulatedUpsertLatency)).
		Return(nil)

	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(map[string]string{})

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.SetWorkerOptions(worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{&heartbeatInterceptor{clock: clock}},
	})

	players := []store.BoxscorePlayer{
		{ID: heartbeatDownloadedID1},
		{ID: heartbeatCachedID},
		{ID: heartbeatMissingID, FirstName: "Ghost", LastName: "Player", Position: "C"},
		{ID: heartbeatDownloadedID2},
	}
	// Every player but the missing one reads its landing through the GobCache.
	landingReads := len(players) - 1
	a := newTestProcessActivities(mem, client, redisClient, newTestGobCacheForReads(landingReads), upserter, nil)
	result, err := executeProcessPlayerBatch(env, context.Background(), a, players)
	require.NoError(t, err)

	end, heartbeats := clock.snapshot()
	require.Greater(t, end, heartbeatTimeout,
		"the simulated batch must outlast the heartbeat timeout for this test to mean anything")
	assertHeartbeatPerPlayer(t, players, heartbeats)
	assertNoHeartbeatGap(t, heartbeats, end, heartbeatTimeout)

	// Download behavior is unchanged by the heartbeats.
	assert.Equal(t, 2, result.Downloaded)
	assert.Equal(t, 1, result.Missing)
	assert.Equal(t, 4, result.Imported)
	assert.Equal(t, 2, result.Origins[core.OriginRemoteNHLAPI])
	assert.Equal(t, 1, result.Origins[core.OriginFileSystem])
	assert.Empty(t, result.Errors)
	for _, id := range []int64{heartbeatDownloadedID1, heartbeatDownloadedID2} {
		assert.True(t, mem.Exists(context.Background(), resource.PlayerLanding{PlayerID: nhl.PlayerID(id)}.Path()))
	}
	assert.True(t, mem.Exists(context.Background(), resource.MissingPlayerLanding{PlayerID: nhl.PlayerID(heartbeatMissingID)}.Path()))
	client.AssertExpectations(t)
	upserter.AssertExpectations(t)
	assert.NoError(t, mockRedis.ExpectationsWereMet())
}

// assertHeartbeatPerPlayer checks that the batch heartbeats once per player,
// in batch order, with the player's NHL ID as details.
func assertHeartbeatPerPlayer(t *testing.T, players []store.BoxscorePlayer, heartbeats []heartbeatStamp) {
	t.Helper()
	want := make([][]any, len(players))
	for i, p := range players {
		want[i] = []any{p.ID}
	}
	got := make([][]any, len(heartbeats))
	for i, hb := range heartbeats {
		got[i] = hb.details
	}
	assert.Equal(t, want, got)
}

// assertNoHeartbeatGap checks that every span of the batch without a
// heartbeat (start to first, between two, last to end) is shorter than
// timeout.
func assertNoHeartbeatGap(t *testing.T, heartbeats []heartbeatStamp, end, timeout time.Duration) {
	t.Helper()
	require.NotEmpty(t, heartbeats)
	var last time.Duration
	for i, hb := range heartbeats {
		assert.Less(t, hb.at-last, timeout, "span before heartbeat %d reaches the heartbeat timeout", i)
		last = hb.at
	}
	assert.Less(t, end-last, timeout, "span after the last heartbeat reaches the heartbeat timeout")
}
