package worker

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ImportYahooDataForDateInput contains parameters for importing Yahoo team data for a single date.
type ImportYahooDataForDateInput struct {
	Season int
	Teams  []TeamInfo
	Date   time.Time
}

// ImportYahooDataForDateResult contains the results of importing Yahoo team data for a date.
type ImportYahooDataForDateResult struct {
	SummariesImported int
	StatsImported     int
	RostersImported   int
	Origins           core.OriginCounts
}

// ImportYahooDataForDate imports Yahoo team summaries and rosters for a single date.
func (a *SeasonsActivities) ImportYahooDataForDate(ctx context.Context, input ImportYahooDataForDateInput) (ImportYahooDataForDateResult, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("ImportYahooDataForDate", time.Since(start))
	}()

	logger := activity.GetLogger(ctx)
	result := ImportYahooDataForDateResult{Origins: core.OriginCounts{}}

	if len(input.Teams) == 0 {
		return result, nil
	}

	// Collect params for this date across all teams
	summaryParams, statParams := a.collectSummaryParams(ctx, input.Teams, input.Date, result.Origins)
	rosterParams := a.collectRosterParams(ctx, input.Teams, input.Date, result.Origins)

	// Batch upsert summaries
	if len(summaryParams) > 0 {
		if err := upsertSummaries(ctx, a.ImportQueries, summaryParams); err != nil {
			return result, err
		}
		result.SummariesImported = len(summaryParams)
	}

	// Batch upsert stats
	if len(statParams) > 0 {
		if err := upsertStats(ctx, a.ImportQueries, statParams); err != nil {
			return result, err
		}
		result.StatsImported = len(statParams)
	}

	// Batch upsert rosters
	if len(rosterParams) > 0 {
		if err := upsertRosters(ctx, a.ImportQueries, rosterParams); err != nil {
			return result, err
		}
		result.RostersImported = len(rosterParams)
	}

	if result.SummariesImported > 0 || result.RostersImported > 0 {
		logger.Info("Imported Yahoo data for date",
			"date", input.Date.Format(config.DateFormat),
			"summaries", result.SummariesImported,
			"stats", result.StatsImported,
			"rosters", result.RostersImported)
	}

	return result, nil
}

// collectSummaryParams reads team summary files and returns params for summaries and stats.
func (a *SeasonsActivities) collectSummaryParams(ctx context.Context, teams []TeamInfo, date time.Time, origins core.OriginCounts) (
	[]sqlcdb.UpsertYahooTeamSummaryBatchParams,
	[]sqlcdb.UpsertYahooTeamSummaryStatBatchParams,
) {
	var summaryParams []sqlcdb.UpsertYahooTeamSummaryBatchParams
	var statParams []sqlcdb.UpsertYahooTeamSummaryStatBatchParams
	pgDate := pgtype.Date{Time: date, Valid: true}

	for _, teamInfo := range teams {
		summaryRes := resource.TeamSummary{LeagueID: teamInfo.LeagueID, TeamID: teamInfo.TeamID, Date: date}
		if !a.Storage.Exists(summaryRes.Path()) {
			continue
		}

		fantasy, origin, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, summaryRes)
		if err != nil {
			log.Debug().Err(err).
				Int("teamID", teamInfo.TeamID).
				Str("date", date.Format(config.DateFormat)).
				Msg("Failed to read summary file")
			continue
		}
		origins.Record(origin)

		team := fantasy.Team
		teamStats := team.TeamStats

		summaryParams = append(summaryParams, sqlcdb.UpsertYahooTeamSummaryBatchParams{
			LeagueID:     int32(teamInfo.LeagueID),
			TeamID:       int32(teamInfo.TeamID),
			Date:         pgDate,
			CoverageType: teamStats.CoverageType,
		})

		for _, stat := range teamStats.Stats.Slice {
			statID, err := strconv.Atoi(stat.StatID)
			if err != nil {
				continue
			}
			statParams = append(statParams, sqlcdb.UpsertYahooTeamSummaryStatBatchParams{
				LeagueID: int32(teamInfo.LeagueID),
				TeamID:   int32(teamInfo.TeamID),
				Date:     pgDate,
				StatID:   int32(statID),
				Value:    stat.Value,
			})
		}
	}

	return summaryParams, statParams
}

