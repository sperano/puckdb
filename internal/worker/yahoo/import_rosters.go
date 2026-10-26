package yahoo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
)

// ImportYahooDataForDate imports Yahoo team summaries (their fixed category
// columns and every category as a normalized row), the leagues' standings
// snapshots and the team rosters for a single date.
func (a *ImportActivities) ImportYahooDataForDate(ctx context.Context, input ImportYahooDataForDateInput) (ImportYahooDataForDateResult, error) {
	defer metrics.TrackActivityDuration("ImportYahooDataForDate")()

	logger := activity.GetLogger(ctx)
	result := ImportYahooDataForDateResult{Origins: core.OriginCounts{}}

	if len(input.Teams) == 0 {
		return result, nil
	}

	// Collect params for this date across all teams
	summaries, summaryFailures, err := a.collectSummaryParams(ctx, input.Teams, input.Date, result.Origins)
	if err != nil {
		return result, err
	}
	standings, standingFailures := a.collectStandingParams(ctx, input, result.Origins)
	rosterParams, rosterFailures := a.collectRosterParams(ctx, input.Teams, input.Date, result.Origins)
	result.ReadFailures = summaryFailures + standingFailures + rosterFailures

	rows := dateRows{date: input.Date, summaries: summaries, standings: standings, rosters: rosterParams}
	if rows.empty() {
		return result, nil
	}
	var written dateCounts
	if err := a.Tx.InTx(ctx, func(q YahooDataUpserter) error {
		var err error
		written, err = writeDateRows(ctx, q, rows)
		return err
	}); err != nil {
		return result, err
	}
	result.SummariesImported = written.summaries
	result.StandingsImported = written.standings
	result.RostersImported = written.rosters

	if result.SummariesImported > 0 || result.StandingsImported > 0 || result.RostersImported > 0 {
		logger.Info("Imported Yahoo data for date",
			"date", input.Date.Format(config.DateFormat),
			"summaries", result.SummariesImported,
			"standings", result.StandingsImported,
			"rosters", result.RostersImported)
	}

	return result, nil
}

// collectRosterParams reads team roster files and returns params for
// rosters, and how many existing files could not be read.
func (a *ImportActivities) collectRosterParams(ctx context.Context, teams []TeamInfo, date time.Time, origins core.OriginCounts) ([]sqlcdb.UpsertYahooTeamRosterBatchParams, int) {
	var rosterParams []sqlcdb.UpsertYahooTeamRosterBatchParams
	readFailures := 0
	pgDate := pgtype.Date{Time: date, Valid: true}

	for _, teamInfo := range teams {
		rosterRes := resource.Roster{LeagueID: teamInfo.LeagueID, TeamID: teamInfo.TeamID, Date: date}
		if !resource.Exists(ctx, a.Storage, rosterRes) {
			continue
		}

		fantasy, origin, err := a.GobCache.ReadParsedCached(ctx, a.Storage, rosterRes)
		if err != nil {
			log.Debug().Err(err).
				Int("teamID", teamInfo.TeamID).
				Str("date", date.Format(config.DateFormat)).
				Msg("Failed to read roster file")
			readFailures++
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

	return rosterParams, readFailures
}

// upsertRosters batch upserts team roster records.
func upsertRosters(ctx context.Context, queries YahooDataUpserter, params []sqlcdb.UpsertYahooTeamRosterBatchParams) error {
	return shared.ExecBatch(queries.UpsertYahooTeamRosterBatch(ctx, params), func(i int) string {
		return fmt.Sprintf("roster league_id=%d team_id=%d date=%s player_id=%d",
			params[i].LeagueID, params[i].TeamID, params[i].Date.Time.Format(config.DateFormat), params[i].PlayerID)
	})
}
