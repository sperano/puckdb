package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/matching"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

var (
	eliasForwardBirth = time.Date(1998, time.November, 12, 0, 0, 0, 0, time.UTC)
	eliasDefenceBirth = time.Date(2004, time.February, 16, 0, 0, 0, 0, time.UTC)
)

const (
	eliasForwardID store.YahooPlayerID = 7520
	eliasDefenceID store.YahooPlayerID = 8800
)

// yahooPlayerPageHTML is the part of a Yahoo Sports player page
// store.ParseYahooPlayerHTML reads.
func yahooPlayerPageHTML(birth time.Time) []byte {
	return fmt.Appendf(nil, `<html><head><title>Elias Pettersson (C) Stats, News - Vancouver - Yahoo Sports</title></head>`+
		`<body><span>">#40<</span><li>Born: <span>%s (Sundsvall, Sweden)</span></li></body></html>`,
		birth.Format("January 2, 2006"))
}

// unparseablePage has no player title, so ParseYahooPlayerHTML refuses it.
var unparseablePage = []byte("<html><head><title>Service unavailable</title></head></html>")

// eliasLanding is the 1998 Elias Pettersson's landing: jersey 40, which
// neither pool entry carries, so the jersey step leaves both tied.
func eliasLanding() *nhl.PlayerLanding {
	sweater := 40
	team := "VAN"
	return &nhl.PlayerLanding{
		PlayerID:          nhl.PlayerID(8480012),
		FirstName:         nhl.LocalizedString{Default: "Elias"},
		LastName:          nhl.LocalizedString{Default: "Pettersson"},
		SweaterNumber:     &sweater,
		CurrentTeamAbbrev: &team,
		BirthDate:         "1998-11-12",
	}
}

// eliasPool holds both Vancouver Elias Petterssons as game lists give them:
// no birth date, no jersey, the same team.
func eliasPool() map[store.YahooPlayerID]*store.YahooPlayer {
	return map[store.YahooPlayerID]*store.YahooPlayer{
		eliasForwardID: {YahooID: eliasForwardID, FirstName: "Elias", LastName: "Pettersson", Team: "Vancouver"},
		eliasDefenceID: {YahooID: eliasDefenceID, FirstName: "Elias", LastName: "Pettersson", Team: "Vancouver"},
	}
}

// pageServer is a YahooPlayerPages downloader serving pages by URL and
// recording each requested URL; a URL it has no page for fails.
type pageServer struct {
	pages     map[store.YahooPlayerID][]byte
	err       error
	requested []string
}

func (s *pageServer) download(_ context.Context, url string) ([]byte, error) {
	s.requested = append(s.requested, url)
	if s.err != nil {
		return nil, s.err
	}
	for id, page := range s.pages {
		if url == (resource.YahooPlayer{PlayerID: id}).URL() {
			return page, nil
		}
	}
	return nil, fmt.Errorf("no page for %s", url)
}

func pageURL(id store.YahooPlayerID) string { return resource.YahooPlayer{PlayerID: id}.URL() }

func pagePath(id store.YahooPlayerID) string { return resource.YahooPlayer{PlayerID: id}.Path() }

// faultyStorage fails every Read with readErr and every Write with writeErr
// when set, and otherwise is its embedded storage.
type faultyStorage struct {
	store.Storage
	readErr, writeErr error
}

func (s faultyStorage) Read(ctx context.Context, path string) ([]byte, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	return s.Storage.Read(ctx, path)
}

func (s faultyStorage) Write(ctx context.Context, path string, data []byte) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	return s.Storage.Write(ctx, path, data)
}

// matchElias adds the birth dates eliasLanding needs to pool and matches it.
func matchElias(t *testing.T, a *Activities, pool map[store.YahooPlayerID]*store.YahooPlayer) (matching.YahooIDMatchResult, ProcessPlayerBatchResult) {
	t.Helper()
	var out batchOutcome
	landing := eliasLanding()
	require.NoError(t, a.addYahooBirthDates(context.Background(), landing, landingBirthDate(landing), pool, &out))
	return matchLandingYahooID(landing, pool, nil), out.result
}

