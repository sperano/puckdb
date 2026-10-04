package yahoo

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	oldDraftResultXML = `<fantasy_content><league><draft_results count="1"><draft_result><round>1</round><pick>1</pick><team_key>423.l.12345.t.1</team_key><player_key>423.p.100</player_key></draft_result></draft_results></league></fantasy_content>`
	newDraftResultXML = `<fantasy_content><league><draft_results count="1"><draft_result><round>1</round><pick>1</pick><team_key>423.l.12345.t.1</team_key><player_key>423.p.200</player_key></draft_result></draft_results></league></fantasy_content>`
	testLockPrefix    = "puckdb:resource-cache-lock:"
	testSyncTimeout   = 2 * time.Second
	testAttemptBuffer = 100
)

func TestRefreshCoherentSerializesCacheMissRepopulation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	server := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	lockAttempts := installLockAttemptHook(redisClient)
	gobCache := cache.NewGobCache(redisClient)
	res := resource.DraftResults{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
	memory := store.NewMemStorage()
	require.NoError(t, memory.Write(ctx, res.Path(), []byte(oldDraftResultXML)))
	storage := &blockingReadStorage{
		Storage: memory,
		entered: make(chan struct{}),
		resume:  make(chan struct{}),
	}

	readerDone := make(chan error, 1)
	go func() {
		_, _, err := gobCache.ReadParsedCached(ctx, storage, res)
		readerDone <- err
	}()
	<-storage.entered
	waitForLockAttempts(t, lockAttempts, 1)

	refreshDownloaded := make(chan struct{})
	refreshDone := make(chan error, 1)
	go func() {
		fetcher := Fetcher{
			Storage: storage, GobCache: gobCache,
			Download: signalingDownloader(newDraftResultXML, refreshDownloaded), Throttle: func() {},
		}
		_, err := fetcher.RefreshCoherent(ctx, res)
		refreshDone <- err
	}()
	waitForLockAttempts(t, lockAttempts, 1)
	assertNotCompleted(t, refreshDone)
	assertNotSignaled(t, refreshDownloaded)

	close(storage.resume)
	require.NoError(t, <-readerDone)
	<-refreshDownloaded
	require.NoError(t, <-refreshDone)

	cached, ok, err := gobCache.Get[*store.FantasyContent](ctx, core.RedisKey(res))
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, cached.League.DraftResults.Slice, 1)
	assert.Equal(t, "423.p.200", cached.League.DraftResults.Slice[0].PlayerKey)
}

func TestRefreshCoherentFollowsInFlightNormalDownload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	server := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	lockAttempts := installLockAttemptHook(redisClient)
	gobCache := cache.NewGobCache(redisClient)
	res := resource.DraftResults{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
	memory := store.NewMemStorage()
	oldDownloadEntered := make(chan struct{})
	oldDownloadResume := make(chan struct{})
	oldFetcher := Fetcher{
		Storage: memory, GobCache: gobCache, Throttle: func() {},
		Download: blockingDownloader(oldDraftResultXML, oldDownloadEntered, oldDownloadResume),
	}

	oldDone := make(chan error, 1)
	go func() {
		_, _, err := oldFetcher.Fetch(ctx, res)
		oldDone <- err
	}()
	<-oldDownloadEntered
	waitForLockAttempts(t, lockAttempts, 2)

	refreshDownloaded := make(chan struct{})
	refreshDone := make(chan error, 1)
	go func() {
		fetcher := Fetcher{
			Storage: memory, GobCache: gobCache, Throttle: func() {},
			Download: signalingDownloader(newDraftResultXML, refreshDownloaded),
		}
		_, err := fetcher.RefreshCoherent(ctx, res)
		refreshDone <- err
	}()
	waitForLockAttempts(t, lockAttempts, 1)
	assertNotCompleted(t, refreshDone)
	assertNotSignaled(t, refreshDownloaded)

	close(oldDownloadResume)
	require.NoError(t, <-oldDone)
	<-refreshDownloaded
	require.NoError(t, <-refreshDone)
	assertCachedPlayer(t, ctx, gobCache, res, "423.p.200")
	stored, err := memory.Read(ctx, res.Path())
	require.NoError(t, err)
	assert.Equal(t, newDraftResultXML, string(stored))
}

