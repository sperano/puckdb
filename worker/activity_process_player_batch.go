package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/sqlcdb"
)

const yahooPlayerBaseURL = "https://sports.yahoo.com/nhl/players/"

// PlayerUpserter is the interface for database operations needed by player import.
type PlayerUpserter interface {
	UpsertPlayer(ctx context.Context, arg sqlcdb.UpsertPlayerParams) error
	ClearConflictingYahooID(ctx context.Context, arg sqlcdb.ClearConflictingYahooIDParams) error
}

// ProcessPlayerBatchResult contains combined download and import statistics.
type ProcessPlayerBatchResult struct {
	// Download stats
	Downloaded int // Players downloaded from API
	CacheHits  int // Players found in cache
	Missing    int // 404 responses (cached for future runs)

	// Import stats
	Imported int      // Players imported to database
	Matched  int      // Players matched with Yahoo IDs
	Errors   []string // Error messages for failed players
}

// ProcessPlayerBatchActivity downloads player landing pages (if needed) and imports them to the database.
// This combines DownloadPlayerLandingBatchActivity and ImportPlayerBatchActivity into a single pass.
func ProcessPlayerBatchActivity(ctx context.Context, players []BoxscorePlayer) (ProcessPlayerBatchResult, error) {
	fs := store.NewStore()
	nhlClient := newNHLClient()

	redisClient := cache.NewClient()
	defer func() { _ = redisClient.Close() }()

	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return ProcessPlayerBatchResult{}, err
	}
	defer pool.Close()

	deps := processDeps{
		fs:        fs,
		nhlClient: nhlClient,
		redis:     redisClient,
		queries:   database.NewQueries(pool),
	}

	return processPlayerBatchImpl(ctx, deps, players)
}

// processDeps holds dependencies for the combined process activity.
type processDeps struct {
	fs        store.Store
	nhlClient NHLClient
	redis     cache.Client
	queries   PlayerUpserter
}