func TestAddYahooBirthDates_DownloadsAndStoresTiedCandidatesPages(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	server := &pageServer{pages: map[store.YahooPlayerID][]byte{
		eliasForwardID: yahooPlayerPageHTML(eliasForwardBirth),
		eliasDefenceID: yahooPlayerPageHTML(eliasDefenceBirth),
	}}
	redisClient, valkey := redismock.NewClientMock()
	for _, id := range []store.YahooPlayerID{eliasForwardID, eliasDefenceID} {
		key := core.RedisKey(resource.YahooPlayer{PlayerID: id})
		valkey.ExpectGet(key).SetErr(redis.Nil)
		valkey.CustomMatch(anyArgs).ExpectSet(key, "page", cache.GobCacheTTL).SetVal("OK")
	}
	a := &Activities{Storage: mem, GobCache: cache.NewGobCache(redisClient), YahooPlayerPages: server.download}
	pool := eliasPool()

	match, result := matchElias(t, a, pool)

	require.NoError(t, valkey.ExpectationsWereMet(), "each downloaded page is cached in Valkey")
	assert.Equal(t, matching.YahooIDMatchResult{YahooID: eliasForwardID, Matched: true, Reason: matching.MatchReasonNameBirthdate}, match)
	assert.Equal(t, []string{pageURL(eliasForwardID), pageURL(eliasDefenceID)}, server.requested)
	assert.Equal(t, 2, result.BirthDatePagesDownloaded)
	assert.Zero(t, result.BirthDatePagesFailed)
	assert.Equal(t, eliasForwardBirth, pool[eliasForwardID].BirthDate)
	assert.Equal(t, eliasDefenceBirth, pool[eliasDefenceID].BirthDate)
	stored, err := mem.Read(context.Background(), pagePath(eliasDefenceID))
	require.NoError(t, err)
	assert.Equal(t, yahooPlayerPageHTML(eliasDefenceBirth), stored)
}

func TestAddYahooBirthDates_ReadsStoredPagesWithoutDownloading(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	require.NoError(t, mem.Write(context.Background(), pagePath(eliasForwardID), yahooPlayerPageHTML(eliasForwardBirth)))
	require.NoError(t, mem.Write(context.Background(), pagePath(eliasDefenceID), yahooPlayerPageHTML(eliasDefenceBirth)))
	server := &pageServer{err: errors.New("must not download")}
	a := &Activities{Storage: mem, YahooPlayerPages: server.download}

	match, result := matchElias(t, a, eliasPool())

	assert.Equal(t, eliasForwardID, match.YahooID)
	assert.Equal(t, matching.MatchReasonNameBirthdate, match.Reason)
	assert.Empty(t, server.requested)
	assert.Equal(t, ProcessPlayerBatchResult{}, result)
}

func TestAddYahooBirthDates_ReplacesStoredPageThatNoLongerParses(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	require.NoError(t, mem.Write(context.Background(), pagePath(eliasForwardID), unparseablePage))
	require.NoError(t, mem.Write(context.Background(), pagePath(eliasDefenceID), yahooPlayerPageHTML(eliasDefenceBirth)))
	server := &pageServer{pages: map[store.YahooPlayerID][]byte{eliasForwardID: yahooPlayerPageHTML(eliasForwardBirth)}}
	a := &Activities{Storage: mem, YahooPlayerPages: server.download}

	match, result := matchElias(t, a, eliasPool())

	assert.Equal(t, eliasForwardID, match.YahooID)
	assert.Equal(t, []string{pageURL(eliasForwardID)}, server.requested)
	assert.Equal(t, 1, result.BirthDatePagesDownloaded)
	stored, err := mem.Read(context.Background(), pagePath(eliasForwardID))
	require.NoError(t, err)
	assert.Equal(t, yahooPlayerPageHTML(eliasForwardBirth), stored)
}

