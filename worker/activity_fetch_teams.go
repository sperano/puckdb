package worker

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/urls"
)

// TeamInfo identifies a team within a league for Yahoo downloads.
type TeamInfo struct {
	LeagueID int
	TeamID   int
}

// FetchTeamsInput contains parameters for fetching multiple teams in a single activity.
type FetchTeamsInput struct {
	Season int
	Teams  []TeamInfo
}

// teamFetcher abstracts team fetching for testability.
type teamFetcher interface {
	FetchTeam(ctx context.Context, season, gameKey, leagueID, teamID int) error
}

// realTeamFetcher calls the actual fetch implementation.
type realTeamFetcher struct {
	repos    *store.Repos
	download Downloader
}

func (f realTeamFetcher) FetchTeam(ctx context.Context, season, gameKey, leagueID, teamID int) error {
	log.Trace().Int("season", season).Int("gameKey", gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("Fetching Yahoo Team")
	path := store.TeamPath(season, leagueID, teamID)
	url := urls.YahooTeamURL(gameKey, leagueID, teamID)
	return doDownloadImpl(ctx, f.repos.Storage, path, url, f.download, "TeamFile")
}

// FetchTeamsActivity downloads Yahoo fantasy team pages for multiple teams.
// This batches what would otherwise be N separate activity calls into one.
func FetchTeamsActivity(ctx context.Context, input FetchTeamsInput) error {
	defer metrics.TrackActivityDuration("FetchTeamsActivity")()

	gameKey, err := GetGameKeyForSeason(input.Season)
	if err != nil {
		return err
	}

	repos := store.NewDefaultRepos()
	fetcher := realTeamFetcher{repos: repos, download: DownloadFromYahoo}
	return fetchTeamsImpl(ctx, fetcher, input.Season, gameKey, input.Teams)
}

func fetchTeamsImpl(ctx context.Context, fetcher teamFetcher, season, gameKey int, teams []TeamInfo) error {
	log.Debug().
		Int("season", season).
		Int("gameKey", gameKey).
		Int("numTeams", len(teams)).
		Msg("FetchTeamsActivity started")

	for _, team := range teams {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := fetcher.FetchTeam(ctx, season, gameKey, team.LeagueID, team.TeamID); err != nil {
			return err
		}
	}

	return nil
}
