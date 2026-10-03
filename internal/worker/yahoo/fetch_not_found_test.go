package yahoo

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

const missingTeamID = 11

// retryabilityCases maps a downloader failure to whether Temporal may retry it.
var retryabilityCases = map[string]struct {
	err       error
	retryable bool
}{
	"400 is not retryable":   {&httpx.HTTPError{StatusCode: http.StatusBadRequest, Status: "400 Bad Request"}, false},
	"404 is retryable":       {&httpx.HTTPError{StatusCode: http.StatusNotFound, Status: "404 Not Found"}, true},
	"429 is retryable":       {&httpx.HTTPError{StatusCode: http.StatusTooManyRequests, Status: "429"}, true},
	"500 is retryable":       {&httpx.HTTPError{StatusCode: http.StatusInternalServerError, Status: "500"}, true},
	"transport is retryable": {errors.New("connection reset"), true},
}

// requireApplicationError asserts err carries a *temporal.ApplicationError and returns it.
func requireApplicationError(t *testing.T, err error) *temporal.ApplicationError {
	t.Helper()
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	return appErr
}

func assertRetryability(t *testing.T, err error, retryable bool, errType string) {
	t.Helper()
	require.Error(t, err)
	var appErr *temporal.ApplicationError
	if retryable {
		assert.False(t, errors.As(err, &appErr) && appErr.NonRetryable(), "error must stay retryable: %v", err)
		return
	}
	appErr = requireApplicationError(t, err)
	assert.True(t, appErr.NonRetryable())
	assert.Equal(t, errType, appErr.Type())
}

func failingDownloader(err error) shared.Downloader {
	return mockDownloader(nil, err)
}

func TestFetchTeams_Retryability(t *testing.T) {
	for name, tc := range retryabilityCases {
		t.Run(name, func(t *testing.T) {
			// Not parallel: seedGameKey mutates the package-level game key cache.
			seedGameKey()
			redisClient, mockRedis := redismock.NewClientMock()
			res := resource.Team{Season: testYahooSeason, LeagueID: testLeagueID, TeamID: missingTeamID, GameKey: testGameKey}
			mockRedis.ExpectGet(core.RedisKey(res)).SetErr(redis.Nil)

			act := newFetchActivities(store.NewMemStorage(), failingDownloader(tc.err), cache.NewGobCache(redisClient))
			env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
			env.RegisterActivity(act.FetchTeams)
			input := FetchTeamsInput{StartSeason: testYahooSeason, Teams: []TeamInfo{{LeagueID: testLeagueID, TeamID: missingTeamID}}}
			_, err := env.ExecuteActivity(act.FetchTeams, input)

			assertRetryability(t, err, tc.retryable, teamNotFoundErrorType)
			if !tc.retryable {
				assert.Contains(t, err.Error(), "team_ids")
				assert.Contains(t, err.Error(), fmt.Sprintf("team %d", missingTeamID))
			}
		})
	}
}

func TestFetchLeague_Retryability(t *testing.T) {
	for name, tc := range retryabilityCases {
		t.Run(name, func(t *testing.T) {
			seedGameKey()
			redisClient, mockRedis := redismock.NewClientMock()
			res := resource.League{Season: testYahooSeason, LeagueID: testLeagueID, GameKey: testGameKey}
			mockRedis.ExpectGet(core.RedisKey(res)).SetErr(redis.Nil)

			act := newFetchActivities(store.NewMemStorage(), failingDownloader(tc.err), cache.NewGobCache(redisClient))
			env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
			env.RegisterActivity(act.FetchLeague)
			_, err := env.ExecuteActivity(act.FetchLeague, testYahooSeason, testLeagueID)

			assertRetryability(t, err, tc.retryable, leagueNotFoundErrorType)
		})
	}
}