func TestAddYahooBirthDates_SkipsWhenJerseyDecides(t *testing.T) {
	t.Parallel()
	server := &pageServer{err: errors.New("must not download")}
	a := &Activities{Storage: store.NewMemStorage(), YahooPlayerPages: server.download}
	pool := eliasPool()
	pool[eliasForwardID].JerseyNumber = 40
	pool[eliasDefenceID].JerseyNumber = 25

	match, result := matchElias(t, a, pool)

	assert.Equal(t, eliasForwardID, match.YahooID)
	assert.Equal(t, matching.MatchReasonNameJersey, match.Reason)
	assert.Empty(t, server.requested)
	assert.Equal(t, ProcessPlayerBatchResult{}, result)
}

// A tied candidate whose page cannot be had keeps no birth date, so the
// match falls through to the team step, which cannot tell the two
// Vancouver players apart.
func TestAddYahooBirthDates_FailuresLeaveTheMatchWithoutBirthDates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		storage       func() store.Storage
		downloader    func() *pageServer
		wantRequested int
		// wantDownloaded counts the bodies the downloader returned, even
		// those that then failed to parse or be stored.
		wantDownloaded int
	}{
		{name: "download error",
			storage:       func() store.Storage { return store.NewMemStorage() },
			downloader:    func() *pageServer { return &pageServer{err: errors.New("503 Service Unavailable")} },
			wantRequested: 2},
		{name: "downloaded page does not parse",
			storage: func() store.Storage { return store.NewMemStorage() },
			downloader: func() *pageServer {
				return &pageServer{pages: map[store.YahooPlayerID][]byte{eliasForwardID: unparseablePage, eliasDefenceID: unparseablePage}}
			},
			wantRequested: 2, wantDownloaded: 2},
		{name: "storage read error",
			storage: func() store.Storage {
				return faultyStorage{Storage: store.NewMemStorage(), readErr: errors.New("juicefs down")}
			},
			downloader: func() *pageServer { return &pageServer{err: errors.New("must not download")} }},
		{name: "storage write error",
			storage: func() store.Storage {
				return faultyStorage{Storage: store.NewMemStorage(), writeErr: errors.New("juicefs read-only")}
			},
			downloader: func() *pageServer {
				return &pageServer{pages: map[store.YahooPlayerID][]byte{
					eliasForwardID: yahooPlayerPageHTML(eliasForwardBirth),
					eliasDefenceID: yahooPlayerPageHTML(eliasDefenceBirth),
				}}
			},
			wantRequested: 2, wantDownloaded: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := tc.downloader()
			a := &Activities{Storage: tc.storage(), YahooPlayerPages: server.download}
			pool := eliasPool()

			match, result := matchElias(t, a, pool)

			assert.Equal(t, matching.YahooIDMatchResult{Reason: matching.MatchReasonAmbiguous}, match)
			assert.Len(t, server.requested, tc.wantRequested)
			assert.Equal(t, ProcessPlayerBatchResult{BirthDatePagesDownloaded: tc.wantDownloaded, BirthDatePagesFailed: 2}, result)
			assert.True(t, pool[eliasForwardID].BirthDate.IsZero())
			assert.True(t, pool[eliasDefenceID].BirthDate.IsZero())
		})
	}
}

