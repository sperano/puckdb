package nhl

import (
	"context"
	"errors"
	"fmt"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/resource"
	"go.temporal.io/sdk/activity"
)

// FetchClubStatsInput contains the parameters for fetching club stats.
type FetchClubStatsInput struct {
	Season int // start year (e.g., 2024 for the 2024-2025 season)
}

// gameTypesToFetch lists the game types for which club stats are fetched.
var gameTypesToFetch = []nhlapi.GameType{
	nhlapi.GameTypeRegularSeason,
	nhlapi.GameTypePlayoffs,
}

// FetchClubStats fetches and caches pre-aggregated club stats for every team
// in the given season for both regular season and playoff game types.
// A cached file is refetched while the games of its type are still being
// played (see seasonAggregateStale). Teams or game types that have no data
// are skipped with a log entry.
func (a *SeasonsActivities) FetchClubStats(ctx context.Context, input FetchClubStatsInput) error {
	logger := activity.GetLogger(ctx)

	season := nhlapi.NewSeason(input.Season)
	teams, err := a.ClubStatsQueries.GetSeasonTeamAbbrevs(ctx, int32(season.ID()))
	if err != nil {
		return fmt.Errorf("get season teams: %w", err)
	}

	for _, team := range teams {
		schedule := readCachedClubSchedule(ctx, a.Storage, a.GobCache, input.Season, team.Abbrev)
		for _, gameType := range gameTypesToFetch {
			activity.RecordHeartbeat(ctx, fmt.Sprintf("clubstats:%s:%d", team.Abbrev, gameType))

			res := resource.ClubStatsResource{
				Season:     input.Season,
				TeamAbbrev: team.Abbrev,
				GameType:   gameType.Int(),
			}
			_, err := fetchSeasonAggregate(ctx, a.Storage, a.GobCache, res, schedule,
				func(ctx context.Context) (*nhlapi.ClubStats, error) {
					return a.NHLClient.ClubStats(ctx, team.Abbrev, season, gameType)
				}, gameType)
			if err != nil {
				if errors.Is(err, nhlapi.ErrNotFound) {
					logger.Warn("No club stats available", "team", team.Abbrev, "gameType", gameType)
					continue
				}
				return fmt.Errorf("fetch club stats for %s (gameType %d): %w", team.Abbrev, gameType, err)
			}
		}
	}

	logger.Info("Fetched club stats", "season", input.Season, "teams", len(teams))
	return nil
}
