package nhl

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"go.temporal.io/sdk/activity"
	"golang.org/x/sync/errgroup"
)

// fetchYahooResource fetches one Yahoo resource through the shared cache-or-download primitive.
func (a *DailyScheduleActivities) fetchYahooResource(ctx context.Context, res yahoo.Resource) error {
	fetcher := yahoo.Fetcher{Storage: a.Storage, GobCache: a.GobCache, Download: a.Download}
	_, origin, err := fetcher.Fetch(ctx, res)
	if err != nil {
		return fmt.Errorf("fetch %s %s: %w", res.Type(), res.Path(), err)
	}
	logger := activity.GetLogger(ctx)
	if origin == core.OriginRemoteYahooAPI {
		logger.Info(res.Type().String()+" downloaded", "path", res.Path())
	} else {
		logger.Debug(res.Type().String()+" loaded from cache", "path", res.Path())
	}
	return nil
}

// FetchDayInput contains parameters for FetchDay.
// TeamIDs should be pre-computed by the parent workflow from the Yahoo seasons config.
// If TeamIDs is empty, only NHL data (daily schedule/boxscores) is fetched.
type FetchDayInput struct {
	Day         time.Time
	StartSeason int              // Season start year (e.g., 2023 for 2023-2024 season)
	TeamIDs     []yahoo.TeamInfo // Teams to fetch Yahoo data for (empty if no Yahoo config)
	DayIndex    int              // 0-based index for progress tracking
	TotalDays   int              // Total days in season for progress tracking
}

// FetchDay fetches all data for a single day.
// This is used by FetchNHLSeasonWorkflow for better performance (avoiding child workflow overhead).
func (a *DailyScheduleActivities) FetchDay(ctx context.Context, input FetchDayInput) (core.OriginCounts, error) {
	defer metrics.TrackActivityDuration("FetchDay")()
	activity.RecordHeartbeat(ctx, nil)
	var gameKey int
	if len(input.TeamIDs) > 0 {
		var err error
		gameKey, err = yahoo.GetGameKeyForSeason(ctx, a.Storage, a.GobCache, a.Download, input.StartSeason)
		if err != nil {
			return nil, err
		}
	}

	log.Debug().
		Time("day", input.Day).
		Int("startYear", input.StartSeason).
		Int("numTeams", len(input.TeamIDs)).
		Msg("FetchDay started")

	counts := core.OriginCounts{}

	result, err := a.FetchDailySchedule(ctx, input.Day)
	if err != nil {
		return nil, err
	}
	counts.Record(result.Origin)

	standingsRes := resource.DailyStandings{Date: input.Day}
	_, _, err = shared.FetchOrCache(ctx, a.Storage, a.GobCache, standingsRes,
		func(ctx context.Context) ([]nhlapi.Standing, error) {
			return a.NHLClient.LeagueStandingsForDate(ctx, nhlapi.FromDate(input.Day))
		})
	if err != nil {
		return nil, fmt.Errorf("fetch standings for %s: %w", input.Day.Format(config.DateFormat), err)
	}

	if err := a.fetchYahooTeams(ctx, input.Day, gameKey, input.TeamIDs); err != nil {
		return nil, err
	}

	return counts, nil
}

// fetchYahooTeams fetches every team's roster and team summary for day, at
// most getYahooTeamDownloadConcurrency downloads at a time; the process-wide
// Yahoo rate limiter inside a.Download still paces the requests. The first
// failure stops the rest, and its error names the team.
func (a *DailyScheduleActivities) fetchYahooTeams(ctx context.Context, day time.Time, gameKey int, teams []yahoo.TeamInfo) error {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(getYahooTeamDownloadConcurrency())
schedule:
	for _, team := range teams {
		for _, res := range yahooTeamResources(team, day, gameKey) {
			if gctx.Err() != nil {
				break schedule
			}
			g.Go(func() error {
				activity.RecordHeartbeat(gctx, nil)
				if err := a.fetchYahooResource(gctx, res); err != nil {
					return fmt.Errorf("league %d team %d: %w", team.LeagueID, team.TeamID, err)
				}
				return nil
			})
		}
	}
	return g.Wait()
}

// yahooTeamResources lists the Yahoo downloads FetchDay makes for one team.
func yahooTeamResources(team yahoo.TeamInfo, day time.Time, gameKey int) []yahoo.Resource {
	return []yahoo.Resource{
		resource.Roster{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: day, GameKey: gameKey},
		resource.TeamSummary{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: day, GameKey: gameKey},
	}
}

func getYahooTeamDownloadConcurrency() int {
	return shared.ViperIntOrDefault(config.FlagYahooTeamDownloadConcurrency, config.DefaultYahooTeamDownloadConcurrency)
}
