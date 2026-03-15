package worker

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// testSeason creates a nhl.SeasonInfo for testing with the given start and end dates.
func testSeason(start, end time.Time) nhl.SeasonInfo {
	return nhl.SeasonInfo{
		ID:             nhl.NewSeason(start.Year()),
		StandingsStart: nhl.DateFromTime(start),
		StandingsEnd:   nhl.DateFromTime(end),
	}
}

func TestParseCombinedName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		input         string
		wantFirstName string
		wantLastName  string
	}{
		{
			name:          "simple two part name",
			input:         "Connor McDavid",
			wantFirstName: "Connor",
			wantLastName:  "McDavid",
		},
		{
			name:          "hyphenated first name",
			input:         "Pierre-Luc Dubois",
			wantFirstName: "Pierre-Luc",
			wantLastName:  "Dubois",
		},
		{
			name:          "multi-part last name",
			input:         "James van Riemsdyk",
			wantFirstName: "James",
			wantLastName:  "van Riemsdyk",
		},
		{
			name:          "first name only",
			input:         "Cher",
			wantFirstName: "Cher",
			wantLastName:  "",
		},
		{
			name:          "empty string",
			input:         "",
			wantFirstName: "",
			wantLastName:  "",
		},
		{
			name:          "whitespace only",
			input:         "   ",
			wantFirstName: "",
			wantLastName:  "",
		},
		{
			name:          "leading/trailing whitespace",
			input:         "  Connor McDavid  ",
			wantFirstName: "Connor",
			wantLastName:  "McDavid",
		},
		{
			name:          "three part name",
			input:         "Jean-Gabriel Pageau",
			wantFirstName: "Jean-Gabriel",
			wantLastName:  "Pageau",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			firstName, lastName := store.ParseCombinedName(tt.input)
			assert.Equal(t, tt.wantFirstName, firstName, "firstName mismatch")
			assert.Equal(t, tt.wantLastName, lastName, "lastName mismatch")
		})
	}
}

func TestExtractTeamPlayers_Empty(t *testing.T) {
	t.Parallel()

	stats := &nhl.TeamPlayerStats{
		Forwards: []nhl.SkaterStats{},
		Defense:  []nhl.SkaterStats{},
		Goalies:  []nhl.GoalieStats{},
	}

	players := extractTeamPlayers(stats)

	assert.Empty(t, players)
}

