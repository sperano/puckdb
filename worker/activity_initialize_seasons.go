package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"go.temporal.io/sdk/activity"
)

const redisSeasonsManifestKey = "seasons-manifest"

// DownloadSeasonsManifestResult contains statistics from the seasons manifest download.
type DownloadSeasonsManifestResult struct {
	Count     int  // Number of seasons in manifest
	FromCache bool // Whether data came from cache
}

// DownloadSeasonsManifestActivity downloads the NHL seasons manifest.
// Uses multi-layer caching: Redis (1h TTL) -> Filesystem (24h staleness) -> API.
func DownloadSeasonsManifestActivity(ctx context.Context) (DownloadSeasonsManifestResult, error) {
	repos := store.NewDefaultRepos()
	redisClient := cache.NewClient()
	defer redisClient.Close()
	nhlClient := newNHLClient()
	return downloadSeasonsManifestImpl(ctx, repos, redisClient, nhlClient)
}

func downloadSeasonsManifestImpl(
	ctx context.Context,
	repos *store.Repos,
	redisClient cache.Client,
	nhlClient NHLClient,
) (DownloadSeasonsManifestResult, error) {
	// Layer 1: Check Redis cache first
	if data, err := redisClient.Get(ctx, redisSeasonsManifestKey).Bytes(); err == nil {
		var seasons []nhl.SeasonInfo
		if err := json.Unmarshal(data, &seasons); err == nil {
			log.Debug().Int("count", len(seasons)).Msg("Seasons manifest loaded from Redis")
			metrics.IncDownload(store.FileTypeSeasonsManifest, "redis_hit")
			return DownloadSeasonsManifestResult{Count: len(seasons), FromCache: true}, nil
		}
		log.Debug().Err(err).Msg("Failed to unmarshal Redis seasons manifest")
	}

	// Layer 2: Check filesystem cache
	var staleData []byte
	if repos.Season.ManifestExists() {
		data, err := repos.Season.GetManifestRaw()
		if err == nil {
			// Check if file is stale
			info, statErr := repos.Storage.Stat(store.SeasonsManifestPath())
			isStale := statErr != nil || time.Since(info.ModTime()) > config.DefaultSeasonsManifestStaleTTL

			if !isStale {
				var seasons []nhl.SeasonInfo
				if err := json.Unmarshal(data, &seasons); err == nil {
					log.Debug().Int("count", len(seasons)).Msg("Seasons manifest loaded from filesystem")
					metrics.IncDownload(store.FileTypeSeasonsManifest, "fs_hit")
					// Cache in Redis for faster subsequent reads
					cacheInRedis(ctx, redisClient, data)
					return DownloadSeasonsManifestResult{Count: len(seasons), FromCache: true}, nil
				}
			} else {
				// Keep stale data for fallback
				staleData = data
				if statErr == nil {
					log.Debug().Time("modTime", info.ModTime()).Msg("Seasons manifest is stale, will refresh")
				} else {
					log.Debug().Err(statErr).Msg("Seasons manifest stat failed, will refresh")
				}
			}
		}
	}

	// Layer 3: Fetch from API
	seasons, err := nhlClient.SeasonStandingManifest(ctx)
	if err != nil {
		// If download fails but we have stale data, use it with a warning
		if len(staleData) > 0 {
			var staleSeasons []nhl.SeasonInfo
			if unmarshalErr := json.Unmarshal(staleData, &staleSeasons); unmarshalErr == nil {
				log.Warn().Err(err).Int("count", len(staleSeasons)).Msg("API fetch failed, using stale seasons manifest")
				metrics.IncDownload(store.FileTypeSeasonsManifest, "stale_fallback")
				cacheInRedis(ctx, redisClient, staleData)
				return DownloadSeasonsManifestResult{Count: len(staleSeasons), FromCache: true}, nil
			}
		}
		metrics.IncDownload(store.FileTypeSeasonsManifest, "error")
		return DownloadSeasonsManifestResult{}, err
	}

	// Save to filesystem
	data, err := json.Marshal(seasons)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to marshal seasons manifest")
	} else {
		if err := repos.Season.SaveManifest(data); err != nil {
			log.Warn().Err(err).Msg("Failed to write seasons manifest to filesystem")
		}
		// Cache in Redis
		cacheInRedis(ctx, redisClient, data)
	}

	log.Info().Int("count", len(seasons)).Msg("Seasons manifest downloaded from API")
	metrics.IncDownload(store.FileTypeSeasonsManifest, "api_fetch")

	return DownloadSeasonsManifestResult{Count: len(seasons), FromCache: false}, nil
}

// cacheInRedis stores the seasons manifest in Redis with TTL.
func cacheInRedis(ctx context.Context, client cache.Client, data []byte) {
	if err := client.Set(ctx, redisSeasonsManifestKey, data, config.DefaultSeasonsManifestCacheTTL).Err(); err != nil {
		log.Warn().Err(err).Msg("Failed to cache seasons manifest in Redis")
	}
}