func processPlayerBatchImpl(ctx context.Context, deps processDeps, players []BoxscorePlayer) (ProcessPlayerBatchResult, error) {
	result := ProcessPlayerBatchResult{}

	if len(players) == 0 {
		return result, nil
	}

	// Load YahooID pool from Redis once for the batch
	yahooPool, err := LoadYahooIDPool(ctx, deps.redis)
	if err != nil {
		return result, fmt.Errorf("load yahoo pool: %w", err)
	}

	var matchedYahooIDs []store.YahooPlayerID

	for _, p := range players {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		playerID := nhl.PlayerID(p.ID)

		// Step 1: Download player landing (if not cached)
		downloadStatus, err := ensurePlayerLandingCached(ctx, deps.fs, deps.nhlClient, playerID, &p)
		if err != nil {
			log.Error().Err(err).Int64("player_id", p.ID).Msg("Failed to download player landing")
			return result, err
		}

		switch downloadStatus {
		case playerLandingCached:
			result.CacheHits++
			metrics.IncDownload("PlayerLanding", "hit")
		case playerLandingDownloaded:
			result.Downloaded++
			metrics.IncDownload("PlayerLanding", "miss")
		case playerLandingMissing:
			result.Missing++
			metrics.IncDownload("PlayerLanding", "missing")
			// Import missing player with minimal info from boxscore data
			if err := importMissingPlayer(ctx, deps, &p); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("player %d (missing): import error: %v", p.ID, err))
			} else {
				result.Imported++
			}
			continue
		}

		// Step 2: Import player to database
		file := store.PlayerLandingFile{PlayerID: playerID}
		if !deps.fs.Exists(file) {
			// This shouldn't happen after successful download, but handle gracefully
			result.Errors = append(result.Errors, fmt.Sprintf("player %d: file not found after download", p.ID))
			continue
		}

		content, err := deps.fs.Read(file)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("player %d: read error: %v", p.ID, err))
			continue
		}

		var landing nhl.PlayerLanding
		if err := json.Unmarshal(content, &landing); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("player %d: parse error: %v", p.ID, err))
			continue
		}

		// Match with Yahoo player
		var teamAbbrev string
		if landing.CurrentTeamAbbrev != nil {
			teamAbbrev = *landing.CurrentTeamAbbrev
		}
		var nhlBirthDate time.Time
		if landing.BirthDate != "" {
			nhlBirthDate, _ = time.Parse("2006-01-02", landing.BirthDate)
		}
		matchResult, err := MatchYahooID(&landing, teamAbbrev, nhlBirthDate, yahooPool)
		if err != nil {
			log.Debug().
				Err(err).
				Int64("nhl_id", p.ID).
				Str("name", landing.FirstName.Default+" "+landing.LastName.Default).
				Msg("Yahoo ID matching warning")
		}

		// Build UpsertPlayerParams
		params := buildProcessUpsertParams(&landing, matchResult)

		// Clear any conflicting yahoo_id assignment before upserting
		if matchResult.Matched {
			clearParams := sqlcdb.ClearConflictingYahooIDParams{
				YahooID: pgtype.Int8{Int64: int64(matchResult.YahooID), Valid: true},
				ID:      p.ID,
			}
			if err := deps.queries.ClearConflictingYahooID(ctx, clearParams); err != nil {
				log.Warn().Err(err).
					Int64("nhl_id", p.ID).
					Int("yahoo_id", int(matchResult.YahooID)).
					Msg("Failed to clear conflicting yahoo_id")
			}
		}

		// Upsert to database
		if err := deps.queries.UpsertPlayer(ctx, params); err != nil {
			errMsg := fmt.Sprintf("player %d (%s %s): upsert error: %v",
				p.ID, landing.FirstName.Default, landing.LastName.Default, err)
			if matchResult.Matched {
				errMsg = fmt.Sprintf("player %d (%s %s, yahoo_id=%d): upsert error: %v",
					p.ID, landing.FirstName.Default, landing.LastName.Default, matchResult.YahooID, err)
			}
			result.Errors = append(result.Errors, errMsg)
			continue
		}

		result.Imported++
		if matchResult.Matched {
			result.Matched++
			matchedYahooIDs = append(matchedYahooIDs, matchResult.YahooID)
		}
	}

	// Remove matched Yahoo IDs from available set
	if len(matchedYahooIDs) > 0 {
		if err := RemoveFromYahooIDPool(ctx, deps.redis, matchedYahooIDs); err != nil {
			log.Warn().Err(err).Int("count", len(matchedYahooIDs)).Msg("Failed to remove matched Yahoo IDs")
		}
	}

	log.Info().
		Int("batch_size", len(players)).
		Int("downloaded", result.Downloaded).
		Int("cache_hits", result.CacheHits).
		Int("missing", result.Missing).
		Int("imported", result.Imported).
		Int("matched", result.Matched).
		Int("errors", len(result.Errors)).
		Msg("Process player batch complete")

	return result, nil
}

