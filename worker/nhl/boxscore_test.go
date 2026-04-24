package nhl

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// testSeason creates a nhlapi.SeasonInfo for testing with the given start and end dates.
func testSeason(start, end time.Time) nhlapi.SeasonInfo {
	return nhlapi.SeasonInfo{
		ID:             nhlapi.NewSeason(start.Year()),
		StandingsStart: nhlapi.DateFromTime(start),
		StandingsEnd:   nhlapi.DateFromTime(end),
	}
}

func TestExtractTeamPlayers_Empty(t *testing.T) {
	t.Parallel()

	stats := &nhlapi.TeamPlayerStats{
		Forwards: []nhlapi.SkaterStats{},
		Defense:  []nhlapi.SkaterStats{},
		Goalies:  []nhlapi.GoalieStats{},
	}

	players := extractTeamPlayers(stats)

	assert.Empty(t, players)
}

func TestExtractTeamPlayers_Forwards(t *testing.T) {
	t.Parallel()

	stats := &nhlapi.TeamPlayerStats{
		Forwards: []nhlapi.SkaterStats{
			{
				PlayerID: nhlapi.PlayerID(8478402),
				Name:     nhlapi.LocalizedString{Default: "Connor McDavid"},
				Position: "C",
			},
			{
				PlayerID: nhlapi.PlayerID(8477934),
				Name:     nhlapi.LocalizedString{Default: "Leon Draisaitl"},
				Position: "C",
			},
		},
		Defense: []nhlapi.SkaterStats{},
		Goalies: []nhlapi.GoalieStats{},
	}

	players := extractTeamPlayers(stats)

	assert.Len(t, players, 2)
	assert.Equal(t, int64(8478402), players[0].ID)
	assert.Equal(t, "Connor", players[0].FirstName)
	assert.Equal(t, "McDavid", players[0].LastName)
	assert.Equal(t, "C", players[0].Position)
	assert.Equal(t, int64(8477934), players[1].ID)
}

func TestExtractTeamPlayers_Defense(t *testing.T) {
	t.Parallel()

	stats := &nhlapi.TeamPlayerStats{
		Forwards: []nhlapi.SkaterStats{},
		Defense: []nhlapi.SkaterStats{
			{
				PlayerID: nhlapi.PlayerID(8480069),
				Name:     nhlapi.LocalizedString{Default: "Cale Makar"},
				Position: "D",
			},
		},
		Goalies: []nhlapi.GoalieStats{},
	}

	players := extractTeamPlayers(stats)

	assert.Len(t, players, 1)
	assert.Equal(t, int64(8480069), players[0].ID)
	assert.Equal(t, "Cale", players[0].FirstName)
	assert.Equal(t, "Makar", players[0].LastName)
	assert.Equal(t, "D", players[0].Position)
}

func TestExtractTeamPlayers_Goalies(t *testing.T) {
	t.Parallel()

	stats := &nhlapi.TeamPlayerStats{
		Forwards: []nhlapi.SkaterStats{},
		Defense:  []nhlapi.SkaterStats{},
		Goalies: []nhlapi.GoalieStats{
			{
				PlayerID: nhlapi.PlayerID(8479394),
				Name:     nhlapi.LocalizedString{Default: "Connor Hellebuyck"},
				Position: "G",
			},
		},
	}

	players := extractTeamPlayers(stats)

	assert.Len(t, players, 1)
	assert.Equal(t, int64(8479394), players[0].ID)
	assert.Equal(t, "Connor", players[0].FirstName)
	assert.Equal(t, "Hellebuyck", players[0].LastName)
	assert.Equal(t, "G", players[0].Position)
}

func TestExtractTeamPlayers_AllPositions(t *testing.T) {
	t.Parallel()

	stats := &nhlapi.TeamPlayerStats{
		Forwards: []nhlapi.SkaterStats{
			{PlayerID: nhlapi.PlayerID(1), Name: nhlapi.LocalizedString{Default: "Forward One"}, Position: "LW"},
			{PlayerID: nhlapi.PlayerID(2), Name: nhlapi.LocalizedString{Default: "Forward Two"}, Position: "RW"},
		},
		Defense: []nhlapi.SkaterStats{
			{PlayerID: nhlapi.PlayerID(3), Name: nhlapi.LocalizedString{Default: "Defense One"}, Position: "D"},
		},
		Goalies: []nhlapi.GoalieStats{
			{PlayerID: nhlapi.PlayerID(4), Name: nhlapi.LocalizedString{Default: "Goalie One"}, Position: "G"},
		},
	}

	players := extractTeamPlayers(stats)

	assert.Len(t, players, 4)
	assert.Equal(t, int64(1), players[0].ID)
	assert.Equal(t, int64(2), players[1].ID)
	assert.Equal(t, int64(3), players[2].ID)
	assert.Equal(t, int64(4), players[3].ID)
}

// --- ExtractBoxscoreDataForSeason tests ---

type ExtractBoxscoreTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *ExtractBoxscoreTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestExtractBoxscoreTestSuite(t *testing.T) {
	suite.Run(t, new(ExtractBoxscoreTestSuite))
}

const gobCacheExpectationCount = 20

func newPermissiveGobCache() (*cache.GobCache, redismock.ClientMock) {
	redisClient, mockRedis := redismock.NewClientMock()
	mockRedis.MatchExpectationsInOrder(false)
	keyPattern := core.RedisResourceKeyPrefix + ".*"
	for range gobCacheExpectationCount {
		mockRedis.Regexp().ExpectGet(keyPattern).SetErr(redis.Nil)
		mockRedis.Regexp().CustomMatch(anyArgs).ExpectSet(keyPattern, "x", cache.GobCacheTTL).SetVal("OK")
	}
	return cache.NewGobCache(redisClient), mockRedis
}

