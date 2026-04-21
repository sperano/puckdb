package nhl

import (
	"context"
	"errors"
	"fmt"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/worker/shared"
	"go.temporal.io/sdk/activity"
)

// FetchEdgeInput contains the parameters for fetching Edge stats.
type FetchEdgeInput struct {
	Season         int  // start year (e.g., 2024 for the 2024-2025 season)
	GameType       int  // 2 = regular season, 3 = playoffs
	RefreshCurrent bool // if true AND current season, invalidate cache before fetching
}

// shouldInvalidate returns true if cached Edge data should be deleted before fetching.
func (i FetchEdgeInput) shouldInvalidate() bool {
	return i.RefreshCurrent && shared.IsCurrentSeason(i.Season)
}

// invalidateIfNeeded deletes the cached resource from storage and gob cache when invalidate is true.
func invalidateIfNeeded[T any](ctx context.Context, a *SeasonsActivities, r core.ReadWritable[T], invalidate bool) {
	if invalidate {
		_ = a.Storage.Delete(r.Path())
		_ = a.GobCache.Delete(ctx, core.RedisKey(r))
	}
}

// FetchEdgeLandings fetches league-wide Edge landing pages (3 calls total).
func (a *SeasonsActivities) FetchEdgeLandings(ctx context.Context, input FetchEdgeInput) error {
	logger := activity.GetLogger(ctx)
	season := nhlapi.NewSeason(input.Season)
	gameType := nhlapi.GameType(input.GameType)
	invalidate := input.shouldInvalidate()

	// Skater landing
	activity.RecordHeartbeat(ctx, "edge-landing:skater")
	skaterRes := resource.EdgeSkaterLanding{Season: season, GameType: gameType}
	invalidateIfNeeded(ctx, a, skaterRes, invalidate)
	if _, _, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, skaterRes,
		func(ctx context.Context) (*nhlapi.EdgeSkaterLanding, error) {
			return a.NHLClient.EdgeSkaterLanding(ctx, season, gameType)
		}); err != nil {
		if !errors.Is(err, nhlapi.ErrNotFound) {
			return fmt.Errorf("fetch edge skater landing: %w", err)
		}
		logger.Warn("No edge skater landing available", "season", input.Season)
	}

	// Goalie landing
	activity.RecordHeartbeat(ctx, "edge-landing:goalie")
	goalieRes := resource.EdgeGoalieLanding{Season: season, GameType: gameType}
	invalidateIfNeeded(ctx, a, goalieRes, invalidate)
	if _, _, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, goalieRes,
		func(ctx context.Context) (*nhlapi.EdgeGoalieLanding, error) {
			return a.NHLClient.EdgeGoalieLanding(ctx, season, gameType)
		}); err != nil {
		if !errors.Is(err, nhlapi.ErrNotFound) {
			return fmt.Errorf("fetch edge goalie landing: %w", err)
		}
		logger.Warn("No edge goalie landing available", "season", input.Season)
	}

	// Team landing
	activity.RecordHeartbeat(ctx, "edge-landing:team")
	teamRes := resource.EdgeTeamLanding{Season: season, GameType: gameType}
	invalidateIfNeeded(ctx, a, teamRes, invalidate)
	if _, _, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, teamRes,
		func(ctx context.Context) (*nhlapi.EdgeTeamLanding, error) {
			return a.NHLClient.EdgeTeamLanding(ctx, season, gameType)
		}); err != nil {
		if !errors.Is(err, nhlapi.ErrNotFound) {
			return fmt.Errorf("fetch edge team landing: %w", err)
		}
		logger.Warn("No edge team landing available", "season", input.Season)
	}

	logger.Info("Fetched edge landings", "season", input.Season, "gameType", input.GameType)
	return nil
}

