package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/urls"
)

// dayFetcher abstracts fetching operations for testability.
type dayFetcher interface {
	FetchDailySchedule(ctx context.Context, day time.Time) error
	FetchRoster(ctx context.Context, leagueID, teamID int, day time.Time) error
	FetchTeamSummary(ctx context.Context, leagueID, teamID int, day time.Time) error
}

// realDayFetcher calls the impl functions directly with pre-initialized dependencies.
type realDayFetcher struct {
	fs            store.Store
	nhlClient     NHLClient
	gameKey       int
	download      Downloader
	gameDownloads GameDataDownloaders
}

func (f realDayFetcher) FetchDailySchedule(ctx context.Context, day time.Time) error {
	return fetchDailyScheduleImpl(ctx, f.fs, f.nhlClient, day, f.gameDownloads)
}

func (f realDayFetcher) FetchRoster(ctx context.Context, leagueID, teamID int, day time.Time) error {
	log.Trace().Time("day", day).Int("gameKey", f.gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("Fetching Yahoo roster")
	file := store.RosterFile{Date: day, LeagueID: leagueID, TeamID: teamID}
	url := urls.YahooRosterURL(f.gameKey, leagueID, teamID, day)
	return doDownloadImpl(ctx, f.fs, file, url, f.download)
}

func (f realDayFetcher) FetchTeamSummary(ctx context.Context, leagueID, teamID int, day time.Time) error {
	log.Trace().Time("day", day).Int("gameKey", f.gameKey).Int("leagueID", leagueID).Int("team", teamID).Msg("Fetching Yahoo team summary")
	file := store.TeamSummaryFile{Date: day, LeagueID: leagueID, TeamID: teamID}
	url := urls.YahooTeamSummaryURL(f.gameKey, leagueID, teamID, day)
	return doDownloadImpl(ctx, f.fs, file, url, f.download)
}

// FetchDayActivity fetches all data for a single day.
// This is used by FetchSeasonWorkflow for better performance (avoiding child workflow overhead).
//
// TeamIDs should be pre-computed by the parent workflow from the Yahoo seasons config.
// If TeamIDs is empty, only NHL data (daily schedule/boxscores) is fetched.
func FetchDayActivity(ctx context.Context, input FetchDayInput) error {
	defer metrics.TrackActivityDuration("FetchDayActivity")()

	fs := store.NewStore()
	nhlClient := newNHLClient()

	// Only look up gameKey if we have teams to fetch
	var gameKey int
	if len(input.TeamIDs) > 0 {
		var err error
		gameKey, err = GetGameKeyForSeason(input.StartYear)
		if err != nil {
			return err
		}
	}

	fetcher := realDayFetcher{
		fs:        fs,
		nhlClient: nhlClient,
		gameKey:   gameKey,
		download:  DownloadFromYahoo,
		gameDownloads: GameDataDownloaders{
			Boxscore:   DownloadBoxscore,
			PlayByPlay: DownloadPlayByPlay,
			ShiftChart: DownloadShiftChart,
		},
	}
	return fetchDayImpl(ctx, fetcher, input)
}

func fetchDayImpl(ctx context.Context, fetcher dayFetcher, input FetchDayInput) error {
	log.Debug().
		Time("day", input.Day).
		Int("startYear", input.StartYear).
		Int("numTeams", len(input.TeamIDs)).
		Msg("FetchDayActivity started")

	// Fetch daily schedule (boxscores)
	if err := fetcher.FetchDailySchedule(ctx, input.Day); err != nil {
		return err
	}

	// Fetch Yahoo rosters and summaries for each team (if any configured)
	for _, team := range input.TeamIDs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := fetcher.FetchRoster(ctx, team.LeagueID, team.TeamID, input.Day); err != nil {
			return err
		}
		if err := fetcher.FetchTeamSummary(ctx, team.LeagueID, team.TeamID, input.Day); err != nil {
			return err
		}
	}

	return nil
}
