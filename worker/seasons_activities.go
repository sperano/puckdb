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
	SeasonSeriesUpserter
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
// Uses a 3-layer cache: Redis → filesystem (with staleness check) → NHL API.
// If the API fails but stale filesystem data exists, falls back to stale data.
func (a *SeasonsActivities) FetchSeasonsManifest(ctx context.Context, input *model.SeasonsInput) (FetchSeasonsManifestResult, error) {
	// Layer 1: Redis cache (fastest, short-lived)
	if data, err := a.RedisClient.Get(ctx, redisSeasonsManifestKey).Bytes(); err == nil {
		response, err := resource.SeasonsManifest{}.Parse(data)
		if err == nil {
			log.Debug().Int("count", len(response.Seasons)).Msg("Seasons manifest loaded from Redis")
			metrics.IncDownload(core.SeasonsManifest, metrics.ResultRedisHit)
			return FetchSeasonsManifestResult{
				Origin:  core.OriginRedis,
				Seasons: filterSeasons(response.Seasons, input),
			}, nil
		}
		log.Warn().Err(err).Msg("Failed to unmarshal Redis seasons manifest")
	}

	// Layer 2: Filesystem cache with staleness check
	var staleData []byte
	manifestRes := resource.SeasonsManifest{}
	if a.Storage.Exists(manifestRes.Path()) {
		data, err := a.Storage.Read(manifestRes.Path())
		if err == nil {
			info, statErr := a.Storage.Stat(manifestRes.Path())
			isStale := (statErr != nil && !os.IsNotExist(statErr)) ||
				(statErr == nil && time.Since(info.ModTime()) > config.DefaultSeasonsManifestStaleTTL)

			if !isStale {
				response, err := resource.SeasonsManifest{}.Parse(data)
				if err != nil {
					return FetchSeasonsManifestResult{}, err
				}
				metrics.IncDownload(core.SeasonsManifest, metrics.ResultFSHit)
				cacheInRedis(ctx, a.RedisClient, data)
				return FetchSeasonsManifestResult{
					Origin:  core.OriginFileSystem,
					Seasons: filterSeasons(response.Seasons, input),
				}, nil
			}
			staleData = data
			if statErr == nil {
				log.Debug().Time("modTime", info.ModTime()).Msg("Seasons manifest is stale, will refresh")
			} else {
				log.Debug().Err(statErr).Msg("Seasons manifest stat failed, will refresh")
			}
		}
	}

	// Layer 3: Fetch from NHL API
	seasons, err := a.NHLClient.SeasonStandingManifest(ctx)
	if err != nil {
		if len(staleData) > 0 {
			staleResponse, unmarshalErr := resource.SeasonsManifest{}.Parse(staleData)
			if unmarshalErr == nil {
				log.Warn().Err(err).Int("count", len(staleResponse.Seasons)).Msg("API fetch failed, using stale seasons manifest")
				metrics.IncDownload(core.SeasonsManifest, metrics.ResultStaleFallback)
				cacheInRedis(ctx, a.RedisClient, staleData)
				return FetchSeasonsManifestResult{
					Origin:  core.OriginFileSystem,
					Seasons: filterSeasons(staleResponse.Seasons, input),
				}, nil
			}
		}
		metrics.IncDownload(core.SeasonsManifest, metrics.ResultError)
		return FetchSeasonsManifestResult{}, err
	}

	// API success - persist to both caches
	data, _ := manifestRes.Format(nhl.SeasonsResponse{Seasons: seasons})
	if err := a.Storage.Write(manifestRes.Path(), data); err != nil {
		log.Warn().Err(err).Msg("Failed to write seasons manifest to filesystem")
	}
	cacheInRedis(ctx, a.RedisClient, data)
	log.Info().Int("count", len(seasons)).Msg("Seasons manifest downloaded from API")
	metrics.IncDownload(core.SeasonsManifest, metrics.ResultAPIFetch)
	return FetchSeasonsManifestResult{
		Origin:  core.OriginRemoteNHLAPI,
		Seasons: filterSeasons(seasons, input),
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

// cacheInRedis stores the seasons manifest in Redis with TTL.
func cacheInRedis(ctx context.Context, client cache.Client, data []byte) {
	if err := client.Set(ctx, redisSeasonsManifestKey, data, config.DefaultSeasonsManifestCacheTTL).Err(); err != nil {
		log.Warn().Err(err).Msg("Failed to cache seasons manifest in Redis")
	}
}