// FetchEdgeSkaters fetches Edge stats for all skaters in a season.
func (a *SeasonsActivities) FetchEdgeSkaters(ctx context.Context, input FetchEdgeInput) error {
	logger := activity.GetLogger(ctx)
	season := nhlapi.NewSeason(input.Season)
	gameType := nhlapi.GameType(input.GameType)
	invalidate := input.shouldInvalidate()

	teams, err := a.ClubStatsQueries.GetSeasonTeamAbbrevs(ctx, int32(season.ID()))
	if err != nil {
		return fmt.Errorf("get season teams: %w", err)
	}

	var fetched int
	for _, team := range teams {
		roster, err := a.loadSeasonRoster(ctx, team.Abbrev, season)
		if err != nil {
			logger.Warn("No roster for edge skater fetch", "team", team.Abbrev, "err", err)
			continue
		}

		skaters := append(roster.Forwards, roster.Defensemen...)
		for _, player := range skaters {
			activity.RecordHeartbeat(ctx, fmt.Sprintf("edge-skater:%s:%d", team.Abbrev, player.ID))
			playerID := player.ID

			if err := fetchEdgeSkaterEndpoints(ctx, a, playerID, season, gameType, invalidate); err != nil {
				if errors.Is(err, nhlapi.ErrNotFound) {
					continue
				}
				return fmt.Errorf("fetch edge skater %d: %w", playerID, err)
			}
			fetched++
		}
	}

	logger.Info("Fetched edge skater stats", "season", input.Season, "skaters", fetched)
	return nil
}

func fetchEdgeSkaterEndpoints(ctx context.Context, a *SeasonsActivities, playerID nhlapi.PlayerID, season nhlapi.Season, gameType nhlapi.GameType, invalidate bool) error {
	// Detail (composite — always fetched first; 404 here means no Edge data)
	detailRes := resource.EdgeSkaterDetail{PlayerID: playerID, Season: season, GameType: gameType}
	invalidateIfNeeded(ctx, a, detailRes, invalidate)
	if _, _, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, detailRes,
		func(ctx context.Context) (*nhlapi.EdgeSkaterDetail, error) {
			return a.NHLClient.EdgeSkaterDetail(ctx, playerID, season, gameType)
		}); err != nil {
		return err
	}

	// Sub-detail endpoints (404s are expected for some players)
	fetchOptional(ctx, a, resource.EdgeSkaterSpeedDetail{PlayerID: playerID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeSkaterSpeedDetail, error) {
			return a.NHLClient.EdgeSkaterSpeedDetail(ctx, playerID, season, gameType)
		}, invalidate)
	fetchOptional(ctx, a, resource.EdgeSkaterDistanceDetail{PlayerID: playerID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeSkaterDistanceDetail, error) {
			return a.NHLClient.EdgeSkaterDistanceDetail(ctx, playerID, season, gameType)
		}, invalidate)
	fetchOptional(ctx, a, resource.EdgeSkaterShotSpeedDetail{PlayerID: playerID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeSkaterShotSpeedDetail, error) {
			return a.NHLClient.EdgeSkaterShotSpeedDetail(ctx, playerID, season, gameType)
		}, invalidate)
	fetchOptional(ctx, a, resource.EdgeSkaterShotLocationDetail{PlayerID: playerID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeSkaterShotLocationDetail, error) {
			return a.NHLClient.EdgeSkaterShotLocationDetail(ctx, playerID, season, gameType)
		}, invalidate)
	fetchOptional(ctx, a, resource.EdgeSkaterZoneTime{PlayerID: playerID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeSkaterZoneTimeDetail, error) {
			return a.NHLClient.EdgeSkaterZoneTime(ctx, playerID, season, gameType)
		}, invalidate)
	fetchOptional(ctx, a, resource.EdgeSkaterComparison{PlayerID: playerID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeSkaterComparison, error) {
			return a.NHLClient.EdgeSkaterComparison(ctx, playerID, season, gameType)
		}, invalidate)

	return nil
}

