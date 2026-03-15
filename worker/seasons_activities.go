package worker

// TODO: use activity logger

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"go.temporal.io/sdk/activity"
)

var redisSeasonsManifestKey = core.RedisKey(resource.SeasonsManifest{})

// seasonsUpserter defines the interface for upserting seasons.
type seasonsUpserter interface {
	UpsertSeason(ctx context.Context, arg sqlcdb.UpsertSeasonParams) error
}

// seasonTeamsUpserter defines the interface for upserting season teams.
type seasonTeamsUpserter interface {
	UpsertSeasonTeam(ctx context.Context, arg sqlcdb.UpsertSeasonTeamParams) error
}

// PlayerGameLogUpdater defines the interface for updating player game log stats.
type PlayerGameLogUpdater interface {
	UpdateSkaterGameLogStats(ctx context.Context, arg sqlcdb.UpdateSkaterGameLogStatsParams) error
}

// YahooDataUpserter defines the interface for batch Yahoo data upsert operations.
type YahooDataUpserter interface {
	UpsertYahooTeamSummaryBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamSummaryBatchParams) *sqlcdb.UpsertYahooTeamSummaryBatchBatchResults
	UpsertYahooTeamSummaryStatBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamSummaryStatBatchParams) *sqlcdb.UpsertYahooTeamSummaryStatBatchBatchResults
	UpsertYahooTeamRosterBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamRosterBatchParams) *sqlcdb.UpsertYahooTeamRosterBatchBatchResults
}

// importQueries is a composite interface for all import activity database operations.
type importQueries interface {
	BoxscoreUpserter
	PlayerGameLogUpdater
	GameStoryUpdater
	PlayByPlayUpserter
	ShiftChartUpserter
	YahooLeagueUpserter
	YahooTeamUpserter
	YahooDataUpserter
}

// SeasonsActivities holds dependencies for season-related activities.
type SeasonsActivities struct {
	Storage             store.Storage
	GobCache            *cache.GobCache
	NHLClient           NHLClient
	SeasonsUpserter     seasonsUpserter
	SeasonTeamsUpserter seasonTeamsUpserter
	ImportQueries       importQueries
	RedisClient         cache.Client // for progress tracking (nil-safe)
}

// --- FetchSeasonsManifest ---

// FetchSeasonsManifestResult contains the result of fetching seasons data.
type FetchSeasonsManifestResult struct {
	Seasons []nhl.SeasonInfo
	Origin  core.DataOrigin
}

// FetchSeasonsManifest fetches season data and filters by input range.
// It reads from the cached seasons manifest first (Redis -> filesystem),
// falling back to the NHL API only if the cache doesn't exist.
func (a *SeasonsActivities) FetchSeasonsManifest(ctx context.Context, input *model.SeasonsInput) (FetchSeasonsManifestResult, error) {
	seasons, origin, err := cache.GetSeasons(ctx, a.Storage, a.GobCache)
	if err != nil {
		return FetchSeasonsManifestResult{Origin: origin}, fmt.Errorf("failed to fetch seasons: %w", err)
	}
	return FetchSeasonsManifestResult{
		Seasons: filterSeasons(seasons, input),
		Origin:  origin,
	}, nil
}

// filterSeasons filters nhl.SeasonInfo by input range.
func filterSeasons(seasons []nhl.SeasonInfo, input *model.SeasonsInput) []nhl.SeasonInfo {
	if input == nil {
		return seasons
	}
	var result []nhl.SeasonInfo
	for _, s := range seasons {
		startYear := s.ID.StartYear()
		if input.StartSeason != nil && startYear < *input.StartSeason {
			continue
		}
		if input.EndSeason != nil && startYear > *input.EndSeason {
			continue
		}
		result = append(result, s)
	}
	return result
}

// --- UpsertSeasons ---

// UpsertSeasonsResult contains results from upserting seasons to database.
type UpsertSeasonsResult struct {
	SeasonsUpserted int `json:"seasonsUpserted"`
}

