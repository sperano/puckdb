package player

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/matching"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
)

const yahooPlayerBaseURL = "https://sports.yahoo.com/nhl/players/"

// PlayerUpserter is the interface for database operations needed by player import.
type PlayerUpserter interface {
	UpsertPlayer(ctx context.Context, arg sqlcdb.UpsertPlayerParams) error
	ClearConflictingYahooID(ctx context.Context, arg sqlcdb.ClearConflictingYahooIDParams) error
}

// ProcessPlayerBatchResult contains combined download and import statistics.
type ProcessPlayerBatchResult struct {
	shared.FetchStats // Download stats (Downloaded, CacheHits, Missing)

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
func (a *Activities) ProcessPlayerBatch(ctx context.Context, players []store.BoxscorePlayer) (ProcessPlayerBatchResult, error) {
	result := ProcessPlayerBatchResult{
		Origins: core.OriginCounts{},
	}

	if len(players) == 0 {
		return result, nil
	}

	yahooPool, err := LoadYahooIDPool(ctx, a.RedisClient)
	if err != nil {
		return result, fmt.Errorf("load yahoo pool: %w", err)
	}

	var matchedYahooIDs []store.YahooPlayerID

	for _, p := range players {
		downloadStatus, err := a.cachePlayerLanding(ctx, p)
		if err != nil {
			return result, err
		}

		switch downloadStatus {
		case playerLandingDownloaded:
			result.Downloaded++
			result.Origins.Record(core.OriginRemoteNHLAPI)
		case playerLandingMissing:
			result.Missing++
			if err := a.importMissingPlayer(ctx, p); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("player %d (missing): import error: %v", p.ID, err))
			} else {
				result.Imported++
			}
			continue
		case playerLandingCached:
			// File exists in storage — will be read via GobCache below
		}

		if yahooID, matched := a.importPlayerLanding(ctx, p, downloadStatus, yahooPool, &result); matched {
			matchedYahooIDs = append(matchedYahooIDs, yahooID)
		}
	}

	if len(matchedYahooIDs) > 0 {
		if err := RemoveFromYahooIDPool(ctx, a.RedisClient, matchedYahooIDs); err != nil {
			log.Warn().Err(err).Int("count", len(matchedYahooIDs)).Msg("Failed to remove matched Yahoo IDs")
		}
	}

	logProcessPlayerBatch(len(players), result)
	return result, nil
}

// logProcessPlayerBatch logs the outcome of one ProcessPlayerBatch run.
func logProcessPlayerBatch(batchSize int, result ProcessPlayerBatchResult) {
	log.Info().
		Int("batch_size", batchSize).
		Int("downloaded", result.Downloaded).
		Int("cache_hits", result.Origins.Total()-result.Downloaded).
		Int("missing", result.Missing).
		Int("imported", result.Imported).
		Int("matched", result.Matched).
		Int("errors", len(result.Errors)).
		Msg("Process player batch complete")
}

// importPlayerLanding reads p's stored landing, matches it to a Yahoo ID from
// yahooPool and upserts the player with its career data, recording the
// outcome in result. It returns the matched Yahoo ID, if any, so the caller
// can remove it from the pool once the batch is done.
func (a *Activities) importPlayerLanding(
	ctx context.Context,
	p store.BoxscorePlayer,
	downloadStatus playerLandingStatus,
	yahooPool map[store.YahooPlayerID]*store.YahooPlayer,
	result *ProcessPlayerBatchResult,
) (store.YahooPlayerID, bool) {
	landingRes := resource.PlayerLanding{PlayerID: nhl.PlayerID(p.ID)}
	landing, origin, err := a.GobCache.ReadParsedCached(ctx, a.Storage, landingRes)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("player %d: read error: %v", p.ID, err))
		return 0, false
	}
	if downloadStatus == playerLandingCached {
		result.Origins.Record(origin)
	}

	matchResult := matchLandingYahooID(landing, yahooPool)
	if err := a.upsertLandingPlayer(ctx, p.ID, landing, matchResult); err != nil {
		errMsg := fmt.Sprintf("player %d (%s %s): upsert error: %v",
			p.ID, landing.FirstName.Default, landing.LastName.Default, err)
		if matchResult.Matched {
			errMsg = fmt.Sprintf("player %d (%s %s, yahoo_id=%d): upsert error: %v",
				p.ID, landing.FirstName.Default, landing.LastName.Default, matchResult.YahooID, err)
		}
		result.Errors = append(result.Errors, errMsg)
		return 0, false
	}

	result.Imported++
	if matchResult.Matched {
		result.Matched++
	}

	awards, totals, err := a.upsertPlayerCareerData(ctx, landing)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("player %d career data: %v", p.ID, err))
	} else {
		result.AwardsImported += awards
		result.TotalsImported += totals
	}
	return matchResult.YahooID, matchResult.Matched
}

