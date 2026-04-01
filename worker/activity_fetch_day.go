package worker

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"go.temporal.io/sdk/activity"
)

// fetchableYahooResource combines URL fetching and parsing capabilities.
// Both resource.Roster and resource.TeamSummary satisfy this interface.
type fetchableYahooResource interface {
	core.URLResource
	Parse(data []byte) (*store.FantasyContent, error)
}

func (a *DailyScheduleActivities) fetchYahooResource(ctx context.Context, res fetchableYahooResource) error {
	logger := activity.GetLogger(ctx)
	typeName := res.Type().String()

	_, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, res)
	if err == nil {
		logger.Debug(typeName+" loaded from cache", "path", res.Path())
		metrics.IncDownload(res.Type(), metrics.ResultHit)
		return nil
	}

	start := time.Now()
	content, err := a.Download(res.URL())
	duration := time.Since(start)
	if err != nil {
		metrics.ObserveHTTP("yahoo", http.MethodGet, 0, duration, 0)
		metrics.IncDownload(res.Type(), metrics.ResultError)
		return fmt.Errorf("download %s: %w", typeName, err)
	}
	metrics.ObserveHTTP("yahoo", http.MethodGet, http.StatusOK, duration, len(content))

	if err := a.Storage.Write(res.Path(), content); err != nil {
		return fmt.Errorf("save %s: %w", typeName, err)
	}

	parsed, err := res.Parse(content)
	if err != nil {
		return err
	}
	if err := cache.Set(a.GobCache, ctx, core.RedisKey(res), parsed); err != nil {
		return fmt.Errorf("gob cache set %s: %w", typeName, err)
	}

	logger.Info(typeName+" downloaded", "path", res.Path())
	metrics.IncDownload(res.Type(), metrics.ResultMiss)
	sleepAfterYahooDownload()
	return nil
}

// FetchDay fetches all data for a single day.
// This is used by FetchSeasonWorkflow for better performance (avoiding child workflow overhead).
//
// YahooTeamIDs should be pre-computed by the parent workflow from the Yahoo seasons config.
// If YahooTeamIDs is empty, only NHL data (daily schedule/boxscores) is fetched.
func (a *DailyScheduleActivities) FetchDay(ctx context.Context, input FetchDayInput) (core.OriginCounts, error) {
	defer metrics.TrackActivityDuration("FetchDay")()
	activity.RecordHeartbeat(ctx, nil)
	var gameKey int
	if len(input.TeamIDs) > 0 {
		var err error
		gameKey, err = getGameKeyForSeason(ctx, a.Storage, a.GobCache, a.Download, input.StartSeason)
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
	_, _, err = FetchOrCache(ctx, a.Storage, a.GobCache, standingsRes,
		func(ctx context.Context) ([]nhl.Standing, error) {
			return a.NHLClient.LeagueStandingsForDate(ctx, nhl.FromDate(input.Day))
		})
	if err != nil {
		return nil, fmt.Errorf("fetch standings for %s: %w", input.Day.Format(config.DateFormat), err)
	}

	for _, team := range input.TeamIDs {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		activity.RecordHeartbeat(ctx, nil)
		if err := a.fetchYahooResource(ctx, resource.Roster{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: input.Day, GameKey: gameKey}); err != nil {
			return nil, err
		}
		if err := a.fetchYahooResource(ctx, resource.TeamSummary{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: input.Day, GameKey: gameKey}); err != nil {
			return nil, err
		}
	}

	return counts, nil
}
