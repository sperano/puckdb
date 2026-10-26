package yahoo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const (
	// standingsTestLeagueXML is the test league's settings: its season runs
	// 2023-10-10 through 2024-04-18.
	standingsTestLeagueXML = `<fantasy_content><league><league_key>423.l.12345</league_key><league_id>12345</league_id>` +
		`<game_code>nhl</game_code><season>2023</season><start_date>2023-10-10</start_date><end_date>2024-04-18</end_date>` +
		`</league></fantasy_content>`
	standingsTestXML = `<fantasy_content><league><league_key>423.l.12345</league_key><league_id>12345</league_id>` +
		`<game_code>nhl</game_code><season>2023</season><standings><teams count="1"><team>` +
		`<team_key>423.l.12345.t.1</team_key><team_id>1</team_id><name>One</name>` +
		`<team_standings><rank>1</rank><outcome_totals><wins>1</wins><losses>0</losses><ties>0</ties>` +
		`<percentage>1.000</percentage></outcome_totals></team_standings></team></teams></standings></league></fantasy_content>`
	// standingsNoTeamsXML is a document Yahoo could serve with no standings.
	standingsNoTeamsXML = `<fantasy_content><league><league_key>423.l.12345</league_key><league_id>12345</league_id>` +
		`<game_code>nhl</game_code><season>2023</season><standings><teams count="0"></teams></standings></league></fantasy_content>`
)

// laterStandingsXML is a capture made moments after standingsTestXML's.
const laterStandingsXML = `<fantasy_content><league><league_key>423.l.12345</league_key><league_id>12345</league_id>` +
	`<game_code>nhl</game_code><season>2023</season><standings><teams count="1"><team>` +
	`<team_key>423.l.12345.t.1</team_key><team_id>1</team_id><name>One</name>` +
	`<team_standings><rank>1</rank><outcome_totals><wins>2</wins><losses>0</losses><ties>0</ties>` +
	`<percentage>1.000</percentage></outcome_totals></team_standings></team></teams></standings></league></fantasy_content>`

// standingsTestNow is 2023-12-01 20:30 Pacific (2023-12-02 04:30 UTC): the
// capture day is 2023-12-01, not the UTC day.
var standingsTestNow = time.Date(2023, time.December, 2, 4, 30, 0, 0, time.UTC)

func standingsTestDate() time.Time { return time.Date(2023, time.December, 1, 0, 0, 0, 0, time.UTC) }

func standingsRes() resource.LeagueStandings {
	return resource.LeagueStandings{Season: testYahooSeason, LeagueID: testLeagueID, Date: standingsTestDate(), GameKey: testGameKey}
}

func newStandingsActivities(t *testing.T, dl func(context.Context, string) ([]byte, error), gobCache *cache.GobCache) *FetchActivities {
	t.Helper()
	mem := store.NewMemStorage()
	writeLeagueXML(t, mem, testYahooSeason, testLeagueID, []byte(standingsTestLeagueXML))
	return newFetchActivities(mem, dl, gobCache)
}

func runStandings(t *testing.T, act *FetchActivities, now time.Time) (FetchYahooLeagueStandingsResult, error) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	capture := func(ctx context.Context, input FetchYahooLeagueDataInput) (FetchYahooLeagueStandingsResult, error) {
		return act.fetchYahooLeagueStandings(ctx, input, now)
	}
	env.RegisterActivity(capture)
	val, err := env.ExecuteActivity(capture, testLeagueDataInput)
	var result FetchYahooLeagueStandingsResult
	if err == nil {
		require.NoError(t, val.Get(&result))
	}
	return result, err
}

