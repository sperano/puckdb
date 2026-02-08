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
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// DownloadSeasonsManifestResult contains statistics from the seasons manifest download.
type DownloadSeasonsManifestResult struct {
	Count     int  // Number of seasons in manifest
	FromCache bool // Whether data came from cache
}

// DownloadSeasonsManifestActivity downloads the NHL seasons manifest.
// Uses SimpleFS cache; skips download if already cached.
func DownloadSeasonsManifestActivity(ctx context.Context) (DownloadSeasonsManifestResult, error) {
	fs := cache.NewSimpleCache()
	client := newNHLClient()
	return downloadSeasonsManifestImpl(ctx, fs, client)
}

func downloadSeasonsManifestImpl(
	ctx context.Context,
	fs cache.FileSystem,
	client NHLClient,
) (DownloadSeasonsManifestResult, error) {
	file := cache.SeasonsManifestFile{}

	// Check cache first
	if fs.Exists(file) {
		data, err := fs.Read(file)
		if err == nil {
			var seasons []nhl.SeasonInfo
			if err := json.Unmarshal(data, &seasons); err == nil {
				log.Debug().Int("count", len(seasons)).Msg("Seasons manifest loaded from cache")
				metrics.IncDownload(cache.FileTypeSeasonsManifest, "hit")
				return DownloadSeasonsManifestResult{Count: len(seasons), FromCache: true}, nil
			}
			log.Debug().Err(err).Msg("Failed to unmarshal cached seasons manifest")
		}
	}

	// Fetch from API
	seasons, err := client.SeasonStandingManifest(ctx)
	if err != nil {
		metrics.IncDownload(cache.FileTypeSeasonsManifest, "error")
		return DownloadSeasonsManifestResult{}, err
	}

	// Save to cache
	data, err := json.Marshal(seasons)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to marshal seasons manifest for cache")
	} else {
		if err := fs.Write(file, data); err != nil {
			log.Warn().Err(err).Msg("Failed to write seasons manifest to cache")
		}
	}

	log.Info().Int("count", len(seasons)).Msg("Seasons manifest downloaded from API")
	metrics.IncDownload(cache.FileTypeSeasonsManifest, "miss")

	return DownloadSeasonsManifestResult{Count: len(seasons), FromCache: false}, nil
}

// DownloadSeasonStandingsResult contains statistics from downloading standings for a season.
type DownloadSeasonStandingsResult struct {
	SeasonID  int  // Season ID that was processed
	TeamCount int  // Number of teams in standings
	FromCache bool // Whether data came from cache
}

// DownloadSeasonStandingsActivity downloads standings for a specific season.
func DownloadSeasonStandingsActivity(ctx context.Context, seasonID int) (DownloadSeasonStandingsResult, error) {
	fs := cache.NewSimpleCache()
	client := newNHLClient()
	return downloadSeasonStandingsImpl(ctx, fs, client, seasonID)
}

func downloadSeasonStandingsImpl(
	ctx context.Context,
	fs cache.FileSystem,
	client NHLClient,
	seasonID int,
) (DownloadSeasonStandingsResult, error) {
	file := cache.SeasonStandingsFile{SeasonID: seasonID}

	// Check cache first
	if fs.Exists(file) {
		data, err := fs.Read(file)
		if err == nil {
			var standings []nhl.Standing
			if err := json.Unmarshal(data, &standings); err == nil {
				log.Debug().Int("season", seasonID).Int("teams", len(standings)).Msg("Season standings loaded from cache")
				metrics.IncDownload(cache.FileTypeSeasonStandings, "hit")
				return DownloadSeasonStandingsResult{SeasonID: seasonID, TeamCount: len(standings), FromCache: true}, nil
			}
			log.Debug().Err(err).Int("season", seasonID).Msg("Failed to unmarshal cached season standings")
		}
	}

	// Fetch from API
	season := nhl.NewSeason(seasonID)
	standings, err := client.LeagueStandingsForSeason(ctx, season)
	if err != nil {
		metrics.IncDownload(cache.FileTypeSeasonStandings, "error")
		return DownloadSeasonStandingsResult{SeasonID: seasonID}, err
	}

	// Save to cache
	data, err := json.Marshal(standings)
	if err != nil {
		log.Warn().Err(err).Int("season", seasonID).Msg("Failed to marshal season standings for cache")
	} else {
		if err := fs.Write(file, data); err != nil {
			log.Warn().Err(err).Int("season", seasonID).Msg("Failed to write season standings to cache")
		}
	}

	log.Info().Int("season", seasonID).Int("teams", len(standings)).Msg("Season standings downloaded from API")
	metrics.IncDownload(cache.FileTypeSeasonStandings, "miss")

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
	fs := cache.NewSimpleCache()
	file := cache.SeasonsManifestFile{}

	data, err := fs.Read(file)
	if err != nil {
		return UpsertSeasonsResult{}, fmt.Errorf("read seasons manifest from cache: %w", err)
	}

	var seasons []nhl.SeasonInfo
	if err := json.Unmarshal(data, &seasons); err != nil {
		return UpsertSeasonsResult{}, fmt.Errorf("unmarshal seasons manifest: %w", err)
	}

	// Open database connection
	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return UpsertSeasonsResult{}, fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()

	queries := sqlcdb.New(pool)

	return upsertSeasonsImpl(ctx, queries, seasons, logger)
}