func seedBoxscoreDay(t *testing.T, mem *store.MemStorage, day time.Time, gameID nhlapi.GameID, players []nhlapi.SkaterStats) {
	t.Helper()

	schedule := &nhlapi.DailySchedule{
		Games: []nhlapi.ScheduleGame{{ID: gameID, GameType: nhlapi.GameTypeRegularSeason, GameState: nhlapi.GameStateOff}},
	}
	require.NoError(t, resource.WriteParsed(context.Background(), mem, resource.DailySchedule{Date: day}, schedule))

	type minimalBoxscore struct {
		PlayerByGameStats nhlapi.PlayerByGameStats `json:"playerByGameStats"`
	}
	boxscoreData, err := json.Marshal(minimalBoxscore{
		PlayerByGameStats: nhlapi.PlayerByGameStats{
			HomeTeam: nhlapi.TeamPlayerStats{Forwards: players},
		},
	})
	require.NoError(t, err)
	require.NoError(t, mem.Write(context.Background(),resource.Boxscore{Date: day, GameID: gameID}.Path(), boxscoreData))
}

// testExtractInput creates an ExtractBoxscoreInput from a season for testing.
// In production, EndDate is computed by the workflow using workflow.Now().
func testExtractInput(season nhlapi.SeasonInfo) ExtractBoxscoreInput {
	return ExtractBoxscoreInput{
		Season:  season,
		EndDate: season.StandingsEnd.Time,
	}
}

func (s *ExtractBoxscoreTestSuite) TestEmptySeason() {
	mem := store.NewMemStorage()
	gobCache, _ := newPermissiveGobCache()
	act := &BoxscoreActivities{Storage: mem, GobCache: gobCache}
	s.env.RegisterActivity(act.ExtractBoxscoreDataForSeason)

	season := testSeason(
		time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
	)

	future, err := s.env.ExecuteActivity(act.ExtractBoxscoreDataForSeason, testExtractInput(season))
	s.Require().NoError(err)

	var result BoxscoreExtractionResult
	s.Require().NoError(future.Get(&result))
	s.Empty(result.Players)
}

func (s *ExtractBoxscoreTestSuite) TestSingleDay() {
	mem := store.NewMemStorage()
	gobCache, _ := newPermissiveGobCache()
	act := &BoxscoreActivities{Storage: mem, GobCache: gobCache}
	s.env.RegisterActivity(act.ExtractBoxscoreDataForSeason)

	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	season := testSeason(day, day)

	seedBoxscoreDay(s.T(), mem, day, nhlapi.GameID(2024020001), []nhlapi.SkaterStats{
		{PlayerID: nhlapi.PlayerID(1), Name: nhlapi.LocalizedString{Default: "Connor McDavid"}, Position: "C"},
		{PlayerID: nhlapi.PlayerID(2), Name: nhlapi.LocalizedString{Default: "Leon Draisaitl"}, Position: "C"},
	})

	future, err := s.env.ExecuteActivity(act.ExtractBoxscoreDataForSeason, testExtractInput(season))
	s.Require().NoError(err)

	var result BoxscoreExtractionResult
	s.Require().NoError(future.Get(&result))
	s.Len(result.Players, 2)
}

func (s *ExtractBoxscoreTestSuite) TestDeduplication() {
	mem := store.NewMemStorage()
	gobCache, _ := newPermissiveGobCache()
	act := &BoxscoreActivities{Storage: mem, GobCache: gobCache}
	s.env.RegisterActivity(act.ExtractBoxscoreDataForSeason)

	season := testSeason(
		time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 17, 0, 0, 0, 0, time.UTC),
	)

	mcDavid := []nhlapi.SkaterStats{
		{PlayerID: nhlapi.PlayerID(1), Name: nhlapi.LocalizedString{Default: "Connor McDavid"}, Position: "C"},
	}
	for day := 15; day <= 17; day++ {
		t := time.Date(2024, 1, day, 0, 0, 0, 0, time.UTC)
		seedBoxscoreDay(s.T(), mem, t, nhlapi.GameID(2024020000+day), mcDavid)
	}

	future, err := s.env.ExecuteActivity(act.ExtractBoxscoreDataForSeason, testExtractInput(season))
	s.Require().NoError(err)

	var result BoxscoreExtractionResult
	s.Require().NoError(future.Get(&result))
	s.Len(result.Players, 1)
	s.Equal(int64(1), result.Players[0].ID)
}

func (s *ExtractBoxscoreTestSuite) TestFutureEndDate_WorkflowCapsToYesterday() {
	mem := store.NewMemStorage()
	gobCache, _ := newPermissiveGobCache()
	act := &BoxscoreActivities{Storage: mem, GobCache: gobCache}
	s.env.RegisterActivity(act.ExtractBoxscoreDataForSeason)

	now := time.Now()
	yesterday := now.AddDate(0, 0, -1).Truncate(24 * time.Hour)
	futureDate := now.AddDate(0, 0, 10)

	season := testSeason(yesterday, futureDate)

	// Workflow pre-computes EndDate using shared.EffectiveEndDate(ctx, season.StandingsEnd),
	// which caps future dates to yesterday. Test that the activity works with this capped date.
	input := ExtractBoxscoreInput{
		Season:  season,
		EndDate: yesterday, // Simulates workflow.Now().AddDate(0, 0, -1)
	}

	future, err := s.env.ExecuteActivity(act.ExtractBoxscoreDataForSeason, input)
	s.Require().NoError(err)

	var result BoxscoreExtractionResult
	s.Require().NoError(future.Get(&result))
	s.Empty(result.Players)
}
