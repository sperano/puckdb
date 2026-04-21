package shared

import (
	"context"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	puckhttp "github.com/sperano/puckdb/http"
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
}

// Compile-time check that nhl.Client implements NHLClient
var _ NHLClient = (*nhl.Client)(nil)

// NewNHLClient creates an NHL API client with the configured timeout.
func NewNHLClient() *nhl.Client {
	cfg := nhl.NewClientConfig(nhl.WithConfigTimeout(nhlAPITimeout))
	return nhl.NewClientWithConfig(cfg)
}

// Downloader fetches content from a URL.
type Downloader func(url string) ([]byte, error)

// NewYahooDownloader creates a Downloader that uses the shared Redis client for OAuth2 token management.
func NewYahooDownloader(redisClient cache.Client) Downloader {
	return func(url string) ([]byte, error) {
		ctx := context.WithValue(context.Background(), config.CtxUser, config.DefaultUser)
		return puckhttp.DownloadYahoo(ctx, redisClient, url)
	}
}