// A page that failed is not requested again for a later landing of the same
// batch tied on the same Yahoo ID, and counts as failed once.
func TestAddYahooBirthDates_DoesNotRetryAFailedPageInTheSameBatch(t *testing.T) {
	t.Parallel()
	server := &pageServer{pages: map[store.YahooPlayerID][]byte{eliasDefenceID: yahooPlayerPageHTML(eliasDefenceBirth)}}
	a := &Activities{Storage: store.NewMemStorage(), YahooPlayerPages: server.download}
	pool := eliasPool()
	var out batchOutcome

	for range 2 {
		landing := eliasLanding()
		require.NoError(t, a.addYahooBirthDates(context.Background(), landing, landingBirthDate(landing), pool, &out))
	}

	assert.Equal(t, []string{pageURL(eliasForwardID), pageURL(eliasDefenceID)}, server.requested)
	assert.Equal(t, ProcessPlayerBatchResult{BirthDatePagesDownloaded: 1, BirthDatePagesFailed: 1}, out.result)
	assert.True(t, pool[eliasForwardID].BirthDate.IsZero())
	assert.Equal(t, eliasDefenceBirth, pool[eliasDefenceID].BirthDate)
}

// Storage is the source of truth: a Valkey SET failure after a download is
// logged, and the page still counts as downloaded and gives its birth date.
func TestAddYahooBirthDates_ValkeyWriteFailureKeepsTheDownloadedPage(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	server := &pageServer{pages: map[store.YahooPlayerID][]byte{
		eliasForwardID: yahooPlayerPageHTML(eliasForwardBirth),
		eliasDefenceID: yahooPlayerPageHTML(eliasDefenceBirth),
	}}
	redisClient, valkey := redismock.NewClientMock()
	forwardKey := core.RedisKey(resource.YahooPlayer{PlayerID: eliasForwardID})
	defenceKey := core.RedisKey(resource.YahooPlayer{PlayerID: eliasDefenceID})
	valkey.ExpectGet(forwardKey).SetErr(redis.Nil)
	valkey.CustomMatch(anyArgs).ExpectSet(forwardKey, "page", cache.GobCacheTTL).SetErr(errors.New("valkey down"))
	valkey.ExpectGet(defenceKey).SetErr(redis.Nil)
	valkey.CustomMatch(anyArgs).ExpectSet(defenceKey, "page", cache.GobCacheTTL).SetVal("OK")
	a := &Activities{Storage: mem, GobCache: cache.NewGobCache(redisClient), YahooPlayerPages: server.download}
	pool := eliasPool()

	match, result := matchElias(t, a, pool)

	require.NoError(t, valkey.ExpectationsWereMet())
	assert.Equal(t, matching.YahooIDMatchResult{YahooID: eliasForwardID, Matched: true, Reason: matching.MatchReasonNameBirthdate}, match)
	assert.Equal(t, ProcessPlayerBatchResult{BirthDatePagesDownloaded: 2}, result)
	assert.Equal(t, eliasForwardBirth, pool[eliasForwardID].BirthDate)
	stored, err := mem.Read(context.Background(), pagePath(eliasForwardID))
	require.NoError(t, err)
	assert.Equal(t, yahooPlayerPageHTML(eliasForwardBirth), stored)
}

// cancellingServer cancels the activity context while it serves the first
// request, the way Temporal cancels an activity mid-download.
type cancellingServer struct {
	cancel    context.CancelFunc
	requested []string
}

func (s *cancellingServer) download(ctx context.Context, url string) ([]byte, error) {
	s.requested = append(s.requested, url)
	s.cancel()
	return nil, ctx.Err()
}

