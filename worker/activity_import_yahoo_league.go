package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ImportYahooLeagueInput contains parameters for importing a Yahoo league.
type ImportYahooLeagueInput struct {
	Season   int
	LeagueID int
}

// ImportYahooLeagueResult contains the results of importing a Yahoo league.
type ImportYahooLeagueResult struct {
	RosterPositions int
	StatCategories  int
}

// ImportYahooLeague imports a Yahoo league from cached XML into the database.
func (a *SeasonsActivities) ImportYahooLeague(ctx context.Context, input ImportYahooLeagueInput) (ImportYahooLeagueResult, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("ImportYahooLeague", time.Since(start))
	}()

	logger := activity.GetLogger(ctx)
	result := ImportYahooLeagueResult{}

	// Read the league file
	leagueRes := resource.League{Season: input.Season, LeagueID: input.LeagueID}
	if !a.Storage.Exists(leagueRes.Path()) {
		log.Debug().Int("season", input.Season).Int("leagueID", input.LeagueID).Msg("No league file found")
		return result, nil
	}

	fantasy, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, leagueRes)
	if err != nil {
		return result, fmt.Errorf("read league file: %w", err)
	}

	league := fantasy.League

	// Parse dates
	var startDate, endDate, tradeEndDate pgtype.Date
	if t, err := time.Parse(config.DateFormat, league.StartDate); err == nil {
		startDate = pgtype.Date{Time: t, Valid: true}
	}
	if t, err := time.Parse(config.DateFormat, league.EndDate); err == nil {
		endDate = pgtype.Date{Time: t, Valid: true}
	}
	if t, err := time.Parse(config.DateFormat, league.Settings.TradeEndDate); err == nil {
		tradeEndDate = pgtype.Date{Time: t, Valid: true}
	}

	// Parse draft time
	var draftTime pgtype.Timestamptz
	if league.Settings.DraftTime > 0 {
		draftTime = pgtype.Timestamptz{
			Time:  time.Unix(int64(league.Settings.DraftTime), 0),
			Valid: true,
		}
	}

	// Upsert the league
	leagueParams := sqlcdb.UpsertYahooLeagueParams{
		ID:                    int32(league.ID),
		LeagueKey:             league.Key,
		Name:                  league.Name,
		Url:                   league.URL,
		LogoUrl:               league.LogoURL,
		Season:                int32(league.Season),
		GameCode:              league.GameCode,
		NumTeams:              int32(league.NumTeams),
		ScoringType:           league.ScoringType,
		LeagueType:            league.LeagueType,
		DraftStatus:           league.DraftStatus,
		IsProLeague:           league.IsProLeague == 1,
		IsCashLeague:          league.IsCashLeague == 1,
		StartDate:             startDate,
		EndDate:               endDate,
		DraftType:             league.Settings.DraftType,
		IsAuctionDraft:        league.Settings.IsAuctionDraft == 1,
		DraftTime:             draftTime,
		DraftPickTime:         pgtype.Int4{Int32: int32(league.Settings.DraftPickTime), Valid: league.Settings.DraftPickTime > 0},
		WaiverType:            league.Settings.WaiverType,
		WaiverRule:            league.Settings.WaiverRule,
		WaiverTime:            pgtype.Int4{Int32: int32(league.Settings.WaiverTime), Valid: league.Settings.WaiverTime > 0},
		TradeEndDate:          tradeEndDate,
		TradeRatifyType:       league.Settings.TradeRatifyType,
		TradeRejectTime:       pgtype.Int4{Int32: int32(league.Settings.TradeRejectTime), Valid: league.Settings.TradeRejectTime > 0},
		MaxTeams:              pgtype.Int4{Int32: int32(league.Settings.MaxTeams), Valid: league.Settings.MaxTeams > 0},
		PlayerPool:            league.Settings.PlayerPool,
		PostDraftPlayers:      league.Settings.PostDraftPlayers,
		CantCutList:           league.Settings.CantCutList,
		UsesPlayoff:           league.Settings.UsesPlayoff == 1,
		PersistentUrl:         league.Settings.PersistentURL,
		LeagueUpdateTimestamp: pgtype.Int8{Int64: int64(league.LeagueUpdateTimestamp), Valid: league.LeagueUpdateTimestamp > 0},
	}

	if err := a.ImportQueries.UpsertYahooLeague(ctx, leagueParams); err != nil {
		return result, fmt.Errorf("upsert league %d: %w", league.ID, err)
	}

	// Batch upsert roster positions
	if len(league.Settings.RosterPositions.Slice) > 0 {
		posParams := make([]sqlcdb.UpsertYahooLeagueRosterPositionBatchParams, len(league.Settings.RosterPositions.Slice))
		for i, pos := range league.Settings.RosterPositions.Slice {
			posParams[i] = sqlcdb.UpsertYahooLeagueRosterPositionBatchParams{
				LeagueID:           int32(league.ID),
				Position:           pos.Position,
				PositionType:       pos.PositionType,
				Count:              int32(pos.Count),
				IsStartingPosition: pos.IsStartingPosition == 1,
			}
		}

		var batchErr error
		results := a.ImportQueries.UpsertYahooLeagueRosterPositionBatch(ctx, posParams)
		results.Exec(func(i int, err error) {
			if err != nil && batchErr == nil {
				batchErr = fmt.Errorf("roster position %s: %w", posParams[i].Position, err)
			}
		})
		if batchErr != nil {
			return result, batchErr
		}
		result.RosterPositions = len(posParams)
	}

	// Batch upsert stat categories
	if len(league.Settings.StatCategories.Stats.Slice) > 0 {
		statParams := make([]sqlcdb.UpsertYahooLeagueStatCategoryBatchParams, len(league.Settings.StatCategories.Stats.Slice))
		for i, stat := range league.Settings.StatCategories.Stats.Slice {
			statParams[i] = sqlcdb.UpsertYahooLeagueStatCategoryBatchParams{
				LeagueID:  int32(league.ID),
				StatID:    int32(stat.StatID),
				Name:      stat.Name,
				Abbr:      stat.Abbr,
				StatGroup: stat.Group,
				Enabled:   stat.Enabled == 1,
				Value:     pgtype.Float4{}, // Yahoo doesn't provide point values in settings response
			}
		}

		var batchErr error
		results := a.ImportQueries.UpsertYahooLeagueStatCategoryBatch(ctx, statParams)
		results.Exec(func(i int, err error) {
			if err != nil && batchErr == nil {
				batchErr = fmt.Errorf("stat category %d: %w", statParams[i].StatID, err)
			}
		})
		if batchErr != nil {
			return result, batchErr
		}
		result.StatCategories = len(statParams)
	}

	logger.Info("Imported Yahoo league",
		"season", input.Season,
		"leagueID", input.LeagueID,
		"rosterPositions", result.RosterPositions,
		"statCategories", result.StatCategories)

	return result, nil
}

// YahooLeagueUpserter is the interface for database operations needed by Yahoo league import.
type YahooLeagueUpserter interface {
	UpsertYahooLeague(ctx context.Context, arg sqlcdb.UpsertYahooLeagueParams) error
	UpsertYahooLeagueRosterPositionBatch(ctx context.Context, arg []sqlcdb.UpsertYahooLeagueRosterPositionBatchParams) *sqlcdb.UpsertYahooLeagueRosterPositionBatchBatchResults
	UpsertYahooLeagueStatCategoryBatch(ctx context.Context, arg []sqlcdb.UpsertYahooLeagueStatCategoryBatchParams) *sqlcdb.UpsertYahooLeagueStatCategoryBatchBatchResults
}
