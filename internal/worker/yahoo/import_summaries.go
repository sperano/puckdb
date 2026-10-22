package yahoo

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
)

// summaryRows is what a date's team summary files import: one summary row
// per file, with the fixed category columns, and the file's whole category
// set.
type summaryRows struct {
	summaries []sqlcdb.UpsertYahooTeamSummaryBatchParams
	stats     []sqlcdb.ReplaceYahooTeamSummaryStatsBatchParams
}

// collectSummaryParams reads team summary files and returns their summary
// and category rows, and how many existing files could not be read.
func (a *ImportActivities) collectSummaryParams(ctx context.Context, teams []TeamInfo, date time.Time, origins core.OriginCounts) (summaryRows, int, error) {
	var rows summaryRows
	readFailures := 0
	pgDate := pgtype.Date{Time: date, Valid: true}

	for _, teamInfo := range teams {
		summaryRes := resource.TeamSummary{LeagueID: teamInfo.LeagueID, TeamID: teamInfo.TeamID, Date: date}
		if !resource.Exists(ctx, a.Storage, summaryRes) {
			continue
		}

		fantasy, origin, err := a.GobCache.ReadParsedCached(ctx, a.Storage, summaryRes)
		if err != nil {
			log.Debug().Err(err).
				Int("teamID", teamInfo.TeamID).
				Str("date", date.Format(config.DateFormat)).
				Msg("Failed to read summary file")
			readFailures++
			continue
		}
		origins.Record(origin)

		team := fantasy.Team
		teamStats := team.TeamStats

		p := sqlcdb.UpsertYahooTeamSummaryBatchParams{
			LeagueID:     int32(teamInfo.LeagueID),
			TeamID:       int32(teamInfo.TeamID),
			Date:         pgDate,
			CoverageType: teamStats.CoverageType,
		}

		for _, stat := range teamStats.Stats.Slice {
			statID, err := strconv.Atoi(stat.StatID)
			if err != nil {
				continue
			}
			val := parseStatValue(stat.Value)
			assignStatField(&p, statID, val)
		}
		set, err := summaryStatSet(p.LeagueID, p.TeamID, pgDate, teamStats.Stats.Slice)
		if err != nil {
			return rows, readFailures, err
		}

		rows.summaries = append(rows.summaries, p)
		rows.stats = append(rows.stats, set)
	}

	return rows, readFailures, nil
}

// parseStatValue converts a Yahoo stat value string to a nullable float.
// Yahoo uses "-" for stats with no data (e.g., goalie stats for a skater-only day).
func parseStatValue(s string) pgtype.Float4 {
	if s == "-" || s == "" {
		return pgtype.Float4{}
	}
	v, err := strconv.ParseFloat(s, 32)
	if err != nil {
		return pgtype.Float4{}
	}
	return pgtype.Float4{Float32: float32(v), Valid: true}
}

// assignStatField maps a Yahoo stat ID to the corresponding struct field.
func assignStatField(p *sqlcdb.UpsertYahooTeamSummaryBatchParams, statID int, val pgtype.Float4) {
	switch statID {
	case 1:
		p.Goals = val
	case 2:
		p.Assists = val
	case 3:
		p.Points = val
	case 4:
		p.PlusMinus = val
	case 5:
		p.PIM = val
	case 8:
		p.PPP = val
	case 14:
		p.SOG = val
	case 16:
		p.FaceoffsWon = val
	case 17:
		p.FaceoffsLost = val
	case 19:
		p.Wins = val
	case 22:
		p.GoalsAgainst = val
	case 23:
		p.GAA = val
	case 24:
		p.ShotsAgainst = val
	case 25:
		p.Saves = val
	case 26:
		p.SavePct = val
	case 27:
		p.Shutouts = val
	case 29:
		p.SHP = val
	case 30:
		p.GWG = val
	case 31:
		p.Hits = val
	case 32:
		p.Blocks = val
	}
}

// upsertSummaries batch upserts team summary records.
func upsertSummaries(ctx context.Context, queries YahooDataUpserter, params []sqlcdb.UpsertYahooTeamSummaryBatchParams) error {
	return shared.ExecBatch(queries.UpsertYahooTeamSummaryBatch(ctx, params), func(i int) string {
		return fmt.Sprintf("summary league_id=%d team_id=%d date=%s",
			params[i].LeagueID, params[i].TeamID, params[i].Date.Time.Format(config.DateFormat))
	})
}

// replaceSummaryStats replaces each team summary's category rows.
func replaceSummaryStats(ctx context.Context, queries YahooDataUpserter, params []sqlcdb.ReplaceYahooTeamSummaryStatsBatchParams) error {
	return shared.ExecBatch(queries.ReplaceYahooTeamSummaryStatsBatch(ctx, params), func(i int) string {
		return fmt.Sprintf("summary stats league_id=%d team_id=%d date=%s",
			params[i].LeagueID, params[i].TeamID, params[i].Date.Time.Format(config.DateFormat))
	})
}

// noStatValue is the value Yahoo serves for a category without data (goalie
// categories on a day no goalie played, for instance).
const noStatValue = "-"

// summaryStat is one element of ReplaceYahooTeamSummaryStatsBatch's stats
// JSON array.
type summaryStat struct {
	StatID   int      `json:"stat_id"`
	Value    *float64 `json:"value"`
	RawValue string   `json:"raw_value"`
}

// summaryStatSet is the ReplaceYahooTeamSummaryStatsBatch row of one team
// summary: every category of stats with an integer stat ID, sorted by stat
// ID. A stat ID listed twice keeps its last value, as the fixed columns do
// (assignStatField); a stat ID that is not a non-negative integer is logged
// and left out, as the fixed columns leave it out.
func summaryStatSet(leagueID, teamID int32, date pgtype.Date, stats []store.Stat) (sqlcdb.ReplaceYahooTeamSummaryStatsBatchParams, error) {
	byID := make(map[int]summaryStat, len(stats))
	for _, stat := range stats {
		statID, err := strconv.Atoi(stat.StatID)
		if err != nil || statID < 0 {
			log.Warn().Int32("leagueID", leagueID).Int32("teamID", teamID).Str("statID", stat.StatID).
				Msg("Skipping Yahoo summary stat without a non-negative integer stat ID")
			continue
		}
		byID[statID] = summaryStat{StatID: statID, Value: summaryStatNumber(stat.Value), RawValue: stat.Value}
	}
	set := make([]summaryStat, 0, len(byID))
	for _, stat := range byID {
		set = append(set, stat)
	}
	slices.SortFunc(set, func(a, b summaryStat) int { return cmp.Compare(a.StatID, b.StatID) })
	encoded, err := json.Marshal(set)
	if err != nil {
		return sqlcdb.ReplaceYahooTeamSummaryStatsBatchParams{}, fmt.Errorf("encode stats of league %d team %d: %w", leagueID, teamID, err)
	}
	return sqlcdb.ReplaceYahooTeamSummaryStatsBatchParams{
		LeagueID: leagueID, TeamID: teamID, Date: date, Stats: encoded,
	}, nil
}

// summaryStatNumber is a Yahoo stat value as a number, nil when Yahoo serves
// none or a text that is not a finite number (raw_value keeps the text).
func summaryStatNumber(raw string) *float64 {
	if raw == "" || raw == noStatValue {
		return nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}