func TestExtractTeamPlayers_Forwards(t *testing.T) {
	t.Parallel()

	stats := &nhl.TeamPlayerStats{
		Forwards: []nhl.SkaterStats{
			{
				PlayerID: nhl.PlayerID(8478402),
				Name:     nhl.LocalizedString{Default: "Connor McDavid"},
				Position: "C",
			},
			{
				PlayerID: nhl.PlayerID(8477934),
				Name:     nhl.LocalizedString{Default: "Leon Draisaitl"},
				Position: "C",
			},
		},
		Defense: []nhl.SkaterStats{},
		Goalies: []nhl.GoalieStats{},
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

	stats := &nhl.TeamPlayerStats{
		Forwards: []nhl.SkaterStats{},
		Defense: []nhl.SkaterStats{
			{
				PlayerID: nhl.PlayerID(8480069),
				Name:     nhl.LocalizedString{Default: "Cale Makar"},
				Position: "D",
			},
		},
		Goalies: []nhl.GoalieStats{},
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

	stats := &nhl.TeamPlayerStats{
		Forwards: []nhl.SkaterStats{},
		Defense:  []nhl.SkaterStats{},
		Goalies: []nhl.GoalieStats{
			{
				PlayerID: nhl.PlayerID(8479394),
				Name:     nhl.LocalizedString{Default: "Connor Hellebuyck"},
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

	stats := &nhl.TeamPlayerStats{
		Forwards: []nhl.SkaterStats{
			{PlayerID: nhl.PlayerID(1), Name: nhl.LocalizedString{Default: "Forward One"}, Position: "LW"},
			{PlayerID: nhl.PlayerID(2), Name: nhl.LocalizedString{Default: "Forward Two"}, Position: "RW"},
		},
		Defense: []nhl.SkaterStats{
			{PlayerID: nhl.PlayerID(3), Name: nhl.LocalizedString{Default: "Defense One"}, Position: "D"},
		},
		Goalies: []nhl.GoalieStats{
			{PlayerID: nhl.PlayerID(4), Name: nhl.LocalizedString{Default: "Goalie One"}, Position: "G"},
		},
	}

	players := extractTeamPlayers(stats)

	assert.Len(t, players, 4)
	// Verify order: forwards, then defense, then goalies
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

// gobCacheExpectationCount is enough GET+SET pairs for our test cases (up to ~10 resources per test).
const gobCacheExpectationCount = 20

// newPermissiveGobCache returns a GobCache where Get always misses and Set always succeeds.
// Each Regexp expectation in redismock matches exactly once, so we register enough pairs
// to cover all ReadParsedCached calls across a test.
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

// seedBoxscoreDay writes a DailySchedule (with one game) and a Boxscore for that game into storage.
func seedBoxscoreDay(t *testing.T, mem *store.MemStorage, day time.Time, gameID nhl.GameID, players []nhl.SkaterStats) {
	t.Helper()

	schedule := &nhl.DailySchedule{
		Games: []nhl.ScheduleGame{{ID: gameID, GameType: nhl.GameTypeRegularSeason, GameState: nhl.GameStateOff}},
	}
	require.NoError(t, resource.WriteParsed(mem, resource.DailySchedule{Date: day}, schedule))

	// Build boxscore JSON manually — nhl.Boxscore has strict MarshalJSON on
	// GameType/GameState that rejects zero values, but we only need playerByGameStats.
	type minimalBoxscore struct {
		PlayerByGameStats nhl.PlayerByGameStats `json:"playerByGameStats"`
	}
	boxscoreData, err := json.Marshal(minimalBoxscore{
		PlayerByGameStats: nhl.PlayerByGameStats{
			HomeTeam: nhl.TeamPlayerStats{Forwards: players},
		},
	})
	require.NoError(t, err)
	require.NoError(t, mem.Write(resource.Boxscore{Date: day, GameID: gameID}.Path(), boxscoreData))
}

func (s *ExtractBoxscoreTestSuite) TestEmptySeason() {
	mem := store.NewMemStorage()
	gobCache, _ := newPermissiveGobCache()
	act := &BoxscoreActivities{Storage: mem, GobCache: gobCache}
	s.env.RegisterActivity(act.ExtractBoxscoreDataForSeason)

	// Season where start > end (no days to process)
	season := testSeason(
		time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC),
	)

	future, err := s.env.ExecuteActivity(act.ExtractBoxscoreDataForSeason, season)
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

	seedBoxscoreDay(s.T(), mem, day, nhl.GameID(2024020001), []nhl.SkaterStats{
		{PlayerID: nhl.PlayerID(1), Name: nhl.LocalizedString{Default: "Connor McDavid"}, Position: "C"},
		{PlayerID: nhl.PlayerID(2), Name: nhl.LocalizedString{Default: "Leon Draisaitl"}, Position: "C"},
	})

	future, err := s.env.ExecuteActivity(act.ExtractBoxscoreDataForSeason, season)
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
		time.Date(2024, 1, 17, 0, 0, 0, 0, time.UTC), // 3 days
	)

	mcDavid := []nhl.SkaterStats{
		{PlayerID: nhl.PlayerID(1), Name: nhl.LocalizedString{Default: "Connor McDavid"}, Position: "C"},
	}
	// Same player appears in boxscores across 3 days
	for d := 15; d <= 17; d++ {
		day := time.Date(2024, 1, d, 0, 0, 0, 0, time.UTC)
		seedBoxscoreDay(s.T(), mem, day, nhl.GameID(2024020000+d), mcDavid)
	}

	future, err := s.env.ExecuteActivity(act.ExtractBoxscoreDataForSeason, season)
	s.Require().NoError(err)

	var result BoxscoreExtractionResult
	s.Require().NoError(future.Get(&result))
	s.Len(result.Players, 1) // Deduplicated to 1 player
	s.Equal(int64(1), result.Players[0].ID)
}

func (s *ExtractBoxscoreTestSuite) TestFutureEndDate() {
	mem := store.NewMemStorage()
	gobCache, _ := newPermissiveGobCache()
	act := &BoxscoreActivities{Storage: mem, GobCache: gobCache}
	s.env.RegisterActivity(act.ExtractBoxscoreDataForSeason)

	now := time.Now()
	yesterday := now.AddDate(0, 0, -1).Truncate(24 * time.Hour)
	futureDate := now.AddDate(0, 0, 10) // 10 days in future

	season := testSeason(yesterday, futureDate) // end date should be clamped

	// No data seeded — we just verify the day range is clamped (no panic, returns empty)
	future, err := s.env.ExecuteActivity(act.ExtractBoxscoreDataForSeason, season)
	s.Require().NoError(err)

	var result BoxscoreExtractionResult
	s.Require().NoError(future.Get(&result))
	s.Empty(result.Players) // No data seeded, so no players extracted
}