// UpsertSeasons reads seasons from cache and upserts to database.
func (a *SeasonsActivities) UpsertSeasons(ctx context.Context) (UpsertSeasonsResult, error) {
	logger := activity.GetLogger(ctx)
	result := UpsertSeasonsResult{}

	seasons, origin, err := cache.GetSeasons(ctx, a.Storage, a.GobCache)
	if err != nil {
		return result, fmt.Errorf("read seasons from %s: %w", origin, err)
	}

	for _, s := range seasons {
		params := sqlcdb.UpsertSeasonParams{
			ID:             int32(s.ID.ToInt()),
			StandingsStart: pgtype.Date{Time: s.StandingsStart.Time, Valid: true},
			StandingsEnd:   pgtype.Date{Time: s.StandingsEnd.Time, Valid: true},
		}
		if err := a.SeasonsUpserter.UpsertSeason(ctx, params); err != nil {
			return result, fmt.Errorf("upsert season %d: %w", s.ID.ToInt(), err)
		}
		result.SeasonsUpserted++
	}

	logger.Info("Seasons upserted", "count", result.SeasonsUpserted)
	return result, nil
}

// --- InitializeSeasonTeamsActivity ---

// DownloadSeasonStandingsResult contains statistics from downloading standings for a season.
// Standings data provides team information for each season, used to populate season_teams.
type DownloadSeasonStandingsResult struct {
	Season    nhl.Season // Season ID that was processed
	TeamCount int        // Number of teams in standings
	FromCache bool       // Whether data came from cache
}

// UpsertSeasonTeamsResult contains results from upserting season teams to database.
type UpsertSeasonTeamsResult struct {
	Season        nhl.Season `json:"seasonId"`
	TeamsUpserted int        `json:"teamsUpserted"`
}

// InitializeSeasonTeamsResult contains the combined result of downloading standings and upserting teams for a season.
type InitializeSeasonTeamsResult struct {
	Season         nhl.Season                    `json:"season"`
	DownloadResult DownloadSeasonStandingsResult `json:"downloadResult"`
	UpsertResult   UpsertSeasonTeamsResult       `json:"upsertResult"`
}

// InitializeSeasonTeamsActivity downloads standings and upserts teams for a single season.
// seasonID is the full season identifier (e.g., 20242025), not the start season.
func (a *SeasonsActivities) InitializeSeasonTeamsActivity(ctx context.Context, season nhl.Season) (InitializeSeasonTeamsResult, error) {
	result := InitializeSeasonTeamsResult{Season: season}

	// Download standings
	downloadResult, err := a.downloadSeasonStandings(ctx, season)
	if err != nil {
		return result, fmt.Errorf("download season standings: %w", err)
	}
	result.DownloadResult = downloadResult

	// Read standings from cache and upsert teams
	upsertResult, err := a.upsertSeasonTeams(ctx, season)
	if err != nil {
		return result, fmt.Errorf("upsert season teams: %w", err)
	}
	result.UpsertResult = upsertResult

	return result, nil
}

// downloadSeasonStandings downloads league standings for a single season.
//
// Caching strategy:
//   - Layer 1 (Filesystem): Persistent cache, no TTL (standings are immutable for past seasons)
//   - Layer 2 (API): NHL API fetched only on cache miss or corrupted cache
func (a *SeasonsActivities) downloadSeasonStandings(ctx context.Context, season nhl.Season) (DownloadSeasonStandingsResult, error) {
	// Check cache first
	standingsRes := resource.SeasonStandings{Season: season}
	if a.Storage.Exists(standingsRes.Path()) {
		standings, err := resource.ReadParsed(a.Storage, standingsRes)
		if err == nil {
			log.Debug().Int("season", season.ID()).Int("teams", len(standings)).Msg("Season standings loaded from cache")
			metrics.IncDownload(core.SeasonStandings, metrics.ResultHit)
			return DownloadSeasonStandingsResult{Season: season, TeamCount: len(standings), FromCache: true}, nil
		}
		log.Debug().Err(err).Int("season", season.ID()).Msg("Failed to read cached season standings")
	}

	// Fetch from API
	standings, err := a.NHLClient.LeagueStandingsForSeason(ctx, season)
	if err != nil {
		metrics.IncDownload(core.SeasonStandings, metrics.ResultError)
		return DownloadSeasonStandingsResult{Season: season}, err
	}

	// Save to cache
	if err := resource.WriteParsed(a.Storage, standingsRes, standings); err != nil {
		log.Warn().Err(err).Int("season", season.ID()).Msg("Failed to write season standings to cache")
	}

	log.Info().Int("season", season.ID()).Int("teams", len(standings)).Msg("Season standings downloaded from API")
	metrics.IncDownload(core.SeasonStandings, metrics.ResultMiss)
	return DownloadSeasonStandingsResult{Season: season, TeamCount: len(standings), FromCache: false}, nil
}