// DownloadSeasonStandingsResult contains statistics from downloading standings for a season.
type DownloadSeasonStandingsResult struct {
	SeasonID  int  // Season ID that was processed
	TeamCount int  // Number of teams in standings
	FromCache bool // Whether data came from cache
}

func downloadSeasonStandingsImpl(
	ctx context.Context,
	repos *store.Repos,
	client NHLClient,
	seasonID int,
) (DownloadSeasonStandingsResult, error) {
	// Check cache first
	if repos.Season.StandingsExists(seasonID) {
		standings, err := repos.Season.GetStandings(seasonID)
		if err == nil {
			log.Debug().Int("season", seasonID).Int("teams", len(standings)).Msg("Season standings loaded from cache")
			metrics.IncDownload(store.FileTypeSeasonStandings, "hit")
			return DownloadSeasonStandingsResult{SeasonID: seasonID, TeamCount: len(standings), FromCache: true}, nil
		}
		log.Debug().Err(err).Int("season", seasonID).Msg("Failed to read cached season standings")
	}

	// Fetch from API
	season, err := nhl.SeasonFromInt(seasonID)
	if err != nil {
		return DownloadSeasonStandingsResult{SeasonID: seasonID}, fmt.Errorf("parse season ID %d: %w", seasonID, err)
	}
	standings, err := client.LeagueStandingsForSeason(ctx, season)
	if err != nil {
		metrics.IncDownload(store.FileTypeSeasonStandings, "error")
		return DownloadSeasonStandingsResult{SeasonID: seasonID}, err
	}

	// Save to cache
	data, err := json.Marshal(standings)
	if err != nil {
		log.Warn().Err(err).Int("season", seasonID).Msg("Failed to marshal season standings for cache")
	} else {
		if err := repos.Season.SaveStandings(seasonID, data); err != nil {
			log.Warn().Err(err).Int("season", seasonID).Msg("Failed to write season standings to cache")
		}
	}

	log.Info().Int("season", seasonID).Int("teams", len(standings)).Msg("Season standings downloaded from API")
	metrics.IncDownload(store.FileTypeSeasonStandings, "miss")

	return DownloadSeasonStandingsResult{SeasonID: seasonID, TeamCount: len(standings), FromCache: false}, nil
}

// UpsertSeasonsResult contains results from upserting seasons to database.
type UpsertSeasonsResult struct {
	SeasonsUpserted int `json:"seasonsUpserted"`
}

// UpsertSeasonsActivity reads seasons from cache and upserts to database.
func UpsertSeasonsActivity(ctx context.Context) (UpsertSeasonsResult, error) {
	logger := activity.GetLogger(ctx)

	// Read seasons manifest from cache
	repos := store.NewDefaultRepos()

	seasons, err := repos.Season.GetManifest()
	if err != nil {
		return UpsertSeasonsResult{}, fmt.Errorf("read seasons manifest from cache: %w", err)
	}

	// Open database connection
	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return UpsertSeasonsResult{}, fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()

	queries := sqlcdb.New(pool)

	result, err := upsertSeasonsImpl(ctx, queries, seasons)
	if err != nil {
		return result, err
	}

	logger.Info("Seasons upserted", "count", result.SeasonsUpserted)
	return result, nil
}

type seasonsUpserter interface {
	UpsertSeason(ctx context.Context, arg sqlcdb.UpsertSeasonParams) error
}

func upsertSeasonsImpl(
	ctx context.Context,
	queries seasonsUpserter,
	seasons []nhl.SeasonInfo,
) (UpsertSeasonsResult, error) {
	result := UpsertSeasonsResult{}

	for _, s := range seasons {
		startDate, err := time.Parse("2006-01-02", s.StandingsStart)
		if err != nil {
			return result, fmt.Errorf("parse standings start for season %d: %w", s.ID.ToInt(), err)
		}
		endDate, err := time.Parse("2006-01-02", s.StandingsEnd)
		if err != nil {
			return result, fmt.Errorf("parse standings end for season %d: %w", s.ID.ToInt(), err)
		}

		params := sqlcdb.UpsertSeasonParams{
			ID:             int32(s.ID.ToInt()),
			StandingsStart: pgtype.Date{Time: startDate, Valid: true},
			StandingsEnd:   pgtype.Date{Time: endDate, Valid: true},
		}

		if err := queries.UpsertSeason(ctx, params); err != nil {
			return result, fmt.Errorf("upsert season %d: %w", s.ID.ToInt(), err)
		}

		result.SeasonsUpserted++
	}

	return result, nil
}

// UpsertSeasonTeamsResult contains results from upserting season teams to database.
type UpsertSeasonTeamsResult struct {
	SeasonID      int `json:"seasonId"`
	TeamsUpserted int `json:"teamsUpserted"`
}