// buildProcessUpsertParams creates UpsertPlayerParams from PlayerLanding and match result.
// This is a copy of buildUpsertParams to avoid circular dependencies.
func buildProcessUpsertParams(landing *nhl.PlayerLanding, match YahooIDMatchResult) sqlcdb.UpsertPlayerParams {
	// Trim whitespace from name fields - NHL API sometimes has trailing spaces
	firstName := strings.TrimSpace(landing.FirstName.Default)
	lastName := strings.TrimSpace(landing.LastName.Default)

	params := sqlcdb.UpsertPlayerParams{
		ID:                  landing.PlayerID.AsInt64(),
		FirstName:           firstName,
		LastName:            lastName,
		FirstNameNormalized: normalizeName(firstName),
		LastNameNormalized:  normalizeName(lastName),
		Position:            string(landing.Position),
		ShootsCatches:       string(landing.ShootsCatches),
		HeightInches:        pgtype.Int4{Int32: int32(landing.HeightInInches), Valid: landing.HeightInInches > 0},
		WeightPounds:        pgtype.Int4{Int32: int32(landing.WeightInPounds), Valid: landing.WeightInPounds > 0},
		IsActive:            landing.IsActive,
		HeadshotURL:         strings.TrimSpace(landing.Headshot),
	}

	// Yahoo ID and URLs
	if match.Matched {
		params.YahooID = pgtype.Int8{Int64: int64(match.YahooID), Valid: true}
		params.YahooHomeURL = fmt.Sprintf("%s%d/", yahooPlayerBaseURL, match.YahooID)
	}

	// Team ID
	if landing.CurrentTeamID != nil {
		params.TeamID = pgtype.Int8{Int64: int64(*landing.CurrentTeamID), Valid: true}
	}

	// Jersey number
	if landing.SweaterNumber != nil && *landing.SweaterNumber > 0 {
		params.SweaterNumber = pgtype.Int4{Int32: int32(*landing.SweaterNumber), Valid: true}
	}

	// Birth date
	if landing.BirthDate != "" {
		if t, err := time.Parse("2006-01-02", landing.BirthDate); err == nil {
			params.BirthDate = pgtype.Date{Time: t, Valid: true}
		}
	}

	// Birth location
	if landing.BirthCity != nil {
		params.BirthCity = pgtype.Text{String: strings.TrimSpace(landing.BirthCity.Default), Valid: true}
	}
	if landing.BirthStateProvince != nil {
		params.BirthStateProvince = pgtype.Text{String: strings.TrimSpace(landing.BirthStateProvince.Default), Valid: true}
	}
	if landing.BirthCountry != nil {
		params.BirthCountry = pgtype.Text{String: strings.TrimSpace(*landing.BirthCountry), Valid: true}
	}

	// Hero image
	if landing.HeroImage != nil {
		params.HeroImageURL = pgtype.Text{String: strings.TrimSpace(*landing.HeroImage), Valid: true}
	}

	// Player slug
	if landing.PlayerSlug != nil {
		params.PlayerSlug = pgtype.Text{String: strings.TrimSpace(*landing.PlayerSlug), Valid: true}
	}

	// Draft details
	if landing.DraftDetails != nil {
		params.DraftYear = pgtype.Int4{Int32: int32(landing.DraftDetails.Year), Valid: true}
		params.DraftTeamAbbrev = pgtype.Text{String: strings.TrimSpace(landing.DraftDetails.TeamAbbrev), Valid: true}
		params.DraftRound = pgtype.Int4{Int32: int32(landing.DraftDetails.Round), Valid: true}
		params.DraftPickInRound = pgtype.Int4{Int32: int32(landing.DraftDetails.PickInRound), Valid: true}
		params.DraftOverallPick = pgtype.Int4{Int32: int32(landing.DraftDetails.OverallPick), Valid: true}
	}

	return params
}

// importMissingPlayer imports a player with minimal info from boxscore data.
// These are players who returned 404 from the NHL API but appear in boxscores.
func importMissingPlayer(ctx context.Context, deps processDeps, p *BoxscorePlayer) error {
	firstName := strings.TrimSpace(p.FirstName)
	lastName := strings.TrimSpace(p.LastName)
	position := strings.TrimSpace(p.Position)

	params := sqlcdb.UpsertPlayerParams{
		ID:                  p.ID,
		FirstName:           firstName,
		LastName:            lastName,
		FirstNameNormalized: normalizeName(firstName),
		LastNameNormalized:  normalizeName(lastName),
		Position:            position,
		IsActive:            false, // Assume inactive since they returned 404
	}

	log.Debug().
		Int64("player_id", p.ID).
		Str("name", firstName+" "+lastName).
		Str("position", position).
		Msg("Importing missing player with minimal info")

	return deps.queries.UpsertPlayer(ctx, params)
}
