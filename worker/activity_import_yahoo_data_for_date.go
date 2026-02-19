package worker

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
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
}

// ImportYahooDataForDateActivity imports Yahoo team summaries and rosters for a single date.
func ImportYahooDataForDateActivity(ctx context.Context, input ImportYahooDataForDateInput) (ImportYahooDataForDateResult, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("ImportYahooDataForDateActivity", time.Since(start))
	}()

	logger := activity.GetLogger(ctx)
	result := ImportYahooDataForDateResult{}

	if len(input.Teams) == 0 {
		return result, nil
	}

	fs := store.NewStore()

	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return result, fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()

	queries := sqlcdb.New(pool)

	// Collect params for this date across all teams
	summaryParams, statParams := collectSummaryParams(fs, input.Teams, input.Date)
	rosterParams := collectRosterParams(fs, input.Teams, input.Date)

	// Batch upsert summaries
	if len(summaryParams) > 0 {
		if err := upsertSummaries(ctx, queries, summaryParams); err != nil {
			return result, err
		}
		result.SummariesImported = len(summaryParams)
	}

	// Batch upsert stats
	if len(statParams) > 0 {
		if err := upsertStats(ctx, queries, statParams); err != nil {
			return result, err
		}
		result.StatsImported = len(statParams)
	}

	// Batch upsert rosters
	if len(rosterParams) > 0 {
		if err := upsertRosters(ctx, queries, rosterParams); err != nil {
			return result, err
		}
		result.RostersImported = len(rosterParams)
	}

	if result.SummariesImported > 0 || result.RostersImported > 0 {
		logger.Info("Imported Yahoo data for date",
			"date", input.Date.Format("2006-01-02"),
			"summaries", result.SummariesImported,
			"stats", result.StatsImported,
			"rosters", result.RostersImported)
	}

	return result, nil
}

// collectSummaryParams reads team summary files and returns params for summaries and stats.
func collectSummaryParams(fs store.Store, teams []TeamInfo, date time.Time) (
	[]sqlcdb.UpsertYahooTeamSummaryBatchParams,
	[]sqlcdb.UpsertYahooTeamSummaryStatBatchParams,
) {
	var summaryParams []sqlcdb.UpsertYahooTeamSummaryBatchParams
	var statParams []sqlcdb.UpsertYahooTeamSummaryStatBatchParams
	pgDate := pgtype.Date{Time: date, Valid: true}

	for _, teamInfo := range teams {
		summaryFile := store.TeamSummaryFile{
			TeamID:   teamInfo.TeamID,
			LeagueID: teamInfo.LeagueID,
			Date:     date,
		}

		if !fs.Exists(summaryFile) {
			continue
		}

		data, err := fs.Read(summaryFile)
		if err != nil {
			log.Debug().Err(err).
				Int("teamID", teamInfo.TeamID).
				Str("date", date.Format("2006-01-02")).
				Msg("Failed to read summary file")
			continue
		}

		fantasy, err := store.ParseXML(data)
		if err != nil {
			log.Debug().Err(err).
				Int("teamID", teamInfo.TeamID).
				Str("date", date.Format("2006-01-02")).
				Msg("Failed to parse summary XML")
			continue
		}

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
func collectRosterParams(fs store.Store, teams []TeamInfo, date time.Time) []sqlcdb.UpsertYahooTeamRosterBatchParams {
	var rosterParams []sqlcdb.UpsertYahooTeamRosterBatchParams
	pgDate := pgtype.Date{Time: date, Valid: true}

	for _, teamInfo := range teams {
		rosterFile := store.RosterFile{
			TeamID:   teamInfo.TeamID,
			LeagueID: teamInfo.LeagueID,
			Date:     date,
		}

		if !fs.Exists(rosterFile) {
			continue
		}

		data, err := fs.Read(rosterFile)
		if err != nil {
			log.Debug().Err(err).
				Int("teamID", teamInfo.TeamID).
				Str("date", date.Format("2006-01-02")).
				Msg("Failed to read roster file")
			continue
		}

		fantasy, err := store.ParseXML(data)
		if err != nil {
			log.Debug().Err(err).
				Int("teamID", teamInfo.TeamID).
				Str("date", date.Format("2006-01-02")).
				Msg("Failed to parse roster XML")
			continue
		}

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
			})
		}
	}

	return rosterParams
}

// upsertSummaries batch upserts team summary records.
func upsertSummaries(ctx context.Context, queries *sqlcdb.Queries, params []sqlcdb.UpsertYahooTeamSummaryBatchParams) error {
	var batchErr error
	results := queries.UpsertYahooTeamSummaryBatch(ctx, params)
	results.Exec(func(i int, err error) {
		if err != nil && batchErr == nil {
			batchErr = fmt.Errorf("summary league_id=%d team_id=%d date=%s: %w",
				params[i].LeagueID, params[i].TeamID,
				params[i].Date.Time.Format("2006-01-02"), err)
		}
	})
	return batchErr
}

// upsertStats batch upserts team summary stat records.
func upsertStats(ctx context.Context, queries *sqlcdb.Queries, params []sqlcdb.UpsertYahooTeamSummaryStatBatchParams) error {
	var batchErr error
	results := queries.UpsertYahooTeamSummaryStatBatch(ctx, params)
	results.Exec(func(i int, err error) {
		if err != nil && batchErr == nil {
			batchErr = fmt.Errorf("stat league_id=%d team_id=%d date=%s stat_id=%d: %w",
				params[i].LeagueID, params[i].TeamID,
				params[i].Date.Time.Format("2006-01-02"), params[i].StatID, err)
		}
	})
	return batchErr
}

// upsertRosters batch upserts team roster records.
func upsertRosters(ctx context.Context, queries *sqlcdb.Queries, params []sqlcdb.UpsertYahooTeamRosterBatchParams) error {
	var batchErr error
	results := queries.UpsertYahooTeamRosterBatch(ctx, params)
	results.Exec(func(i int, err error) {
		if err != nil && batchErr == nil {
			batchErr = fmt.Errorf("roster league_id=%d team_id=%d date=%s player_id=%d: %w",
				params[i].LeagueID, params[i].TeamID,
				params[i].Date.Time.Format("2006-01-02"), params[i].PlayerID, err)
		}
	})
	return batchErr
}