// upsertSeasonTeams reads standings from cache and upserts teams to database.
func (a *SeasonsActivities) upsertSeasonTeams(ctx context.Context, season nhl.Season) (UpsertSeasonTeamsResult, error) {
	result := UpsertSeasonTeamsResult{Season: season}

	// Read standings from cache
	standingsRes := resource.SeasonStandings{Season: season}
	standings, err := resource.ReadParsed(a.Storage, standingsRes)
	if err != nil {
		return result, fmt.Errorf("read season standings from cache: %w", err)
	}

	for _, s := range standings {
		var confName, confAbbrev pgtype.Text
		if s.ConferenceName != nil {
			confName = pgtype.Text{String: *s.ConferenceName, Valid: true}
		}
		if s.ConferenceAbbrev != nil {
			confAbbrev = pgtype.Text{String: *s.ConferenceAbbrev, Valid: true}
		}

		params := sqlcdb.UpsertSeasonTeamParams{
			SeasonID:         int32(season.ID()),
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

		if err := a.SeasonTeamsUpserter.UpsertSeasonTeam(ctx, params); err != nil {
			return result, fmt.Errorf("upsert season team %s for season %d: %w", s.TeamAbbrev.String(), season.ID(), err)
		}
		result.TeamsUpserted++
	}

	return result, nil
}

// --- Helper functions for backward compatibility and internal use ---

// fetchSeasonsManifestImpl implements a 3-layer caching strategy (used by tests).
//
//	Layer 1 (Redis):  Fast in-memory cache with short TTL
//	Layer 2 (Filesystem): Persistent cache with staleness check (DefaultSeasonsManifestStaleTTL)
//	Layer 3 (API):    Original source fetched when caches miss or are stale
//
// Fallback behavior: If the API fails but stale filesystem data exists, we use
// the stale data rather than failing the activity. This provides resilience
// against temporary API outages.
func fetchSeasonsManifestImpl(
	ctx context.Context,
	storage store.Storage,
	redisClient cache.Client,
	nhlClient NHLClient,
) (FetchSeasonsManifestResult, error) {
	// Layer 1: Redis cache (fastest, short-lived)
	if data, err := redisClient.Get(ctx, redisSeasonsManifestKey).Bytes(); err == nil {
		response, err := resource.SeasonsManifest{}.Parse(data)
		if err == nil {
			log.Debug().Int("count", len(response.Seasons)).Msg("Seasons manifest loaded from Redis")
			metrics.IncDownload(core.SeasonsManifest, metrics.ResultRedisHit)
			return FetchSeasonsManifestResult{
				Origin:  core.OriginRedis,
				Seasons: response.Seasons,
			}, nil
		}
		log.Warn().Err(err).Msg("Failed to unmarshal Redis seasons manifest")
	}

	// Layer 2: Filesystem cache with staleness check
	// If fresh: return it and backfill Redis
	// If stale: save for fallback, proceed to API fetch
	var staleData []byte
	manifestRes := resource.SeasonsManifest{}
	if storage.Exists(manifestRes.Path()) {
		data, err := storage.Read(manifestRes.Path())
		if err == nil {
			info, statErr := storage.Stat(manifestRes.Path())
			// File is stale if: stat failed (but file exists) OR modtime exceeds staleness TTL
			isStale := (statErr != nil && !os.IsNotExist(statErr)) ||
				(statErr == nil && time.Since(info.ModTime()) > config.DefaultSeasonsManifestStaleTTL)

			if !isStale {
				// Fresh cache hit - use it and backfill Redis
				response, err := resource.SeasonsManifest{}.Parse(data)
				if err != nil {
					return FetchSeasonsManifestResult{}, err
				}
				metrics.IncDownload(core.SeasonsManifest, metrics.ResultFSHit)
				cacheInRedis(ctx, redisClient, data)
				return FetchSeasonsManifestResult{
					Origin:  core.OriginFileSystem,
					Seasons: response.Seasons,
				}, nil
			}
			// File is stale - save for fallback in case API fails
			staleData = data
			if statErr == nil {
				log.Debug().Time("modTime", info.ModTime()).Msg("Seasons manifest is stale, will refresh")
			} else {
				log.Debug().Err(statErr).Msg("Seasons manifest stat failed, will refresh")
			}
		}
	}

	// Layer 3: Fetch from NHL API
	seasons, err := nhlClient.SeasonStandingManifest(ctx)
	if err != nil {
		// API failed - fall back to stale data if available
		if len(staleData) > 0 {
			staleResponse, unmarshalErr := resource.SeasonsManifest{}.Parse(staleData)
			if unmarshalErr == nil {
				log.Warn().Err(err).Int("count", len(staleResponse.Seasons)).Msg("API fetch failed, using stale seasons manifest")
				metrics.IncDownload(core.SeasonsManifest, metrics.ResultStaleFallback)
				cacheInRedis(ctx, redisClient, staleData)
				return FetchSeasonsManifestResult{
					Origin:  core.OriginFileSystem,
					Seasons: staleResponse.Seasons,
				}, nil
			}
		}
		metrics.IncDownload(core.SeasonsManifest, metrics.ResultError)
		return FetchSeasonsManifestResult{}, err
	}

	// API success - persist to both caches
	data, _ := manifestRes.Format(nhl.SeasonsResponse{Seasons: seasons})
	if err := storage.Write(manifestRes.Path(), data); err != nil {
		log.Warn().Err(err).Msg("Failed to write seasons manifest to filesystem")
	}
	cacheInRedis(ctx, redisClient, data)
	log.Info().Int("count", len(seasons)).Msg("Seasons manifest downloaded from API")
	metrics.IncDownload(core.SeasonsManifest, metrics.ResultAPIFetch)
	return FetchSeasonsManifestResult{
		Origin:  core.OriginRemoteNHLAPI,
		Seasons: seasons,
	}, nil
}

// cacheInRedis stores the seasons manifest in Redis with TTL.
func cacheInRedis(ctx context.Context, client cache.Client, data []byte) {
	if err := client.Set(ctx, redisSeasonsManifestKey, data, config.DefaultSeasonsManifestCacheTTL).Err(); err != nil {
		log.Warn().Err(err).Msg("Failed to cache seasons manifest in Redis")
	}
}

// downloadSeasonStandingsImpl is the standalone version for testing.
func downloadSeasonStandingsImpl(
	ctx context.Context,
	storage store.Storage,
	client NHLClient,
	season nhl.Season,
) (DownloadSeasonStandingsResult, error) {
	// Check cache first
	standingsRes := resource.SeasonStandings{Season: season}
	if storage.Exists(standingsRes.Path()) {
		standings, err := resource.ReadParsed(storage, standingsRes)
		if err == nil {
			log.Debug().Int("season", season.ID()).Int("teams", len(standings)).Msg("Season standings loaded from cache")
			metrics.IncDownload(core.SeasonStandings, metrics.ResultHit)
			return DownloadSeasonStandingsResult{Season: season, TeamCount: len(standings), FromCache: true}, nil
		}
		log.Debug().Err(err).Int("season", season.ID()).Msg("Failed to read cached season standings")
	}

	// Fetch from API
	standings, err := client.LeagueStandingsForSeason(ctx, season)
	if err != nil {
		metrics.IncDownload(core.SeasonStandings, metrics.ResultError)
		return DownloadSeasonStandingsResult{Season: season}, err
	}

	// Save to cache
	if err := resource.WriteParsed(storage, standingsRes, standings); err != nil {
		log.Warn().Err(err).Int("season", season.ID()).Msg("Failed to write season standings to cache")
	}

	log.Info().Int("season", season.ID()).Int("teams", len(standings)).Msg("Season standings downloaded from API")
	metrics.IncDownload(core.SeasonStandings, metrics.ResultMiss)
	return DownloadSeasonStandingsResult{Season: season, TeamCount: len(standings), FromCache: false}, nil
}
