package nhl

import (
	"context"
	"errors"
	"fmt"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
)

// FetchSeasonRostersInput contains the parameters for fetching season rosters.
type FetchSeasonRostersInput struct {
	Season int // start year (e.g., 2024 for the 2024-2025 season)
}

// FetchSeasonRosters fetches and caches the roster for every team in the given season.
// The endpoint returns the roster as of the last game, so a cached file is
// refetched while the team still has games to play (see seasonAggregateStale),
// and a cached roster with no positions at all is refetched regardless (see
// cachedRosterLacksPositions).
// Teams that have no roster data (e.g., historical franchises) are skipped with a log entry.
func (a *SeasonsActivities) FetchSeasonRosters(ctx context.Context, input FetchSeasonRostersInput) error {
	logger := activity.GetLogger(ctx)

	season := nhlapi.NewSeason(input.Season)
	teams, err := a.RosterQueries.GetSeasonTeamAbbrevs(ctx, int32(season.ID()))
	if err != nil {
		return fmt.Errorf("get season teams: %w", err)
	}

	for _, team := range teams {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("roster:%s", team.Abbrev))
		res := resource.SeasonRoster{Season: input.Season, TeamAbbrev: team.Abbrev}
		schedule := readCachedClubSchedule(ctx, a.Storage, a.GobCache, input.Season, team.Abbrev)
		fetch := func(ctx context.Context) (*nhlapi.Roster, error) {
			return a.NHLClient.RosterSeason(ctx, team.Abbrev, season)
		}
		var err error
		if a.cachedRosterLacksPositions(ctx, res) {
			logger.Warn("Cached roster has no positions, refetching", "team", team.Abbrev)
			_, _, err = shared.FetchAndCache(ctx, a.Storage, a.GobCache, res, fetch)
		} else {
			_, err = fetchSeasonAggregate(ctx, a.Storage, a.GobCache, res, schedule, fetch)
		}
		if err != nil {
			if errors.Is(err, nhlapi.ErrNotFound) {
				logger.Warn("No roster data available", "team", team.Abbrev)
				continue
			}
			return fmt.Errorf("fetch roster for %s: %w", team.Abbrev, err)
		}
	}

	logger.Info("Fetched season rosters", "season", input.Season, "teams", len(teams))
	return nil
}

// cachedRosterLacksPositions reports whether the cached copy of res has
// players but not a single position. Rosters fetched before nhl-api-go decoded
// the endpoint's positionCode were cached that way, and a finished season's
// roster is otherwise never refetched. A roster that is not cached, or cannot
// be read, is left to fetchSeasonAggregate.
func (a *SeasonsActivities) cachedRosterLacksPositions(ctx context.Context, res resource.SeasonRoster) bool {
	if !a.Storage.Exists(ctx, res.Path()) {
		return false
	}
	roster, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, res)
	if err != nil {
		return false
	}
	players := roster.AllPlayers()
	for _, p := range players {
		if p.Position != "" {
			return false
		}
	}
	return len(players) > 0
}