// FetchEdgeGoalies fetches Edge stats for all goalies in a season.
func (a *SeasonsActivities) FetchEdgeGoalies(ctx context.Context, input FetchEdgeInput) error {
	logger := activity.GetLogger(ctx)
	season := nhlapi.NewSeason(input.Season)
	gameType := nhlapi.GameType(input.GameType)
	invalidate := input.shouldInvalidate()

	teams, err := a.ClubStatsQueries.GetSeasonTeamAbbrevs(ctx, int32(season.ID()))
	if err != nil {
		return fmt.Errorf("get season teams: %w", err)
	}

	var fetched int
	for _, team := range teams {
		roster, err := a.loadSeasonRoster(ctx, team.Abbrev, season)
		if err != nil {
			logger.Warn("No roster for edge goalie fetch", "team", team.Abbrev, "err", err)
			continue
		}

		for _, goalie := range roster.Goalies {
			activity.RecordHeartbeat(ctx, fmt.Sprintf("edge-goalie:%s:%d", team.Abbrev, goalie.ID))
			goalieID := goalie.ID

			if err := fetchEdgeGoalieEndpoints(ctx, a, goalieID, season, gameType, invalidate); err != nil {
				if errors.Is(err, nhlapi.ErrNotFound) {
					continue
				}
				return fmt.Errorf("fetch edge goalie %d: %w", goalieID, err)
			}
			fetched++
		}
	}

	logger.Info("Fetched edge goalie stats", "season", input.Season, "goalies", fetched)
	return nil
}

func fetchEdgeGoalieEndpoints(ctx context.Context, a *SeasonsActivities, goalieID nhlapi.PlayerID, season nhlapi.Season, gameType nhlapi.GameType, invalidate bool) error {
	detailRes := resource.EdgeGoalieDetail{GoalieID: goalieID, Season: season, GameType: gameType}
	invalidateIfNeeded(ctx, a, detailRes, invalidate)
	if _, _, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, detailRes,
		func(ctx context.Context) (*nhlapi.EdgeGoalieDetail, error) {
			return a.NHLClient.EdgeGoalieDetail(ctx, goalieID, season, gameType)
		}); err != nil {
		return err
	}

	fetchOptional(ctx, a, resource.EdgeGoalie5v5Detail{GoalieID: goalieID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeGoalie5v5Detail, error) {
			return a.NHLClient.EdgeGoalie5v5Detail(ctx, goalieID, season, gameType)
		}, invalidate)
	fetchOptional(ctx, a, resource.EdgeGoalieShotLocationDetail{GoalieID: goalieID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeGoalieShotLocationDetail, error) {
			return a.NHLClient.EdgeGoalieShotLocationDetail(ctx, goalieID, season, gameType)
		}, invalidate)
	fetchOptional(ctx, a, resource.EdgeGoalieSavePctgDetail{GoalieID: goalieID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeGoalieSavePctgDetail, error) {
			return a.NHLClient.EdgeGoalieSavePctgDetail(ctx, goalieID, season, gameType)
		}, invalidate)
	fetchOptional(ctx, a, resource.EdgeGoalieComparison{GoalieID: goalieID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeGoalieComparison, error) {
			return a.NHLClient.EdgeGoalieComparison(ctx, goalieID, season, gameType)
		}, invalidate)

	return nil
}

