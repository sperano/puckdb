package worker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/matching"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
)

const yahooPlayerBaseURL = "https://sports.yahoo.com/nhl/players/"

// PlayerUpserter is the interface for database operations needed by player import.
type PlayerUpserter interface {
	UpsertPlayer(ctx context.Context, arg sqlcdb.UpsertPlayerParams) error
	ClearConflictingYahooID(ctx context.Context, arg sqlcdb.ClearConflictingYahooIDParams) error
}

// ProcessPlayerBatchResult contains combined download and import statistics.
type ProcessPlayerBatchResult struct {
	FetchStats // Download stats (Downloaded, CacheHits, Missing)

	// Import stats
	Imported       int               // Players imported to database
	Matched        int               // Players matched with Yahoo IDs
	Origins        core.OriginCounts // Tracks where player landings were read from
	AwardsImported int               // Player award rows upserted
	TotalsImported int               // Player season total rows upserted
	Errors         []string          // Error messages for failed players
}

// ProcessPlayerBatch downloads player landing pages (if needed) and imports them to the database.
// This combines DownloadPlayerLandingBatchActivity and ImportPlayerBatchActivity into a single pass.
func (a *PlayerActivities) ProcessPlayerBatch(ctx context.Context, players []store.BoxscorePlayer) (ProcessPlayerBatchResult, error) {
	result := ProcessPlayerBatchResult{
		Origins: core.OriginCounts{},
	}

	if len(players) == 0 {
		return result, nil
	}

	// Load YahooID pool from Redis once for the batch
	yahooPool, err := LoadYahooIDPool(ctx, a.RedisClient)
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

		// Step 1: Ensure player landing is in storage (download from API if needed)
		downloadStatus, err := a.ensurePlayerLandingCached(ctx, playerID, p)
		if err != nil {
			log.Error().Err(err).Int64("player_id", p.ID).Msg("Failed to download player landing")
			return result, err
		}

		switch downloadStatus {
		case playerLandingDownloaded:
			result.Downloaded++
			result.Origins.Record(core.OriginRemoteNHLAPI)
			metrics.LegacyIncDownload("PlayerLanding", metrics.ResultMiss)
		case playerLandingMissing:
			result.Missing++
			metrics.LegacyIncDownload("PlayerLanding", metrics.ResultMissing)
			if err := a.importMissingPlayer(ctx, p); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("player %d (missing): import error: %v", p.ID, err))
			} else {
				result.Imported++
			}
			continue
		case playerLandingCached:
			// File exists in storage — will be read via GobCache below
		}

		// Step 2: Read player landing via GobCache (Redis gob → filesystem JSON)
		landingRes := resource.PlayerLanding{PlayerID: playerID}
		landing, origin, err := cache.ReadParsedCached[*nhl.PlayerLanding](ctx, a.Storage, a.GobCache, landingRes)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("player %d: read error: %v", p.ID, err))
			continue
		}
		// Only record origin for cache hits (downloads already recorded above)
		if downloadStatus == playerLandingCached {
			result.Origins.Record(origin)
		}

		// Step 3: Match with Yahoo player
		var teamAbbrev string
		if landing.CurrentTeamAbbrev != nil {
			teamAbbrev = *landing.CurrentTeamAbbrev
		}
		var nhlBirthDate time.Time
		if landing.BirthDate != "" {
			nhlBirthDate = parseDate(landing.BirthDate)
		}
		matchResult, err := matching.MatchYahooID(landing, teamAbbrev, nhlBirthDate, yahooPool)
		if err != nil {
			log.Debug().
				Err(err).
				Int64("nhl_id", p.ID).
				Str("name", landing.FirstName.Default+" "+landing.LastName.Default).
				Msg("Yahoo ID matching warning")
		}

		// Build UpsertPlayerParams
		params := buildProcessUpsertParams(landing, matchResult)

		// Clear any conflicting yahoo_id assignment before upserting
		if matchResult.Matched {
			clearParams := sqlcdb.ClearConflictingYahooIDParams{
				YahooID: pgtype.Int8{Int64: int64(matchResult.YahooID), Valid: true},
				ID:      p.ID,
			}
			if err := a.Queries.ClearConflictingYahooID(ctx, clearParams); err != nil {
				log.Warn().Err(err).
					Int64("nhl_id", p.ID).
					Int("yahoo_id", int(matchResult.YahooID)).
					Msg("Failed to clear conflicting yahoo_id")
			}
		}

		// Upsert to database
		if err := a.Queries.UpsertPlayer(ctx, params); err != nil {
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

		// Upsert career data per-player to spread DB work across the activity
		// timeout window instead of accumulating a huge batch at the end.
		awards, totals, err := a.upsertPlayerCareerData(ctx, landing)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("player %d career data: %v", p.ID, err))
			continue
		}
		result.AwardsImported += awards
		result.TotalsImported += totals
	}

	// Remove matched Yahoo IDs from available set
	if len(matchedYahooIDs) > 0 {
		if err := RemoveFromYahooIDPool(ctx, a.RedisClient, matchedYahooIDs); err != nil {
			log.Warn().Err(err).Int("count", len(matchedYahooIDs)).Msg("Failed to remove matched Yahoo IDs")
		}
	}

	log.Info().
		Int("batch_size", len(players)).
		Int("downloaded", result.Downloaded).
		Int("cache_hits", result.Origins.Total()-result.Downloaded).
		Int("missing", result.Missing).
		Int("imported", result.Imported).
		Int("matched", result.Matched).
		Int("errors", len(result.Errors)).
		Msg("Process player batch complete")

	return result, nil
}