// collectRosterParams reads team roster files and returns params for rosters.
func (a *SeasonsActivities) collectRosterParams(ctx context.Context, teams []TeamInfo, date time.Time, origins core.OriginCounts) []sqlcdb.UpsertYahooTeamRosterBatchParams {
	var rosterParams []sqlcdb.UpsertYahooTeamRosterBatchParams
	pgDate := pgtype.Date{Time: date, Valid: true}

	for _, teamInfo := range teams {
		rosterRes := resource.Roster{LeagueID: teamInfo.LeagueID, TeamID: teamInfo.TeamID, Date: date}
		if !a.Storage.Exists(rosterRes.Path()) {
			continue
		}

		fantasy, origin, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, rosterRes)
		if err != nil {
			log.Debug().Err(err).
				Int("teamID", teamInfo.TeamID).
				Str("date", date.Format(config.DateFormat)).
				Msg("Failed to read roster file")
			continue
		}
		origins.Record(origin)

		team := fantasy.Team
		roster := team.Roster

		for _, player := range roster.Players.Slice {
			rosterParams = append(rosterParams, sqlcdb.UpsertYahooTeamRosterBatchParams{
				LeagueID:         int32(teamInfo.LeagueID),
				TeamID:           int32(teamInfo.TeamID),
				Date:             pgDate,
				PlayerID:         int32(player.ID),
				CoverageType:     roster.CoverageType,
				IsEditable:       roster.IsEditable,
				PlayerKey:        player.Key,
				SelectedPosition: player.SelectedPosition.Position,
				IsFlex:           player.SelectedPosition.IsFlex != 0,

				PlayerStatus:      pgtype.Text{String: player.Status, Valid: player.Status != ""},
				PlayerStatusFull:  pgtype.Text{String: player.StatusFull, Valid: player.StatusFull != ""},
				InjuryNote:        pgtype.Text{String: player.InjuryNote, Valid: player.InjuryNote != ""},
				OnDisabledList:    pgtype.Bool{Bool: player.OnDisabledList != 0, Valid: true},
				PositionType:      pgtype.Text{String: player.PositionType, Valid: player.PositionType != ""},
				DisplayPosition:   pgtype.Text{String: player.DisplayPosition, Valid: player.DisplayPosition != ""},
				PrimaryPosition:   pgtype.Text{String: player.PrimaryPosition, Valid: player.PrimaryPosition != ""},
				EligiblePositions: player.EligiblePositions,
				UniformNumber:     pgtype.Int4{Int32: int32(player.UniformNumber), Valid: player.UniformNumber != 0},
				EditorialTeamAbbr: pgtype.Text{String: player.EditorialTeamAbbr, Valid: player.EditorialTeamAbbr != ""},
			})
		}
	}

	return rosterParams
}

// upsertSummaries batch upserts team summary records.
func upsertSummaries(ctx context.Context, queries YahooDataUpserter, params []sqlcdb.UpsertYahooTeamSummaryBatchParams) error {
	return execBatch(queries.UpsertYahooTeamSummaryBatch(ctx, params), func(i int) string {
		return fmt.Sprintf("summary league_id=%d team_id=%d date=%s",
			params[i].LeagueID, params[i].TeamID, params[i].Date.Time.Format(config.DateFormat))
	})
}

// upsertStats batch upserts team summary stat records.
func upsertStats(ctx context.Context, queries YahooDataUpserter, params []sqlcdb.UpsertYahooTeamSummaryStatBatchParams) error {
	return execBatch(queries.UpsertYahooTeamSummaryStatBatch(ctx, params), func(i int) string {
		return fmt.Sprintf("stat league_id=%d team_id=%d date=%s stat_id=%d",
			params[i].LeagueID, params[i].TeamID, params[i].Date.Time.Format(config.DateFormat), params[i].StatID)
	})
}

// upsertRosters batch upserts team roster records.
func upsertRosters(ctx context.Context, queries YahooDataUpserter, params []sqlcdb.UpsertYahooTeamRosterBatchParams) error {
	return execBatch(queries.UpsertYahooTeamRosterBatch(ctx, params), func(i int) string {
		return fmt.Sprintf("roster league_id=%d team_id=%d date=%s player_id=%d",
			params[i].LeagueID, params[i].TeamID, params[i].Date.Time.Format(config.DateFormat), params[i].PlayerID)
	})
}
