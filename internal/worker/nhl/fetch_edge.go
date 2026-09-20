package nhl

import (
	"context"
	"errors"
	"fmt"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
)

// FetchEdgeInput contains the parameters for fetching Edge stats.
type FetchEdgeInput struct {
	Season   int // start year (e.g., 2024 for the 2024-2025 season)
	GameType int // 2 = regular season, 3 = playoffs
	// RefreshCurrent invalidates cached data before fetching. The workflow
	// decides which seasons get it (explicit season range, or the latest
	// season) — activities must not second-guess it against the calendar:
	// nhl.Current() rolls over on July 1, which used to freeze mid-playoff
	// caches for the season that just ended.
	RefreshCurrent bool
}

// FetchEdgeTeamInput contains parameters for fetching Edge stats for a single team.
type FetchEdgeTeamInput struct {
	Season         int    // start year
	GameType       int    // 2 = regular season, 3 = playoffs
	TeamID         int64  // team ID
	TeamAbbrev     string // team abbreviation (for roster lookup)
	RefreshCurrent bool   // see FetchEdgeInput.RefreshCurrent
}

func (i FetchEdgeTeamInput) shouldInvalidate() bool {
	return i.RefreshCurrent
}

// shouldInvalidate returns true if cached Edge data should be deleted before fetching.
func (i FetchEdgeInput) shouldInvalidate() bool {
	return i.RefreshCurrent
}

// EdgeTeamInfo contains team information returned by GetEdgeSeasonTeams.
type EdgeTeamInfo struct {
	TeamID int64
	Abbrev string
}

// GetEdgeSeasonTeams returns all teams for a season (for workflow orchestration).
func (a *SeasonsActivities) GetEdgeSeasonTeams(ctx context.Context, seasonID int) ([]EdgeTeamInfo, error) {
	teams, err := a.ClubStatsQueries.GetSeasonTeamAbbrevs(ctx, int32(seasonID))
	if err != nil {
		return nil, fmt.Errorf("get season teams: %w", err)
	}
	result := make([]EdgeTeamInfo, len(teams))
	for i, t := range teams {
		result[i] = EdgeTeamInfo{TeamID: t.TeamID, Abbrev: t.Abbrev}
	}
	return result, nil
}

// invalidateIfNeeded deletes the cached resource from storage and gob cache when invalidate is true.
func (a *SeasonsActivities) invalidateIfNeeded[T any](ctx context.Context, r core.ReadWritable[T], invalidate bool) {
	if invalidate {
		_ = a.Storage.Delete(ctx, r.Path())
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
	a.invalidateIfNeeded(ctx, skaterRes, invalidate)
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
	a.invalidateIfNeeded(ctx, goalieRes, invalidate)
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
	a.invalidateIfNeeded(ctx, teamRes, invalidate)
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

// FetchEdgeTeamSkaters fetches Edge stats for all skaters on a single team.
func (a *SeasonsActivities) FetchEdgeTeamSkaters(ctx context.Context, input FetchEdgeTeamInput) error {
	logger := activity.GetLogger(ctx)
	season := nhlapi.NewSeason(input.Season)
	gameType := nhlapi.GameType(input.GameType)
	invalidate := input.shouldInvalidate()

	roster, err := a.loadSeasonRoster(ctx, input.TeamAbbrev, season)
	if err != nil {
		logger.Warn("No roster for edge skater fetch", "team", input.TeamAbbrev, "err", err)
		return nil // not an error - team might not have roster cached yet
	}

	var fetched int
	skaters := append(roster.Forwards, roster.Defensemen...)
	for _, player := range skaters {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("edge-skater:%s:%d", input.TeamAbbrev, player.ID))
		playerID := player.ID

		if err := a.fetchEdgeSkaterEndpoints(ctx, playerID, season, gameType, invalidate); err != nil {
			if errors.Is(err, nhlapi.ErrNotFound) {
				continue
			}
			return fmt.Errorf("fetch edge skater %d: %w", playerID, err)
		}
		fetched++
	}

	logger.Debug("Fetched edge skater stats", "team", input.TeamAbbrev, "skaters", fetched)
	return nil
}

func (a *SeasonsActivities) fetchEdgeSkaterEndpoints(ctx context.Context, playerID nhlapi.PlayerID, season nhlapi.Season, gameType nhlapi.GameType, invalidate bool) error {
	// Detail (composite — always fetched first; 404 here means no Edge data)
	detailRes := resource.EdgeSkaterDetail{PlayerID: playerID, Season: season, GameType: gameType}
	a.invalidateIfNeeded(ctx, detailRes, invalidate)
	if _, _, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, detailRes,
		func(ctx context.Context) (*nhlapi.EdgeSkaterDetail, error) {
			return a.NHLClient.EdgeSkaterDetail(ctx, playerID, season, gameType)
		}); err != nil {
		return err
	}

	// Sub-detail endpoints (404s are expected for some players)
	a.fetchOptional(ctx, resource.EdgeSkaterSpeedDetail{PlayerID: playerID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeSkaterSpeedDetail, error) {
			return a.NHLClient.EdgeSkaterSpeedDetail(ctx, playerID, season, gameType)
		}, invalidate)
	a.fetchOptional(ctx, resource.EdgeSkaterDistanceDetail{PlayerID: playerID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeSkaterDistanceDetail, error) {
			return a.NHLClient.EdgeSkaterDistanceDetail(ctx, playerID, season, gameType)
		}, invalidate)
	a.fetchOptional(ctx, resource.EdgeSkaterShotSpeedDetail{PlayerID: playerID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeSkaterShotSpeedDetail, error) {
			return a.NHLClient.EdgeSkaterShotSpeedDetail(ctx, playerID, season, gameType)
		}, invalidate)
	a.fetchOptional(ctx, resource.EdgeSkaterShotLocationDetail{PlayerID: playerID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeSkaterShotLocationDetail, error) {
			return a.NHLClient.EdgeSkaterShotLocationDetail(ctx, playerID, season, gameType)
		}, invalidate)
	a.fetchOptional(ctx, resource.EdgeSkaterZoneTime{PlayerID: playerID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeSkaterZoneTimeDetail, error) {
			return a.NHLClient.EdgeSkaterZoneTime(ctx, playerID, season, gameType)
		}, invalidate)
	a.fetchOptional(ctx, resource.EdgeSkaterComparison{PlayerID: playerID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeSkaterComparison, error) {
			return a.NHLClient.EdgeSkaterComparison(ctx, playerID, season, gameType)
		}, invalidate)

	return nil
}

