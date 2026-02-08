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
	"github.com/sperano/puckdb/redis"
	"github.com/sperano/puckdb/sqlcdb"
)

const (
	yahooPlayerBaseURL = "https://sports.yahoo.com/nhl/players/"
)

// PlayerUpserter is the interface for database operations needed by player import.
type PlayerUpserter interface {
	UpsertPlayer(ctx context.Context, arg sqlcdb.UpsertPlayerParams) error
	ClearConflictingYahooID(ctx context.Context, arg sqlcdb.ClearConflictingYahooIDParams) error
}

// ImportBatchResult holds the results of importing a batch of players.
type ImportBatchResult struct {
	Imported int      // Total players imported
	Matched  int      // Players matched with Yahoo IDs
	Errors   []string // Error messages for failed players
}

// ImportPlayerBatchActivity imports a batch of players from cached PlayerLanding files.
// It reads PlayerLanding JSON, matches with Yahoo IDs, and upserts to the database.
func ImportPlayerBatchActivity(ctx context.Context, playerIDs []int64) (ImportBatchResult, error) {
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()

	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return ImportBatchResult{}, err
	}
	defer pool.Close()

	deps := ImportDeps{
		FS:      cache.NewSimpleCache(),
		Redis:   redisClient,
		Queries: database.NewQueries(pool),
	}

	return importPlayerBatchImpl(ctx, deps, playerIDs)
}

// ImportDeps holds dependencies for player import.
type ImportDeps struct {
	FS      cache.FileSystem
	Redis   redis.Client
	Queries PlayerUpserter
}

func importPlayerBatchImpl(ctx context.Context, deps ImportDeps, playerIDs []int64) (ImportBatchResult, error) {
	if len(playerIDs) == 0 {
		return ImportBatchResult{}, nil
	}

	// Load YahooID pool from Redis
	yahooPool, err := LoadYahooIDPool(ctx, deps.Redis)
	if err != nil {
		return ImportBatchResult{}, fmt.Errorf("load yahoo pool: %w", err)
	}

	var result ImportBatchResult
	var matchedYahooIDs []int

	for _, playerID := range playerIDs {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		// Read PlayerLanding from cache
		file := cache.PlayerLandingFile{PlayerID: nhl.PlayerID(playerID)}
		if !deps.FS.Exists(file) {
			result.Errors = append(result.Errors, fmt.Sprintf("player %d: file not found", playerID))
			continue
		}

		content, err := deps.FS.Read(file)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("player %d: read error: %v", playerID, err))
			continue
		}

		var landing nhl.PlayerLanding
		if err := json.Unmarshal(content, &landing); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("player %d: parse error: %v", playerID, err))
			continue
		}

		// Try to match with Yahoo player
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
				Int64("nhl_id", playerID).
				Str("name", landing.FirstName.Default+" "+landing.LastName.Default).
				Msg("Yahoo ID matching warning")
		}

		// Build UpsertPlayerParams
		params := buildUpsertParams(&landing, matchResult)

		// Clear any conflicting yahoo_id assignment before upserting.
		// This handles cases where a yahoo_id was previously assigned to the wrong player
		// (e.g., Bryan Hextall Sr. vs Jr. with the same name).
		if matchResult.Matched {
			clearParams := sqlcdb.ClearConflictingYahooIDParams{
				YahooID: pgtype.Int8{Int64: int64(matchResult.YahooID), Valid: true},
				ID:      playerID,
			}
			if err := deps.Queries.ClearConflictingYahooID(ctx, clearParams); err != nil {
				log.Warn().Err(err).
					Int64("nhl_id", playerID).
					Int("yahoo_id", matchResult.YahooID).
					Msg("Failed to clear conflicting yahoo_id")
			}
		}

		// Upsert to database
		if err := deps.Queries.UpsertPlayer(ctx, params); err != nil {
			errMsg := fmt.Sprintf("player %d (%s %s): upsert error: %v",
				playerID, landing.FirstName.Default, landing.LastName.Default, err)
			if matchResult.Matched {
				errMsg = fmt.Sprintf("player %d (%s %s, yahoo_id=%d): upsert error: %v",
					playerID, landing.FirstName.Default, landing.LastName.Default, matchResult.YahooID, err)
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
		if err := RemoveFromYahooIDPool(ctx, deps.Redis, matchedYahooIDs); err != nil {
			log.Warn().Err(err).Int("count", len(matchedYahooIDs)).Msg("Failed to remove matched Yahoo IDs")
		}
	}

	log.Info().
		Int("batch_size", len(playerIDs)).
		Int("imported", result.Imported).
		Int("matched", result.Matched).
		Int("errors", len(result.Errors)).
		Msg("Import batch complete")

	return result, nil
}

// buildUpsertParams creates UpsertPlayerParams from PlayerLanding and match result.
func buildUpsertParams(landing *nhl.PlayerLanding, match YahooIDMatchResult) sqlcdb.UpsertPlayerParams {
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
