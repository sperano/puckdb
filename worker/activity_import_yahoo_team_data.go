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

// ImportYahooTeamDataInput contains parameters for importing Yahoo team summaries and rosters.
type ImportYahooTeamDataInput struct {
	Season    int
	Teams     []TeamInfo
	StartDate time.Time
	EndDate   time.Time
}

// ImportYahooTeamDataResult contains the results of importing Yahoo team data.
type ImportYahooTeamDataResult struct {
	SummariesImported int
	StatsImported     int
	RostersImported   int
}

// ImportYahooTeamDataActivity imports Yahoo team summaries and rosters from cached XML into the database.
func ImportYahooTeamDataActivity(ctx context.Context, input ImportYahooTeamDataInput) (ImportYahooTeamDataResult, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("ImportYahooTeamDataActivity", time.Since(start))
	}()

	logger := activity.GetLogger(ctx)
	fs := store.NewStore()

	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return ImportYahooTeamDataResult{}, fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()

	queries := sqlcdb.New(pool)

	result, err := importYahooTeamDataImpl(ctx, fs, queries, input)
	if err != nil {
		return result, err
	}

	logger.Info("Imported Yahoo team data",
		"season", input.Season,
		"teams", len(input.Teams),
		"summaries", result.SummariesImported,
		"stats", result.StatsImported,
		"rosters", result.RostersImported)

	return result, nil
}

// YahooTeamDataUpserter is the interface for database operations needed by Yahoo team data import.
type YahooTeamDataUpserter interface {
	UpsertYahooTeamSummaryBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamSummaryBatchParams) *sqlcdb.UpsertYahooTeamSummaryBatchBatchResults
	UpsertYahooTeamSummaryStatBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamSummaryStatBatchParams) *sqlcdb.UpsertYahooTeamSummaryStatBatchBatchResults
	UpsertYahooTeamRosterBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamRosterBatchParams) *sqlcdb.UpsertYahooTeamRosterBatchBatchResults
}

func importYahooTeamDataImpl(
	ctx context.Context,
	fs store.Store,
	queries YahooTeamDataUpserter,
	input ImportYahooTeamDataInput,
) (ImportYahooTeamDataResult, error) {
	result := ImportYahooTeamDataResult{}

	if len(input.Teams) == 0 {
		return result, nil
	}

	// Collect all params across teams and dates
	var summaryParams []sqlcdb.UpsertYahooTeamSummaryBatchParams
	var statParams []sqlcdb.UpsertYahooTeamSummaryStatBatchParams
	var rosterParams []sqlcdb.UpsertYahooTeamRosterBatchParams

	// Iterate through each date in range
	for date := input.StartDate; !date.After(input.EndDate); date = date.AddDate(0, 0, 1) {
		pgDate := pgtype.Date{Time: date, Valid: true}

		for _, teamInfo := range input.Teams {
			// Process team summary
			summaryFile := store.TeamSummaryFile{
				TeamID:   teamInfo.TeamID,
				LeagueID: teamInfo.LeagueID,
				Date:     date,
			}

			if fs.Exists(summaryFile) {
				data, err := fs.Read(summaryFile)
				if err != nil {
					return result, fmt.Errorf("read summary file team %d date %s: %w",
						teamInfo.TeamID, date.Format("2006-01-02"), err)
				}

				fantasy, err := store.ParseXML(data)
				if err != nil {
					return result, fmt.Errorf("parse summary XML team %d date %s: %w",
						teamInfo.TeamID, date.Format("2006-01-02"), err)
				}

				team := fantasy.Team
				teamStats := team.TeamStats

				// Add summary record
				summaryParams = append(summaryParams, sqlcdb.UpsertYahooTeamSummaryBatchParams{
					LeagueID:     int32(teamInfo.LeagueID),
					TeamID:       int32(teamInfo.TeamID),
					Date:         pgDate,
					CoverageType: teamStats.CoverageType,
				})

				// Add stat records
				for _, stat := range teamStats.Stats.Slice {
					statID, err := strconv.Atoi(stat.StatID)
					if err != nil {
						log.Debug().
							Str("statID", stat.StatID).
							Int("teamID", teamInfo.TeamID).
							Msg("Invalid stat ID, skipping")
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

			// Process team roster
			rosterFile := store.RosterFile{
				TeamID:   teamInfo.TeamID,
				LeagueID: teamInfo.LeagueID,
				Date:     date,
			}

			if fs.Exists(rosterFile) {
				data, err := fs.Read(rosterFile)
				if err != nil {
					return result, fmt.Errorf("read roster file team %d date %s: %w",
						teamInfo.TeamID, date.Format("2006-01-02"), err)
				}

				fantasy, err := store.ParseXML(data)
				if err != nil {
					return result, fmt.Errorf("parse roster XML team %d date %s: %w",
						teamInfo.TeamID, date.Format("2006-01-02"), err)
				}

				team := fantasy.Team
				roster := team.Roster

				// Add roster records for each player
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
		}
	}

	// Batch upsert summaries
	if len(summaryParams) > 0 {
		var batchErr error
		results := queries.UpsertYahooTeamSummaryBatch(ctx, summaryParams)
		results.Exec(func(i int, err error) {
			if err != nil && batchErr == nil {
				batchErr = fmt.Errorf("summary league_id=%d team_id=%d date=%s: %w",
					summaryParams[i].LeagueID, summaryParams[i].TeamID,
					summaryParams[i].Date.Time.Format("2006-01-02"), err)
			}
		})
		if batchErr != nil {
			return result, batchErr
		}
		result.SummariesImported = len(summaryParams)
	}

	// Batch upsert stats
	if len(statParams) > 0 {
		var batchErr error
		results := queries.UpsertYahooTeamSummaryStatBatch(ctx, statParams)
		results.Exec(func(i int, err error) {
			if err != nil && batchErr == nil {
				batchErr = fmt.Errorf("stat league_id=%d team_id=%d date=%s stat_id=%d: %w",
					statParams[i].LeagueID, statParams[i].TeamID,
					statParams[i].Date.Time.Format("2006-01-02"), statParams[i].StatID, err)
			}
		})
		if batchErr != nil {
			return result, batchErr
		}
		result.StatsImported = len(statParams)
	}

	// Batch upsert rosters
	if len(rosterParams) > 0 {
		var batchErr error
		results := queries.UpsertYahooTeamRosterBatch(ctx, rosterParams)
		results.Exec(func(i int, err error) {
			if err != nil && batchErr == nil {
				batchErr = fmt.Errorf("roster league_id=%d team_id=%d date=%s player_id=%d: %w",
					rosterParams[i].LeagueID, rosterParams[i].TeamID,
					rosterParams[i].Date.Time.Format("2006-01-02"), rosterParams[i].PlayerID, err)
			}
		})
		if batchErr != nil {
			return result, batchErr
		}
		result.RostersImported = len(rosterParams)
	}

	return result, nil
}