func TestFetchYahooLeagueStandings_CapturesUnderTheCaptureDay(t *testing.T) {
	redisClient, mockRedis := redismock.NewClientMock()
	// A capture is a locked miss: the lock is taken on the Redis miss and
	// again for the download, so a concurrent run reads this capture.
	expectCoherentFetchMiss(mockRedis, standingsRes())
	expectRedisPopulate(mockRedis, standingsRes())
	act := newStandingsActivities(t, mockDownloader([]byte(standingsTestXML), nil), cache.NewGobCache(redisClient))

	result, err := runStandings(t, act, standingsTestNow)

	require.NoError(t, err)
	assert.Equal(t, FetchYahooLeagueStandingsResult{Date: "2023-12-01"}, result)
	require.NoError(t, mockRedis.ExpectationsWereMet())
	data, err := act.Storage.Read(context.Background(), standingsRes().Path())
	require.NoError(t, err)
	assert.Equal(t, standingsTestXML, string(data), "the raw response is stored verbatim")
}

// A second run on the same day reads the day's capture; it never downloads
// again, so the first capture of the day is the day's snapshot.
func TestFetchYahooLeagueStandings_FirstCaptureOfTheDayWins(t *testing.T) {
	act := newStandingsActivities(t, mockDownloader(nil, errors.New("must not download")), cache.NewGobCache(nil))
	require.NoError(t, act.Storage.Write(context.Background(), standingsRes().Path(), []byte(standingsTestXML)))

	result, err := runStandings(t, act, standingsTestNow.Add(3*time.Hour))

	require.NoError(t, err)
	assert.Equal(t, FetchYahooLeagueStandingsResult{Date: "2023-12-01"}, result)
}

// Yesterday's missing snapshot is never filled in by today's run.
func TestFetchYahooLeagueStandings_NeverBackfills(t *testing.T) {
	act := newStandingsActivities(t, mockDownloader([]byte(standingsTestXML), nil), cache.NewGobCache(nil))

	_, err := runStandings(t, act, standingsTestNow.AddDate(0, 0, 1))

	require.NoError(t, err)
	assert.False(t, act.Storage.Exists(context.Background(), standingsRes().Path()), "no file for the previous day")
	next := standingsRes()
	next.Date = standingsTestDate().AddDate(0, 0, 1)
	assert.True(t, act.Storage.Exists(context.Background(), next.Path()))
}

func TestFetchYahooLeagueStandings_SkipsOutsideTheLeagueSeason(t *testing.T) {
	tests := []struct {
		name       string
		now        time.Time
		wantReason string
	}{
		{"before the league starts", time.Date(2023, time.October, 9, 20, 0, 0, 0, time.UTC), "league season starts 2023-10-10"},
		{"the day after the grace day", time.Date(2024, time.April, 20, 20, 0, 0, 0, time.UTC), "league season ended 2024-04-18"},
		{"another NHL season", time.Date(2024, time.October, 1, 20, 0, 0, 0, time.UTC), "capture day 2024-10-01 is not in season 2023"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			act := newStandingsActivities(t, mockDownloader(nil, errors.New("must not download")), cache.NewGobCache(nil))

			result, err := runStandings(t, act, tt.now)

			require.NoError(t, err)
			assert.Equal(t, FetchYahooLeagueStandingsResult{SkipReason: tt.wantReason}, result)
		})
	}
}

// The first day of the league and the grace day after its end are captured.
func TestFetchYahooLeagueStandings_CapturesSeasonBounds(t *testing.T) {
	tests := []struct {
		name     string
		now      time.Time
		wantDate string
	}{
		{"start date", time.Date(2023, time.October, 10, 20, 0, 0, 0, time.UTC), "2023-10-10"},
		{"grace day", time.Date(2024, time.April, 19, 20, 0, 0, 0, time.UTC), "2024-04-19"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			act := newStandingsActivities(t, mockDownloader([]byte(standingsTestXML), nil), cache.NewGobCache(nil))

			result, err := runStandings(t, act, tt.now)

			require.NoError(t, err)
			assert.Equal(t, FetchYahooLeagueStandingsResult{Date: tt.wantDate}, result)
		})
	}
}