// FetchEdgeTeams fetches Edge stats for all teams in a season.
func (a *SeasonsActivities) FetchEdgeTeams(ctx context.Context, input FetchEdgeInput) error {
	logger := activity.GetLogger(ctx)
	season := nhlapi.NewSeason(input.Season)
	gameType := nhlapi.GameType(input.GameType)
	invalidate := input.shouldInvalidate()

	teams, err := a.ClubStatsQueries.GetSeasonTeamAbbrevs(ctx, int32(season.ID()))
	if err != nil {
		return fmt.Errorf("get season teams: %w", err)
	}

	for _, team := range teams {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("edge-team:%s", team.Abbrev))
		teamID := nhlapi.TeamID(team.TeamID)

		// Team detail
		detailRes := resource.EdgeTeamDetail{TeamID: teamID, Season: season, GameType: gameType}
		invalidateIfNeeded(ctx, a, detailRes, invalidate)
		if _, _, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, detailRes,
			func(ctx context.Context) (*nhlapi.EdgeTeamDetail, error) {
				return a.NHLClient.EdgeTeamDetail(ctx, teamID, season, gameType)
			}); err != nil {
			if errors.Is(err, nhlapi.ErrNotFound) {
				logger.Warn("No edge team detail", "team", team.Abbrev)
				continue
			}
			return fmt.Errorf("fetch edge team detail %s: %w", team.Abbrev, err)
		}

		// Zone time details (separate endpoint, imported to DB)
		fetchOptional(ctx, a, resource.EdgeTeamZoneTimeDetails{TeamID: teamID, Season: season, GameType: gameType},
			func(ctx context.Context) (*nhlapi.EdgeTeamZoneTimeDetails, error) {
				return a.NHLClient.EdgeTeamZoneTimeDetails(ctx, teamID, season, gameType)
			}, invalidate)

		// Sub-detail + comparison (cache-only)
		fetchOptional(ctx, a, resource.EdgeTeamSpeedDetail{TeamID: teamID, Season: season, GameType: gameType},
			func(ctx context.Context) (*nhlapi.EdgeTeamSpeedDetail, error) {
				return a.NHLClient.EdgeTeamSpeedDetail(ctx, teamID, season, gameType)
			}, invalidate)
		fetchOptional(ctx, a, resource.EdgeTeamDistanceDetail{TeamID: teamID, Season: season, GameType: gameType},
			func(ctx context.Context) (*nhlapi.EdgeTeamDistanceDetail, error) {
				return a.NHLClient.EdgeTeamDistanceDetail(ctx, teamID, season, gameType)
			}, invalidate)
		fetchOptional(ctx, a, resource.EdgeTeamShotSpeedDetail{TeamID: teamID, Season: season, GameType: gameType},
			func(ctx context.Context) (*nhlapi.EdgeTeamShotSpeedDetail, error) {
				return a.NHLClient.EdgeTeamShotSpeedDetail(ctx, teamID, season, gameType)
			}, invalidate)
		fetchOptional(ctx, a, resource.EdgeTeamShotLocationDetail{TeamID: teamID, Season: season, GameType: gameType},
			func(ctx context.Context) (*nhlapi.EdgeTeamShotLocationDetail, error) {
				return a.NHLClient.EdgeTeamShotLocationDetail(ctx, teamID, season, gameType)
			}, invalidate)
		fetchOptional(ctx, a, resource.EdgeTeamComparison{TeamID: teamID, Season: season, GameType: gameType},
			func(ctx context.Context) (*nhlapi.EdgeTeamComparison, error) {
				return a.NHLClient.EdgeTeamComparison(ctx, teamID, season, gameType)
			}, invalidate)
	}

	logger.Info("Fetched edge team stats", "season", input.Season, "teams", len(teams))
	return nil
}

// fetchOptional fetches a resource, silently skipping 404 errors.
// When invalidate is true the cached copy is deleted before fetching.
func fetchOptional[T any](ctx context.Context, a *SeasonsActivities, r core.ReadWritable[T], fetch func(ctx context.Context) (T, error), invalidate bool) {
	invalidateIfNeeded(ctx, a, r, invalidate)
	_, _, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, r, fetch)
	if err != nil && !errors.Is(err, nhlapi.ErrNotFound) {
		activity.GetLogger(ctx).Warn("Failed to fetch optional edge resource", "path", r.Path(), "err", err)
	}
}

// loadSeasonRoster loads a cached roster for the season.
func (a *SeasonsActivities) loadSeasonRoster(ctx context.Context, teamAbbrev string, season nhlapi.Season) (*nhlapi.Roster, error) {
	res := resource.SeasonRoster{Season: season.StartYear(), TeamAbbrev: teamAbbrev}
	if !a.Storage.Exists(res.Path()) {
		return nil, fmt.Errorf("roster not cached for %s/%d", teamAbbrev, season.StartYear())
	}
	data, err := a.Storage.Read(res.Path())
	if err != nil {
		return nil, err
	}
	return res.Parse(data)
}