// FetchEdgeTeamGoalies fetches Edge stats for all goalies on a single team.
func (a *SeasonsActivities) FetchEdgeTeamGoalies(ctx context.Context, input FetchEdgeTeamInput) error {
	logger := activity.GetLogger(ctx)
	season := nhlapi.NewSeason(input.Season)
	gameType := nhlapi.GameType(input.GameType)
	invalidate := input.shouldInvalidate()

	roster, err := a.loadSeasonRoster(ctx, input.TeamAbbrev, season)
	if err != nil {
		logger.Warn("No roster for edge goalie fetch", "team", input.TeamAbbrev, "err", err)
		return nil // not an error - team might not have roster cached yet
	}

	var fetched int
	for _, goalie := range roster.Goalies {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("edge-goalie:%s:%d", input.TeamAbbrev, goalie.ID))
		goalieID := goalie.ID

		if err := a.fetchEdgeGoalieEndpoints(ctx, goalieID, season, gameType, invalidate); err != nil {
			if errors.Is(err, nhlapi.ErrNotFound) {
				continue
			}
			return fmt.Errorf("fetch edge goalie %d: %w", goalieID, err)
		}
		fetched++
	}

	logger.Debug("Fetched edge goalie stats", "team", input.TeamAbbrev, "goalies", fetched)
	return nil
}

func (a *SeasonsActivities) fetchEdgeGoalieEndpoints(ctx context.Context, goalieID nhlapi.PlayerID, season nhlapi.Season, gameType nhlapi.GameType, invalidate bool) error {
	detailRes := resource.EdgeGoalieDetail{GoalieID: goalieID, Season: season, GameType: gameType}
	a.invalidateIfNeeded(ctx, detailRes, invalidate)
	if _, _, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, detailRes,
		func(ctx context.Context) (*nhlapi.EdgeGoalieDetail, error) {
			return a.NHLClient.EdgeGoalieDetail(ctx, goalieID, season, gameType)
		}); err != nil {
		return err
	}

	a.fetchOptional(ctx, resource.EdgeGoalie5v5Detail{GoalieID: goalieID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeGoalie5v5Detail, error) {
			return a.NHLClient.EdgeGoalie5v5Detail(ctx, goalieID, season, gameType)
		}, invalidate)
	a.fetchOptional(ctx, resource.EdgeGoalieShotLocationDetail{GoalieID: goalieID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeGoalieShotLocationDetail, error) {
			return a.NHLClient.EdgeGoalieShotLocationDetail(ctx, goalieID, season, gameType)
		}, invalidate)
	a.fetchOptional(ctx, resource.EdgeGoalieSavePctgDetail{GoalieID: goalieID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeGoalieSavePctgDetail, error) {
			return a.NHLClient.EdgeGoalieSavePctgDetail(ctx, goalieID, season, gameType)
		}, invalidate)
	a.fetchOptional(ctx, resource.EdgeGoalieComparison{GoalieID: goalieID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeGoalieComparison, error) {
			return a.NHLClient.EdgeGoalieComparison(ctx, goalieID, season, gameType)
		}, invalidate)

	return nil
}

