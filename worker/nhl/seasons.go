package nhl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/go-redis/redis/v8"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/matching"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/worker/shared"
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

// SeasonRosterUpserter is the interface for season roster database operations.
type SeasonRosterUpserter interface {
	GetSeasonTeamAbbrevs(ctx context.Context, seasonID int32) ([]sqlcdb.GetSeasonTeamAbbrevsRow, error)
	EnsurePlayerExistsBatch(ctx context.Context, arg []sqlcdb.EnsurePlayerExistsBatchParams) *sqlcdb.EnsurePlayerExistsBatchBatchResults
	UpsertSeasonRosterBatch(ctx context.Context, arg []sqlcdb.UpsertSeasonRosterBatchParams) *sqlcdb.UpsertSeasonRosterBatchBatchResults
}

// ClubStatsUpserter is the interface for club stats database operations.
type ClubStatsUpserter interface {
	GetSeasonTeamAbbrevs(ctx context.Context, seasonID int32) ([]sqlcdb.GetSeasonTeamAbbrevsRow, error)
	UpsertClubSkaterStatsBatch(ctx context.Context, arg []sqlcdb.UpsertClubSkaterStatsBatchParams) *sqlcdb.UpsertClubSkaterStatsBatchBatchResults
	UpsertClubGoalieStatsBatch(ctx context.Context, arg []sqlcdb.UpsertClubGoalieStatsBatchParams) *sqlcdb.UpsertClubGoalieStatsBatchBatchResults
}

// SeasonsActivities holds dependencies for season management activities:
// fetching season manifests, upserting seasons/teams, rosters, and club stats.
type SeasonsActivities struct {
	Storage             store.Storage
	GobCache            *cache.GobCache
	NHLClient           shared.NHLClient
	SeasonsUpserter     seasonsUpserter
	SeasonTeamsUpserter seasonTeamsUpserter
	RosterQueries       SeasonRosterUpserter
	ClubStatsQueries    ClubStatsUpserter
	EdgeQueries         EdgeStatsUpserter
	RedisClient         *redis.Client // for progress tracking (nil-safe)
}

// --- FetchSeasonsManifest ---

// FetchSeasonsManifestResult contains the result of fetching seasons data.
type FetchSeasonsManifestResult struct {
	Seasons []nhlapi.SeasonInfo
	Origin  core.DataOrigin
}

// FetchSeasonsManifest fetches season data and filters by input range.
// Uses a 3-layer cache: Redis → filesystem (with staleness check) → NHL API.
// If the API fails but stale filesystem data exists, falls back to stale data.
func (a *SeasonsActivities) FetchSeasonsManifest(ctx context.Context, input *model.SeasonsInput) (FetchSeasonsManifestResult, error) {
	// Layer 1: Redis gob cache (fastest, short-lived)
	if response, ok, _ := cache.Get[nhlapi.SeasonsResponse](a.GobCache, ctx, redisSeasonsManifestKey); ok {
		log.Debug().Int("count", len(response.Seasons)).Msg("Seasons manifest loaded from Redis")
		metrics.IncDownload(core.SeasonsManifest, metrics.ResultRedisHit)
		return FetchSeasonsManifestResult{
			Origin:  core.OriginRedis,
			Seasons: filterSeasons(response.Seasons, input),
		}, nil
	}

	// Layer 2: Filesystem cache with staleness check
	var staleData []byte
	manifestRes := resource.SeasonsManifest{}
	if a.Storage.Exists(ctx, manifestRes.Path()) {
		data, err := a.Storage.Read(ctx, manifestRes.Path())
		if err == nil {
			info, statErr := a.Storage.Stat(ctx, manifestRes.Path())
			isStale := (statErr != nil && !os.IsNotExist(statErr)) ||
				(statErr == nil && time.Since(info.ModTime()) > config.DefaultSeasonsManifestStaleTTL)

			if !isStale {
				response, err := resource.SeasonsManifest{}.Parse(data)
				if err != nil {
					return FetchSeasonsManifestResult{}, err
				}
				metrics.IncDownload(core.SeasonsManifest, metrics.ResultFSHit)
				gobCacheSeasons(ctx, a.GobCache, response)
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
				gobCacheSeasons(ctx, a.GobCache, staleResponse)
				return FetchSeasonsManifestResult{
					Origin:  core.OriginFileSystem,
					Seasons: filterSeasons(staleResponse.Seasons, input),
				}, nil
			}
		}
		metrics.IncDownload(core.SeasonsManifest, metrics.ResultError)
		return FetchSeasonsManifestResult{}, err
	}

	// API success - persist to both caches. Filesystem persistence is
	// best-effort: if Format or Write fails we keep going since the caller
	// already has the data in memory. Skip the Write on a Format error
	// instead of persisting empty bytes that would corrupt the cache and
	// break the next Parse.
	response := nhlapi.SeasonsResponse{Seasons: seasons}
	data, err := manifestRes.Format(response)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to format seasons manifest, skipping filesystem write")
	} else if writeErr := a.Storage.Write(ctx, manifestRes.Path(), data); writeErr != nil {
		log.Warn().Err(writeErr).Msg("Failed to write seasons manifest to filesystem")
	}
	gobCacheSeasons(ctx, a.GobCache, response)
	log.Info().Int("count", len(seasons)).Msg("Seasons manifest downloaded from API")
	metrics.IncDownload(core.SeasonsManifest, metrics.ResultAPIFetch)
	return FetchSeasonsManifestResult{
		Origin:  core.OriginRemoteNHLAPI,
		Seasons: filterSeasons(seasons, input),
	}, nil
}