// upsertPlayerCareerData upserts awards and season totals for a single player.
// Returns the number of awards and season totals upserted.
func (a *PlayerActivities) upsertPlayerCareerData(ctx context.Context, landing *nhl.PlayerLanding) (int, int, error) {
	playerID := landing.PlayerID.Int64()

	// Awards
	var awardParams []sqlcdb.UpsertPlayerAwardBatchParams
	for _, award := range landing.Awards {
		trophyName := award.Trophy.Default
		for _, awardSeason := range award.Seasons {
			awardParams = append(awardParams, sqlcdb.UpsertPlayerAwardBatchParams{
				PlayerID:   playerID,
				TrophyName: trophyName,
				Season:     int32(awardSeason.SeasonID.StartYear()),
			})
		}
	}
	if len(awardParams) > 0 {
		if err := execBatch(a.CareerQueries.UpsertPlayerAwardBatch(ctx, awardParams), func(i int) string {
			return fmt.Sprintf("award trophy %q season %d", awardParams[i].TrophyName, awardParams[i].Season)
		}); err != nil {
			return 0, 0, err
		}
	}

	// Season totals
	var totalParams []sqlcdb.UpsertPlayerSeasonTotalBatchParams
	for _, st := range landing.SeasonTotals {
		var sequence int32
		if st.Sequence != nil {
			sequence = int32(*st.Sequence)
		}
		totalParams = append(totalParams, sqlcdb.UpsertPlayerSeasonTotalBatchParams{
			PlayerID:     playerID,
			Season:       int32(st.Season.StartYear()),
			GameType:     int16(st.GameType.Int()),
			LeagueAbbrev: st.LeagueAbbrev,
			TeamName:     st.TeamName.Default,
			Sequence:     sequence,
			GamesPlayed:  int32(st.GamesPlayed),
			Goals:        intPtrToInt4(st.Goals),
			Assists:      intPtrToInt4(st.Assists),
			Points:       intPtrToInt4(st.Points),
			PlusMinus:    intPtrToInt4(st.PlusMinus),
			PIM:          intPtrToInt4(st.PIM),
		})
	}
	if len(totalParams) > 0 {
		if err := execBatch(a.CareerQueries.UpsertPlayerSeasonTotalBatch(ctx, totalParams), func(i int) string {
			return fmt.Sprintf("season total season %d league %s", totalParams[i].Season, totalParams[i].LeagueAbbrev)
		}); err != nil {
			return len(awardParams), 0, err
		}
	}

	return len(awardParams), len(totalParams), nil
}

// buildProcessUpsertParams creates UpsertPlayerParams from PlayerLanding and match result.
// This is a copy of buildUpsertParams to avoid circular dependencies.
func buildProcessUpsertParams(landing *nhl.PlayerLanding, match matching.YahooIDMatchResult) sqlcdb.UpsertPlayerParams {
	// Trim whitespace from name fields - NHL API sometimes has trailing spaces
	firstName := strings.TrimSpace(landing.FirstName.Default)
	lastName := strings.TrimSpace(landing.LastName.Default)

	params := sqlcdb.UpsertPlayerParams{
		ID:                  landing.PlayerID.Int64(),
		FirstName:           firstName,
		LastName:            lastName,
		FirstNameNormalized: matching.NormalizeName(firstName),
		LastNameNormalized:  matching.NormalizeName(lastName),
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
	params.TeamID = ptrToInt8(landing.CurrentTeamID)

	// Jersey number — only set if positive (0 means unassigned)
	if landing.SweaterNumber != nil && *landing.SweaterNumber > 0 {
		params.SweaterNumber = ptrToInt4(landing.SweaterNumber)
	}

	// Birth date
	params.BirthDate = parseDateToPgDate(landing.BirthDate)

	// Birth location
	if landing.BirthCity != nil {
		params.BirthCity = pgtype.Text{String: strings.TrimSpace(landing.BirthCity.Default), Valid: true}
	}
	if landing.BirthStateProvince != nil {
		params.BirthStateProvince = pgtype.Text{String: strings.TrimSpace(landing.BirthStateProvince.Default), Valid: true}
	}
	params.BirthCountry = ptrToTextTrimmed(landing.BirthCountry)
	params.HeroImageURL = ptrToTextTrimmed(landing.HeroImage)
	params.PlayerSlug = ptrToTextTrimmed(landing.PlayerSlug)

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
func (a *PlayerActivities) importMissingPlayer(ctx context.Context, p store.BoxscorePlayer) error {
	firstName := strings.TrimSpace(p.FirstName)
	lastName := strings.TrimSpace(p.LastName)
	position := strings.TrimSpace(p.Position)

	params := sqlcdb.UpsertPlayerParams{
		ID:                  p.ID,
		FirstName:           firstName,
		LastName:            lastName,
		FirstNameNormalized: matching.NormalizeName(firstName),
		LastNameNormalized:  matching.NormalizeName(lastName),
		Position:            position,
		IsActive:            false, // Assume inactive since they returned 404
	}

	log.Debug().
		Int64("player_id", p.ID).
		Str("name", firstName+" "+lastName).
		Str("position", position).
		Msg("Importing missing player with minimal info")

	return a.Queries.UpsertPlayer(ctx, params)
}

// intPtrToInt4 converts a *int to pgtype.Int4.
func intPtrToInt4(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true}
}
