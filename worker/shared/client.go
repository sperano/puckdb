package shared

import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/httpx"
)

const nhlAPITimeout = 30 * time.Second

// NHLClient defines the NHL API methods used by worker activities.
// The real implementation is nhl.Client; tests can provide mock implementations.
type NHLClient interface {
	PlayerLanding(ctx context.Context, playerID nhl.PlayerID) (*nhl.PlayerLanding, error)
	Boxscore(ctx context.Context, gameID nhl.GameID) (*nhl.Boxscore, error)
	PlayByPlay(ctx context.Context, gameID nhl.GameID) (*nhl.PlayByPlay, error)
	ShiftChart(ctx context.Context, gameID nhl.GameID) (*nhl.ShiftChart, error)
	GameStory(ctx context.Context, gameID nhl.GameID) (*nhl.GameStory, error)
	SeasonSeries(ctx context.Context, gameID nhl.GameID) (*nhl.SeasonSeriesMatchup, error)
	PlayerGameLog(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.PlayerGameLog, error)
	DailySchedule(ctx context.Context, date nhl.GameDate) (*nhl.DailySchedule, error)
	SeasonStandingManifest(ctx context.Context) ([]nhl.SeasonInfo, error)
	LeagueStandingsForSeason(ctx context.Context, season nhl.Season) ([]nhl.Standing, error)
	Franchises(ctx context.Context) ([]nhl.Franchise, error)
	SearchPlayer(ctx context.Context, query string, limit *int) ([]nhl.PlayerSearchResult, error)
	LeagueStandingsForDate(ctx context.Context, date nhl.GameDate) ([]nhl.Standing, error)
	RosterSeason(ctx context.Context, teamAbbr string, season nhl.Season) (*nhl.Roster, error)
	ClubStats(ctx context.Context, teamAbbr string, season nhl.Season, gameType nhl.GameType) (*nhl.ClubStats, error)
	ClubScheduleSeason(ctx context.Context, teamAbbr string, season nhl.Season) (*nhl.TeamScheduleResponse, error)

	// Edge methods
	EdgeSkaterDetail(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeSkaterDetail, error)
	EdgeSkaterSpeedDetail(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeSkaterSpeedDetail, error)
	EdgeSkaterDistanceDetail(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeSkaterDistanceDetail, error)
	EdgeSkaterShotSpeedDetail(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeSkaterShotSpeedDetail, error)
	EdgeSkaterShotLocationDetail(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeSkaterShotLocationDetail, error)
	EdgeSkaterZoneTime(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeSkaterZoneTimeDetail, error)
	EdgeSkaterComparison(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeSkaterComparison, error)
	EdgeGoalieDetail(ctx context.Context, goalieID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeGoalieDetail, error)
	EdgeGoalie5v5Detail(ctx context.Context, goalieID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeGoalie5v5Detail, error)
	EdgeGoalieShotLocationDetail(ctx context.Context, goalieID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeGoalieShotLocationDetail, error)
	EdgeGoalieSavePctgDetail(ctx context.Context, goalieID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeGoalieSavePctgDetail, error)
	EdgeGoalieComparison(ctx context.Context, goalieID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeGoalieComparison, error)
	EdgeTeamDetail(ctx context.Context, teamID nhl.TeamID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeTeamDetail, error)
	EdgeTeamSpeedDetail(ctx context.Context, teamID nhl.TeamID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeTeamSpeedDetail, error)
	EdgeTeamDistanceDetail(ctx context.Context, teamID nhl.TeamID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeTeamDistanceDetail, error)
	EdgeTeamShotSpeedDetail(ctx context.Context, teamID nhl.TeamID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeTeamShotSpeedDetail, error)
	EdgeTeamShotLocationDetail(ctx context.Context, teamID nhl.TeamID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeTeamShotLocationDetail, error)
	EdgeTeamZoneTimeDetails(ctx context.Context, teamID nhl.TeamID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeTeamZoneTimeDetails, error)
	EdgeTeamComparison(ctx context.Context, teamID nhl.TeamID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeTeamComparison, error)
	EdgeSkaterLanding(ctx context.Context, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeSkaterLanding, error)
	EdgeGoalieLanding(ctx context.Context, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeGoalieLanding, error)
	EdgeTeamLanding(ctx context.Context, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeTeamLanding, error)
}

// Compile-time check that nhl.Client implements NHLClient
var _ NHLClient = (*nhl.Client)(nil)

// NewNHLClient creates an NHL API client with the configured timeout.
func NewNHLClient() *nhl.Client {
	cfg := nhl.NewClientConfig(nhl.WithConfigTimeout(nhlAPITimeout))
	return nhl.NewClientWithConfig(cfg)
}

// Downloader fetches content from a URL. The ctx is used for cancellation
// and propagates into the underlying HTTP request.
type Downloader func(ctx context.Context, url string) ([]byte, error)

// NewYahooDownloader creates a Downloader that uses the shared Redis client
// for OAuth2 token management. The returned Downloader honors the caller's
// context for cancellation and deadlines.
func NewYahooDownloader(redisClient *redis.Client) Downloader {
	return func(ctx context.Context, url string) ([]byte, error) {
		ctx = context.WithValue(ctx, config.CtxUser, config.DefaultUser)
		return httpx.DownloadYahoo(ctx, redisClient, url)
	}
}