// A league whose settings carry no readable start date is skipped with a
// reason that says so, not with an empty start date.
func TestFetchYahooLeagueStandings_UnreadableStartDateIsSkipped(t *testing.T) {
	mem := store.NewMemStorage()
	writeLeagueXML(t, mem, testYahooSeason, testLeagueID, []byte(testLeagueXML))
	act := newFetchActivities(mem, mockDownloader(nil, errors.New("must not download")), cache.NewGobCache(nil))

	result, err := runStandings(t, act, standingsTestNow)

	require.NoError(t, err)
	assert.Equal(t, FetchYahooLeagueStandingsResult{SkipReason: `league start date "" is not a date`}, result)
}

// Yahoo answers 400 for a league-level resource that does not exist yet:
// the day's capture is reported unavailable, not retried.
func TestFetchYahooLeagueStandings_RejectionIsUnavailable(t *testing.T) {
	act := newStandingsActivities(t, mockDownloader(nil, errNoMoreWeeks), cache.NewGobCache(nil))

	result, err := runStandings(t, act, standingsTestNow)

	require.NoError(t, err)
	assert.Equal(t, FetchYahooLeagueStandingsResult{Unavailable: true}, result)
	assert.False(t, act.Storage.Exists(context.Background(), standingsRes().Path()))
}

func TestFetchYahooLeagueStandings_DownloadErrorIsAnError(t *testing.T) {
	act := newStandingsActivities(t, mockDownloader(nil, errors.New("yahoo is down")), cache.NewGobCache(nil))

	_, err := runStandings(t, act, standingsTestNow)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch standings 2023/12345 for 2023-12-01")
	assert.Contains(t, err.Error(), "yahoo is down")
}

// A response without standings is an error and is not cached, so the next
// attempt downloads again instead of keeping an empty snapshot.
func TestFetchYahooLeagueStandings_EmptyStandingsAreNotCached(t *testing.T) {
	act := newStandingsActivities(t, mockDownloader([]byte(standingsNoTeamsXML), nil), cache.NewGobCache(nil))

	_, err := runStandings(t, act, standingsTestNow)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "contains no standings")
	assert.False(t, act.Storage.Exists(context.Background(), standingsRes().Path()))
}

func TestFetchYahooLeagueStandings_MissingLeagueFileIsAnError(t *testing.T) {
	act := newFetchActivities(store.NewMemStorage(), mockDownloader([]byte(standingsTestXML), nil), cache.NewGobCache(nil))

	_, err := runStandings(t, act, standingsTestNow)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "read league file 2023/12345")
}

// Two runs capturing the same day at once make one download: the second
// misses while the first's download is in flight, waits for it under the
// resource lock, and reads the first capture instead of overwriting it.
func TestFetchYahooLeagueStandings_ConcurrentSameDayCapturesKeepTheFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	server := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	lockAttempts := installLockAttemptHook(redisClient)
	gobCache := cache.NewGobCache(redisClient)
	memory := store.NewMemStorage()
	res := standingsRes()
	firstEntered := make(chan struct{})
	firstResume := make(chan struct{})
	firstDone := runFetch(ctx, Fetcher{
		Storage: memory, GobCache: gobCache,
		Download: blockingDownloader(standingsTestXML, firstEntered, firstResume),
	}, res)
	<-firstEntered
	waitForLockAttempts(t, lockAttempts, 2)

	secondDownloaded := make(chan struct{})
	secondDone := runFetch(ctx, Fetcher{
		Storage: memory, GobCache: gobCache,
		Download: signalingDownloader(laterStandingsXML, secondDownloaded),
	}, res)
	waitForLockAttempts(t, lockAttempts, 1)
	assertNotCompleted(t, secondDone)
	assertNotSignaled(t, secondDownloaded)

	close(firstResume)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	assertNotSignaled(t, secondDownloaded)
	stored, err := memory.Read(ctx, res.Path())
	require.NoError(t, err)
	assert.Equal(t, standingsTestXML, string(stored), "the first capture is the day's file")
}

func runFetch(ctx context.Context, fetcher Fetcher, res Resource) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, _, err := fetcher.Fetch(ctx, res)
		done <- err
	}()
	return done
}