// filterSeasons filters nhlapi.SeasonInfo by input range.
func filterSeasons(seasons []nhlapi.SeasonInfo, input *model.SeasonsInput) []nhlapi.SeasonInfo {
	if input == nil {
		return seasons
	}
	var result []nhlapi.SeasonInfo
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
			ID:             int32(s.ID.ID()),
			StandingsStart: pgtype.Date{Time: s.StandingsStart.Time, Valid: true},
			StandingsEnd:   pgtype.Date{Time: s.StandingsEnd.Time, Valid: true},
		}
		if err := a.SeasonsUpserter.UpsertSeason(ctx, params); err != nil {
			return result, fmt.Errorf("upsert season %d: %w", s.ID.ID(), err)
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
	Season    nhlapi.Season
	TeamCount int
	FromCache bool
}

// UpsertSeasonTeamsResult contains results from upserting season teams to database.
type UpsertSeasonTeamsResult struct {
	Season        nhlapi.Season `json:"seasonId"`
	TeamsUpserted int           `json:"teamsUpserted"`
}

// InitializeSeasonTeamsResult contains the combined result of downloading standings and upserting teams for a season.
type InitializeSeasonTeamsResult struct {
	Season         nhlapi.Season                 `json:"season"`
	DownloadResult DownloadSeasonStandingsResult `json:"downloadResult"`
	UpsertResult   UpsertSeasonTeamsResult       `json:"upsertResult"`
}

// InitializeSeasonTeamsActivity downloads standings and upserts teams for a single season.
// seasonID is the full season identifier (e.g., 20242025), not the start season.
func (a *SeasonsActivities) InitializeSeasonTeamsActivity(ctx context.Context, season nhlapi.Season) (InitializeSeasonTeamsResult, error) {
	result := InitializeSeasonTeamsResult{Season: season}

	downloadResult, err := a.downloadSeasonStandings(ctx, season)
	if err != nil {
		return result, fmt.Errorf("download season standings: %w", err)
	}
	result.DownloadResult = downloadResult

	upsertResult, err := a.upsertSeasonTeams(ctx, season)
	if err != nil {
		return result, fmt.Errorf("upsert season teams: %w", err)
	}
	result.UpsertResult = upsertResult

	return result, nil
}

// downloadSeasonStandings downloads league standings for a single season.
func (a *SeasonsActivities) downloadSeasonStandings(ctx context.Context, season nhlapi.Season) (DownloadSeasonStandingsResult, error) {
	standings, origin, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, resource.SeasonStandings{Season: season},
		func(ctx context.Context) ([]nhlapi.Standing, error) {
			return a.NHLClient.LeagueStandingsForSeason(ctx, season)
		},
	)
	if err != nil {
		return DownloadSeasonStandingsResult{Season: season}, err
	}

	fromCache := origin != core.OriginRemoteNHLAPI
	if fromCache {
		log.Debug().Int("season", season.ID()).Int("teams", len(standings)).Str("origin", origin.String()).Msg("Season standings loaded from cache")
	} else {
		log.Info().Int("season", season.ID()).Int("teams", len(standings)).Msg("Season standings downloaded from API")
	}
	return DownloadSeasonStandingsResult{Season: season, TeamCount: len(standings), FromCache: fromCache}, nil
}

// upsertSeasonTeams reads standings from cache and upserts teams to database.
func (a *SeasonsActivities) upsertSeasonTeams(ctx context.Context, season nhlapi.Season) (UpsertSeasonTeamsResult, error) {
	result := UpsertSeasonTeamsResult{Season: season}

	standingsRes := resource.SeasonStandings{Season: season}
	standings, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, standingsRes)
	if err != nil {
		return result, fmt.Errorf("read season standings from cache: %w", err)
	}

	for _, s := range standings {
		teamID, err := matching.LookupTeamID(s.TeamAbbrev.String())
		if err != nil {
			return result, fmt.Errorf("season %d team %s: %w", season.ID(), s.TeamAbbrev.String(), err)
		}
		params := sqlcdb.UpsertSeasonTeamParams{
			Season:           int32(season.ID()),
			TeamID:           teamID,
			FranchiseID:      pgtype.Int8{Valid: false},
			FullName:         s.TeamName.String(),
			Abbrev:           s.TeamAbbrev.String(),
			LogoUrl:          pgtype.Text{String: s.TeamLogo, Valid: s.TeamLogo != ""},
			DivisionName:     pgtype.Text{String: s.DivisionName, Valid: true},
			DivisionAbbrev:   pgtype.Text{String: s.DivisionAbbrev, Valid: true},
			ConferenceName:   shared.PtrToText(s.ConferenceName),
			ConferenceAbbrev: shared.PtrToText(s.ConferenceAbbrev),
		}

		if err := a.SeasonTeamsUpserter.UpsertSeasonTeam(ctx, params); err != nil {
			return result, fmt.Errorf("upsert season team %s for season %d: %w", s.TeamAbbrev.String(), season.ID(), err)
		}
		result.TeamsUpserted++
	}

	return result, nil
}

// gobCacheSeasons stores the seasons manifest in Redis using gob encoding.
func gobCacheSeasons(ctx context.Context, gobCache *cache.GobCache, response nhlapi.SeasonsResponse) {
	if err := cache.Set(gobCache, ctx, redisSeasonsManifestKey, response); err != nil && !errors.Is(err, cache.ErrNilCache) {
		log.Warn().Err(err).Msg("Failed to cache seasons manifest in Redis")
	}
}