func TestCoherentRefreshSerializesUpstreamRequests(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	server := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	lockAttempts := installLockAttemptHook(redisClient)
	gobCache := cache.NewGobCache(redisClient)
	res := resource.DraftResults{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
	memory := store.NewMemStorage()
	oldDownloadEntered := make(chan struct{})
	oldDownloadResume := make(chan struct{})
	oldDone := runCoherentRefresh(ctx, Fetcher{
		Storage: memory, GobCache: gobCache, Throttle: func() {},
		Download: blockingDownloader(oldDraftResultXML, oldDownloadEntered, oldDownloadResume),
	}, res)

	<-oldDownloadEntered
	waitForLockAttempts(t, lockAttempts, 1)
	newDownloadEntered := make(chan struct{})
	newDone := runCoherentRefresh(ctx, Fetcher{
		Storage: memory, GobCache: gobCache, Throttle: func() {},
		Download: signalingDownloader(newDraftResultXML, newDownloadEntered),
	}, res)
	waitForLockAttempts(t, lockAttempts, 1)
	assertNotSignaled(t, newDownloadEntered)
	assertNotCompleted(t, newDone)

	close(oldDownloadResume)
	require.NoError(t, <-oldDone)
	<-newDownloadEntered
	require.NoError(t, <-newDone)
	assertCachedPlayer(t, ctx, gobCache, res, "423.p.200")
}

func runCoherentRefresh(ctx context.Context, fetcher Fetcher, res resource.DraftResults) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := fetcher.RefreshCoherent(ctx, res)
		done <- err
	}()
	return done
}

type lockAttemptHook struct {
	attempts chan struct{}
}

func installLockAttemptHook(client *redis.Client) <-chan struct{} {
	hook := &lockAttemptHook{attempts: make(chan struct{}, testAttemptBuffer)}
	client.AddHook(hook)
	return hook.attempts
}

func (h *lockAttemptHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (h *lockAttemptHook) AfterProcess(_ context.Context, cmd redis.Cmder) error {
	args := cmd.Args()
	if cmd.Name() == "set" && len(args) > 1 {
		key, ok := args[1].(string)
		if ok && strings.HasPrefix(key, testLockPrefix) {
			h.attempts <- struct{}{}
		}
	}
	return nil
}

func (h *lockAttemptHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (h *lockAttemptHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func waitForLockAttempts(t *testing.T, attempts <-chan struct{}, count int) {
	t.Helper()
	for range count {
		select {
		case <-attempts:
		case <-time.After(testSyncTimeout):
			t.Fatal("timed out waiting for resource lock attempt")
		}
	}
}

func blockingDownloader(content string, entered, resume chan struct{}) func(context.Context, string) ([]byte, error) {
	return func(ctx context.Context, _ string) ([]byte, error) {
		close(entered)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-resume:
			return []byte(content), nil
		}
	}
}

func signalingDownloader(content string, called chan struct{}) func(context.Context, string) ([]byte, error) {
	return func(context.Context, string) ([]byte, error) {
		close(called)
		return []byte(content), nil
	}
}

func assertNotCompleted(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		require.Failf(t, "operation completed", "completed before lock release: %v", err)
	default:
	}
}

func assertNotSignaled(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
		require.Fail(t, "operation reached a protected stage before lock release")
	default:
	}
}

func assertCachedPlayer(
	t *testing.T,
	ctx context.Context,
	gobCache *cache.GobCache,
	res resource.DraftResults,
	wantPlayerKey string,
) {
	t.Helper()
	cached, ok, err := gobCache.Get[*store.FantasyContent](ctx, core.RedisKey(res))
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, cached.League.DraftResults.Slice, 1)
	assert.Equal(t, wantPlayerKey, cached.League.DraftResults.Slice[0].PlayerKey)
}

type blockingReadStorage struct {
	store.Storage
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (s *blockingReadStorage) Read(ctx context.Context, path string) ([]byte, error) {
	data, err := s.Storage.Read(ctx, path)
	if err != nil {
		return nil, err
	}
	s.once.Do(func() { close(s.entered) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.resume:
		return data, nil
	}
}