// FetchEdgeTeam fetches Edge stats for a single team.
func (a *SeasonsActivities) FetchEdgeTeam(ctx context.Context, input FetchEdgeTeamInput) error {
	logger := activity.GetLogger(ctx)
	season := nhlapi.NewSeason(input.Season)
	gameType := nhlapi.GameType(input.GameType)
	teamID := nhlapi.TeamID(input.TeamID)
	invalidate := input.shouldInvalidate()

	activity.RecordHeartbeat(ctx, fmt.Sprintf("edge-team:%s", input.TeamAbbrev))

	// Team detail
	detailRes := resource.EdgeTeamDetail{TeamID: teamID, Season: season, GameType: gameType}
	a.invalidateIfNeeded(ctx, detailRes, invalidate)
	if _, _, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, detailRes,
		func(ctx context.Context) (*nhlapi.EdgeTeamDetail, error) {
			return a.NHLClient.EdgeTeamDetail(ctx, teamID, season, gameType)
		}); err != nil {
		if errors.Is(err, nhlapi.ErrNotFound) {
			logger.Warn("No edge team detail", "team", input.TeamAbbrev, "gameType", input.GameType)
			return nil
		}
		return fmt.Errorf("fetch edge team detail %s: %w", input.TeamAbbrev, err)
	}

	// Zone time details (separate endpoint, imported to DB)
	a.fetchOptional(ctx, resource.EdgeTeamZoneTimeDetails{TeamID: teamID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeTeamZoneTimeDetails, error) {
			return a.NHLClient.EdgeTeamZoneTimeDetails(ctx, teamID, season, gameType)
		}, invalidate)

	// Sub-detail + comparison (cache-only)
	a.fetchOptional(ctx, resource.EdgeTeamSpeedDetail{TeamID: teamID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeTeamSpeedDetail, error) {
			return a.NHLClient.EdgeTeamSpeedDetail(ctx, teamID, season, gameType)
		}, invalidate)
	a.fetchOptional(ctx, resource.EdgeTeamDistanceDetail{TeamID: teamID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeTeamDistanceDetail, error) {
			return a.NHLClient.EdgeTeamDistanceDetail(ctx, teamID, season, gameType)
		}, invalidate)
	a.fetchOptional(ctx, resource.EdgeTeamShotSpeedDetail{TeamID: teamID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeTeamShotSpeedDetail, error) {
			return a.NHLClient.EdgeTeamShotSpeedDetail(ctx, teamID, season, gameType)
		}, invalidate)
	a.fetchOptional(ctx, resource.EdgeTeamShotLocationDetail{TeamID: teamID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeTeamShotLocationDetail, error) {
			return a.NHLClient.EdgeTeamShotLocationDetail(ctx, teamID, season, gameType)
		}, invalidate)
	a.fetchOptional(ctx, resource.EdgeTeamComparison{TeamID: teamID, Season: season, GameType: gameType},
		func(ctx context.Context) (*nhlapi.EdgeTeamComparison, error) {
			return a.NHLClient.EdgeTeamComparison(ctx, teamID, season, gameType)
		}, invalidate)

	logger.Debug("Fetched edge team stats", "team", input.TeamAbbrev)
	return nil
}

// fetchOptional fetches a resource, silently skipping 404 errors.
// When invalidate is true the cached copy is deleted before fetching.
func (a *SeasonsActivities) fetchOptional[T any](ctx context.Context, r core.ReadWritable[T], fetch func(ctx context.Context) (T, error), invalidate bool) {
	a.invalidateIfNeeded(ctx, r, invalidate)
	_, _, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, r, fetch)
	if err != nil && !errors.Is(err, nhlapi.ErrNotFound) {
		activity.GetLogger(ctx).Warn("Failed to fetch optional edge resource", "path", r.Path(), "err", err)
	}
}

// loadSeasonRoster loads a cached roster for the season.
func (a *SeasonsActivities) loadSeasonRoster(ctx context.Context, teamAbbrev string, season nhlapi.Season) (*nhlapi.Roster, error) {
	res := resource.SeasonRoster{Season: season.StartYear(), TeamAbbrev: teamAbbrev}
	if !a.Storage.Exists(ctx, res.Path()) {
		return nil, fmt.Errorf("roster not cached for %s/%d", teamAbbrev, season.StartYear())
	}
	data, err := a.Storage.Read(ctx, res.Path())
	if err != nil {
		return nil, err
	}
	return res.Parse(data)
}