// upsertLandingPlayer upserts the player of landing with its Yahoo match.
// A matched Yahoo ID is first cleared from every player but nhlID; a failure
// there is logged, and the upsert reports any remaining conflict.
func (a *Activities) upsertLandingPlayer(
	ctx context.Context,
	nhlID int64,
	landing *nhl.PlayerLanding,
	matchResult matching.YahooIDMatchResult,
) error {
	if matchResult.Matched {
		clearParams := sqlcdb.ClearConflictingYahooIDParams{
			YahooID: pgtype.Int8{Int64: int64(matchResult.YahooID), Valid: true},
			ID:      nhlID,
		}
		if err := a.Queries.ClearConflictingYahooID(ctx, clearParams); err != nil {
			log.Warn().Err(err).
				Int64("nhl_id", nhlID).
				Int("yahoo_id", int(matchResult.YahooID)).
				Msg("Failed to clear conflicting yahoo_id")
		}
	}
	return a.Queries.UpsertPlayer(ctx, buildProcessUpsertParams(landing, matchResult))
}

// matchLandingYahooID matches a landing to a Yahoo ID from yahooPool by name,
// current team and birth date. A matching warning is logged, not returned.
func matchLandingYahooID(landing *nhl.PlayerLanding, yahooPool map[store.YahooPlayerID]*store.YahooPlayer) matching.YahooIDMatchResult {
	var teamAbbrev string
	if landing.CurrentTeamAbbrev != nil {
		teamAbbrev = *landing.CurrentTeamAbbrev
	}
	var nhlBirthDate time.Time
	if landing.BirthDate != "" {
		nhlBirthDate = shared.ParseDate(landing.BirthDate)
	}
	matchResult, err := matching.MatchYahooID(landing, teamAbbrev, nhlBirthDate, yahooPool)
	if err != nil {
		log.Debug().
			Err(err).
			Int64("nhl_id", landing.PlayerID.Int64()).
			Str("name", landing.FirstName.Default+" "+landing.LastName.Default).
			Msg("Yahoo ID matching warning")
	}
	return matchResult
}

// upsertPlayerCareerData upserts awards and season totals for a single player.
// Returns the number of awards and season totals upserted.
func (a *Activities) upsertPlayerCareerData(ctx context.Context, landing *nhl.PlayerLanding) (int, int, error) {
	playerID := landing.PlayerID.Int64()

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
		if err := shared.ExecBatch(a.CareerQueries.UpsertPlayerAwardBatch(ctx, awardParams), func(i int) string {
			return fmt.Sprintf("award trophy %q season %d", awardParams[i].TrophyName, awardParams[i].Season)
		}); err != nil {
			return 0, 0, err
		}
	}

	var totalParams []sqlcdb.UpsertPlayerSeasonTotalBatchParams
	intlSeen := make(map[string]sqlcdb.UpsertInternationalSeasonTeamParams)
	for _, st := range landing.SeasonTotals {
		var sequence int32
		if st.Sequence != nil {
			sequence = int32(*st.Sequence)
		}
		// Only honor a team_id when it's meaningful for the FK on
		// player_season_totals.season_team_id_fkey: NHL rows match NHL
		// franchises in season_teams, international rows match the
		// 60-67 IDs we upsert ourselves. For other leagues (PCHA, WHA,
		// AHL, juniors, ...) the lookup may incidentally match an NHL
		// team_id (e.g., "Victoria Cougars" in PCHA = the 1926-27 NHL
		// franchise) — leave team_id NULL there so the FK skips.
		var teamID pgtype.Int8
		if tid, err := matching.LookupTeamIDByName(st.TeamName.Default); err == nil {
			isInt := matching.IsInternationalTeam(tid)
			if st.LeagueAbbrev == "NHL" || isInt {
				teamID = pgtype.Int8{Int64: tid, Valid: true}
			}
			if isInt {
				if info, err := matching.LookupTeamByID(tid); err == nil {
					key := fmt.Sprintf("%d:%d", st.Season.ID(), tid)
					intlSeen[key] = sqlcdb.UpsertInternationalSeasonTeamParams{
						Season:   int32(st.Season.ID()),
						TeamID:   tid,
						FullName: info.FullName,
						Abbrev:   info.Abbrev,
					}
				}
			}
		}
		totalParams = append(totalParams, sqlcdb.UpsertPlayerSeasonTotalBatchParams{
			PlayerID:     playerID,
			Season:       int32(st.Season.ID()),
			GameType:     sqlcdb.GameType(st.GameType.Label()),
			LeagueAbbrev: st.LeagueAbbrev,
			TeamName:     st.TeamName.Default,
			TeamID:       teamID,
			Sequence:     sequence,
			GamesPlayed:  int32(st.GamesPlayed),
			Goals:        intPtrToInt4(st.Goals),
			Assists:      intPtrToInt4(st.Assists),
			Points:       intPtrToInt4(st.Points),
			PlusMinus:    intPtrToInt4(st.PlusMinus),
			PIM:          intPtrToInt4(st.PIM),
		})
	}
	// Ensure season_teams rows exist for any international (season, team_id)
	// pairs this player references, so the FK on player_season_totals does
	// not roll back the batch.
	for _, p := range intlSeen {
		if err := a.CareerQueries.UpsertInternationalSeasonTeam(ctx, p); err != nil {
			return len(awardParams), 0, fmt.Errorf("upsert international season-team season=%d team_id=%d: %w",
				p.Season, p.TeamID, err)
		}
	}
	if len(totalParams) > 0 {
		if err := shared.ExecBatch(a.CareerQueries.UpsertPlayerSeasonTotalBatch(ctx, totalParams), func(i int) string {
			return fmt.Sprintf("season total season %d league %s", totalParams[i].Season, totalParams[i].LeagueAbbrev)
		}); err != nil {
			return len(awardParams), 0, err
		}
	}

	return len(awardParams), len(totalParams), nil
}