type seasonTeamsUpserter interface {
	UpsertSeasonTeam(ctx context.Context, arg sqlcdb.UpsertSeasonTeamParams) error
}

func upsertSeasonTeamsImpl(
	ctx context.Context,
	queries seasonTeamsUpserter,
	seasonID int,
	standings []nhl.Standing,
) (UpsertSeasonTeamsResult, error) {
	result := UpsertSeasonTeamsResult{SeasonID: seasonID}

	for _, s := range standings {
		// Convert standing to team to get team ID
		// Note: Standing doesn't have team ID directly, we need to look it up
		// For now, we'll use abbreviation as key and leave team_id as 0
		// TODO: Map abbreviations to team IDs using franchise data or API

		var confName, confAbbrev pgtype.Text
		if s.ConferenceName != nil {
			confName = pgtype.Text{String: *s.ConferenceName, Valid: true}
		}
		if s.ConferenceAbbrev != nil {
			confAbbrev = pgtype.Text{String: *s.ConferenceAbbrev, Valid: true}
		}

		params := sqlcdb.UpsertSeasonTeamParams{
			SeasonID:         int32(seasonID),
			TeamID:           LookupTeamID(s.TeamAbbrev.String()),
			FranchiseID:      pgtype.Int8{Valid: false}, // Will link later
			FullName:         s.TeamName.String(),
			Abbrev:           s.TeamAbbrev.String(),
			LogoUrl:          pgtype.Text{String: s.TeamLogo, Valid: s.TeamLogo != ""},
			DivisionName:     s.DivisionName,
			DivisionAbbrev:   s.DivisionAbbrev,
			ConferenceName:   confName,
			ConferenceAbbrev: confAbbrev,
		}

		if err := queries.UpsertSeasonTeam(ctx, params); err != nil {
			return result, fmt.Errorf("upsert season team %s for season %d: %w", s.TeamAbbrev.String(), seasonID, err)
		}

		result.TeamsUpserted++
	}

	return result, nil
}

// InitializeSeasonTeamsResult contains the combined result of downloading standings and upserting teams for a season.
type InitializeSeasonTeamsResult struct {
	SeasonID       int                           `json:"seasonId"`
	DownloadResult DownloadSeasonStandingsResult `json:"downloadResult"`
	UpsertResult   UpsertSeasonTeamsResult       `json:"upsertResult"`
}

// seasonTeamsInitializer abstracts the sub-operations for testing.
type seasonTeamsInitializer interface {
	DownloadStandings(ctx context.Context, seasonID int) (DownloadSeasonStandingsResult, error)
	UpsertTeams(ctx context.Context, seasonID int) (UpsertSeasonTeamsResult, error)
}

// realSeasonTeamsInitializer calls the impl functions directly with pre-initialized dependencies.
type realSeasonTeamsInitializer struct {
	repos   *store.Repos
	client  NHLClient
	queries seasonTeamsUpserter
}

func (r realSeasonTeamsInitializer) DownloadStandings(ctx context.Context, seasonID int) (DownloadSeasonStandingsResult, error) {
	return downloadSeasonStandingsImpl(ctx, r.repos, r.client, seasonID)
}

func (r realSeasonTeamsInitializer) UpsertTeams(ctx context.Context, seasonID int) (UpsertSeasonTeamsResult, error) {
	// Read standings from cache
	standings, err := r.repos.Season.GetStandings(seasonID)
	if err != nil {
		return UpsertSeasonTeamsResult{SeasonID: seasonID}, fmt.Errorf("read season standings from cache: %w", err)
	}

	return upsertSeasonTeamsImpl(ctx, r.queries, seasonID, standings)
}

// InitializeSeasonTeamsActivity downloads standings and upserts teams for a single season.
func InitializeSeasonTeamsActivity(ctx context.Context, seasonID int) (InitializeSeasonTeamsResult, error) {
	repos := store.NewDefaultRepos()
	client := newNHLClient()

	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return InitializeSeasonTeamsResult{SeasonID: seasonID}, fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()

	queries := sqlcdb.New(pool)

	return initializeSeasonTeamsImpl(ctx, realSeasonTeamsInitializer{
		repos:   repos,
		client:  client,
		queries: queries,
	}, seasonID)
}

func initializeSeasonTeamsImpl(ctx context.Context, init seasonTeamsInitializer, seasonID int) (InitializeSeasonTeamsResult, error) {
	result := InitializeSeasonTeamsResult{SeasonID: seasonID}

	// Download standings
	downloadResult, err := init.DownloadStandings(ctx, seasonID)
	if err != nil {
		return result, fmt.Errorf("download season standings: %w", err)
	}
	result.DownloadResult = downloadResult

	// Upsert teams
	upsertResult, err := init.UpsertTeams(ctx, seasonID)
	if err != nil {
		return result, fmt.Errorf("upsert season teams: %w", err)
	}
	result.UpsertResult = upsertResult

	return result, nil
}