type seasonsUpserter interface {
	UpsertNHLSeason(ctx context.Context, arg sqlcdb.UpsertNHLSeasonParams) error
}

func upsertSeasonsImpl(
	ctx context.Context,
	queries seasonsUpserter,
	seasons []nhl.SeasonInfo,
	logger activityLogger,
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

		params := sqlcdb.UpsertNHLSeasonParams{
			ID:             int32(s.ID.ToInt()),
			StandingsStart: pgtype.Date{Time: startDate, Valid: true},
			StandingsEnd:   pgtype.Date{Time: endDate, Valid: true},
		}

		if err := queries.UpsertNHLSeason(ctx, params); err != nil {
			return result, fmt.Errorf("upsert season %d: %w", s.ID.ToInt(), err)
		}

		result.SeasonsUpserted++
	}

	logger.Info("Seasons upserted", "count", result.SeasonsUpserted)
	log.Info().Int("count", result.SeasonsUpserted).Msg("Seasons upserted to database")

	return result, nil
}

// UpsertSeasonTeamsResult contains results from upserting season teams to database.
type UpsertSeasonTeamsResult struct {
	SeasonID    int `json:"seasonId"`
	TeamsUpserted int `json:"teamsUpserted"`
}

// UpsertSeasonTeamsActivity reads standings for a season from cache and upserts teams to database.
func UpsertSeasonTeamsActivity(ctx context.Context, seasonID int) (UpsertSeasonTeamsResult, error) {
	logger := activity.GetLogger(ctx)

	// Read standings from cache
	fs := cache.NewSimpleCache()
	file := cache.SeasonStandingsFile{SeasonID: seasonID}

	data, err := fs.Read(file)
	if err != nil {
		return UpsertSeasonTeamsResult{SeasonID: seasonID}, fmt.Errorf("read season standings from cache: %w", err)
	}

	var standings []nhl.Standing
	if err := json.Unmarshal(data, &standings); err != nil {
		return UpsertSeasonTeamsResult{SeasonID: seasonID}, fmt.Errorf("unmarshal season standings: %w", err)
	}

	// Open database connection
	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return UpsertSeasonTeamsResult{SeasonID: seasonID}, fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()

	queries := sqlcdb.New(pool)

	return upsertSeasonTeamsImpl(ctx, queries, seasonID, standings, logger)
}

type seasonTeamsUpserter interface {
	UpsertNHLSeasonTeam(ctx context.Context, arg sqlcdb.UpsertNHLSeasonTeamParams) error
}

func upsertSeasonTeamsImpl(
	ctx context.Context,
	queries seasonTeamsUpserter,
	seasonID int,
	standings []nhl.Standing,
	logger activityLogger,
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

		params := sqlcdb.UpsertNHLSeasonTeamParams{
			SeasonID:        int32(seasonID),
			TeamID:          0, // Will need to map from abbrev to ID
			FranchiseID:     pgtype.Int8{Valid: false}, // Will link later
			FullName:        s.TeamName.String(),
			Abbrev:          s.TeamAbbrev.String(),
			LogoUrl:         pgtype.Text{String: s.TeamLogo, Valid: s.TeamLogo != ""},
			DivisionName:    s.DivisionName,
			DivisionAbbrev:  s.DivisionAbbrev,
			ConferenceName:  confName,
			ConferenceAbbrev: confAbbrev,
		}

		if err := queries.UpsertNHLSeasonTeam(ctx, params); err != nil {
			return result, fmt.Errorf("upsert season team %s for season %d: %w", s.TeamAbbrev.String(), seasonID, err)
		}

		result.TeamsUpserted++
	}

	logger.Info("Season teams upserted", "season", seasonID, "count", result.TeamsUpserted)
	log.Info().Int("season", seasonID).Int("count", result.TeamsUpserted).Msg("Season teams upserted to database")

	return result, nil
}