// buildProcessUpsertParams creates UpsertPlayerParams from PlayerLanding and match result.
func buildProcessUpsertParams(landing *nhl.PlayerLanding, match matching.YahooIDMatchResult) sqlcdb.UpsertPlayerParams {
	firstName := strings.TrimSpace(landing.FirstName.Default)
	lastName := strings.TrimSpace(landing.LastName.Default)

	params := sqlcdb.UpsertPlayerParams{
		ID:                  landing.PlayerID.Int64(),
		FirstName:           firstName,
		LastName:            lastName,
		FirstNameNormalized: matching.NormalizeName(firstName),
		LastNameNormalized:  matching.NormalizeName(lastName),
		Position:            stringToNullPlayerPosition(string(landing.Position)),
		ShootsCatches:       stringToNullHandSide(string(landing.ShootsCatches)),
		HeightInches:        pgtype.Int4{Int32: int32(landing.HeightInInches), Valid: landing.HeightInInches > 0},
		WeightPounds:        pgtype.Int4{Int32: int32(landing.WeightInPounds), Valid: landing.WeightInPounds > 0},
		IsActive:            landing.IsActive,
		HeadshotURL:         strings.TrimSpace(landing.Headshot),
	}

	if match.Matched {
		params.YahooID = pgtype.Int8{Int64: int64(match.YahooID), Valid: true}
		params.YahooHomeURL = fmt.Sprintf("%s%d/", yahooPlayerBaseURL, match.YahooID)
		params.YahooImage = match.ImageURL
	}

	params.TeamID = shared.PtrToInt8(landing.CurrentTeamID)

	if landing.SweaterNumber != nil && *landing.SweaterNumber > 0 {
		params.SweaterNumber = shared.PtrToInt4(landing.SweaterNumber)
	}

	params.BirthDate = shared.ParseDateToPgDate(landing.BirthDate)

	if landing.BirthCity != nil {
		params.BirthCity = pgtype.Text{String: strings.TrimSpace(landing.BirthCity.Default), Valid: true}
	}
	if landing.BirthStateProvince != nil {
		params.BirthStateProvince = pgtype.Text{String: strings.TrimSpace(landing.BirthStateProvince.Default), Valid: true}
	}
	params.BirthCountry = shared.PtrToTextTrimmed(landing.BirthCountry)
	params.HeroImageURL = shared.PtrToTextTrimmed(landing.HeroImage)
	params.PlayerSlug = shared.PtrToTextTrimmed(landing.PlayerSlug)

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
func (a *Activities) importMissingPlayer(ctx context.Context, p store.BoxscorePlayer) error {
	firstName := strings.TrimSpace(p.FirstName)
	lastName := strings.TrimSpace(p.LastName)
	position := strings.TrimSpace(p.Position)

	params := sqlcdb.UpsertPlayerParams{
		ID:                  p.ID,
		FirstName:           firstName,
		LastName:            lastName,
		FirstNameNormalized: matching.NormalizeName(firstName),
		LastNameNormalized:  matching.NormalizeName(lastName),
		Position:            stringToNullPlayerPosition(position),
		IsActive:            false,
	}

	log.Debug().
		Int64("player_id", p.ID).
		Str("name", firstName+" "+lastName).
		Str("position", position).
		Msg("Importing missing player with minimal info")

	return a.Queries.UpsertPlayer(ctx, params)
}

// stringToNullPlayerPosition converts a string to NullPlayerPosition, returning invalid for empty strings.
func stringToNullPlayerPosition(s string) sqlcdb.NullPlayerPosition {
	s = strings.TrimSpace(s)
	if s == "" {
		return sqlcdb.NullPlayerPosition{}
	}
	return sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPosition(s), Valid: true}
}

// stringToNullHandSide converts a string to NullHandSide, returning invalid for empty strings.
func stringToNullHandSide(s string) sqlcdb.NullHandSide {
	s = strings.TrimSpace(s)
	if s == "" {
		return sqlcdb.NullHandSide{}
	}
	return sqlcdb.NullHandSide{HandSide: sqlcdb.HandSide(s), Valid: true}
}

// intPtrToInt4 converts a *int to pgtype.Int4.
func intPtrToInt4(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true}
}