// A cancelled activity stops the batch with the cancellation instead of
// counting a failed page and matching and upserting without the dates.
func TestProcessPlayerBatch_CancellationDuringBirthDateFetchStopsTheBatch(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	landing := eliasLanding()
	landingJSON, err := json.Marshal(landing)
	require.NoError(t, err)
	require.NoError(t, mem.Write(context.Background(), resource.PlayerLanding{PlayerID: landing.PlayerID}.Path(), landingJSON))

	redisClient, mockRedis := redismock.NewClientMock()
	entries := map[string]string{}
	for id, p := range eliasPool() {
		entries[id.String()] = string(encodeYahooPlayer(t, p))
	}
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(entries)

	upserter := NewMockPlayerUpserter()
	server := &cancellingServer{}
	a := newTestProcessActivities(mem, &MockNHLClient{}, redisClient, nil, upserter, nil)
	a.YahooPlayerPages = server.download
	var result ProcessPlayerBatchResult
	var batchErr error
	// The activity derives the cancellable context itself: the test
	// environment does not pass on a cancellation made while it runs.
	run := func(ctx context.Context) error {
		ctx, server.cancel = context.WithCancel(ctx)
		defer server.cancel()
		result, batchErr = a.ProcessPlayerBatch(ctx, []store.BoxscorePlayer{{ID: landing.PlayerID.Int64()}})
		return batchErr
	}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(run)
	_, _ = env.ExecuteActivity(run)

	require.ErrorIs(t, batchErr, context.Canceled)
	assert.Len(t, server.requested, 1, "no page is requested after the cancellation")
	assert.Zero(t, result.BirthDatePagesFailed)
	assert.Zero(t, result.Imported)
	upserter.AssertNotCalled(t, "UpsertPlayer", mock.Anything, mock.Anything)
}

func TestYahooBirthDate_NoDownloaderFailsForAMissingPage(t *testing.T) {
	t.Parallel()
	a := &Activities{Storage: store.NewMemStorage()}

	_, downloaded, err := a.yahooBirthDate(context.Background(), eliasForwardID)

	require.ErrorIs(t, err, errNoYahooPlayerPages)
	assert.False(t, downloaded)
}

// The batch fetches the birth dates before matching, so the 1998 Elias
// Pettersson gets his Yahoo ID by birth date and the counts reach the result.
func TestProcessPlayerBatch_MatchesTiedCandidatesByFetchedBirthDate(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	landing := eliasLanding()
	landingJSON, err := json.Marshal(landing)
	require.NoError(t, err)
	require.NoError(t, mem.Write(context.Background(), resource.PlayerLanding{PlayerID: landing.PlayerID}.Path(), landingJSON))
	require.NoError(t, mem.Write(context.Background(), pagePath(eliasDefenceID), yahooPlayerPageHTML(eliasDefenceBirth)))

	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	entries := map[string]string{}
	for id, p := range eliasPool() {
		entries[id.String()] = string(encodeYahooPlayer(t, p))
	}
	mockRedis.ExpectHGetAll(YahooIDPoolKey).SetVal(entries)
	mockRedis.ExpectSRem(YahooIDAvailableKey, eliasForwardID).SetVal(1)

	upserter := NewMockPlayerUpserter()
	upserter.On("ClearConflictingYahooID", mock.Anything, mock.AnythingOfType("sqlcdb.ClearConflictingYahooIDParams")).Return(nil)
	var upserted sqlcdb.UpsertPlayerParams
	upserter.On("UpsertPlayer", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertPlayerParams")).
		Run(func(args mock.Arguments) { upserted = args.Get(1).(sqlcdb.UpsertPlayerParams) }).
		Return(nil)
	server := &pageServer{pages: map[store.YahooPlayerID][]byte{eliasForwardID: yahooPlayerPageHTML(eliasForwardBirth)}}
	a := newTestProcessActivities(mem, &MockNHLClient{}, redisClient, nil, upserter, nil)
	a.YahooPlayerPages = server.download

	result, err := runProcessPlayerBatch(t, context.Background(), a, []store.BoxscorePlayer{{ID: landing.PlayerID.Int64()}})

	require.NoError(t, err)
	assert.Equal(t, map[string]int{matching.MatchReasonNameBirthdate.String(): 1}, result.MatchReasons)
	assert.Equal(t, 1, result.Matched)
	assert.Equal(t, 1, result.BirthDatePagesDownloaded, "only the page not yet stored is downloaded")
	assert.Zero(t, result.BirthDatePagesFailed)
	assert.Equal(t, []string{pageURL(eliasForwardID)}, server.requested)
	assert.Equal(t, int64(eliasForwardID), upserted.YahooID.Int64)
}
