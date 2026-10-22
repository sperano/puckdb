package yahoo

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/worker/shared"
)

// DateInputs lists the cached files ImportYahooDataForDate reads for the
// date: each team's roster and summary, then each league's standings
// snapshot, in the order of input.Teams.
func DateInputs(input ImportYahooDataForDateInput) []core.Resource {
	inputs := make([]core.Resource, 0, 2*len(input.Teams))
	for _, team := range input.Teams {
		inputs = append(inputs,
			resource.Roster{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: input.Date},
			resource.TeamSummary{LeagueID: team.LeagueID, TeamID: team.TeamID, Date: input.Date},
		)
	}
	for _, leagueID := range teamLeagueIDs(input.Teams) {
		inputs = append(inputs, standingsResource(input.Season, leagueID, input.Date))
	}
	return inputs
}

// teamLeagueIDs is the distinct leagues of teams, in order of appearance.
func teamLeagueIDs(teams []TeamInfo) []int {
	var leagueIDs []int
	seen := make(map[int]struct{})
	for _, team := range teams {
		if _, ok := seen[team.LeagueID]; ok {
			continue
		}
		seen[team.LeagueID] = struct{}{}
		leagueIDs = append(leagueIDs, team.LeagueID)
	}
	return leagueIDs
}

// standingsResource is the standings snapshot of a league captured on date.
// It has no game key: the key only builds the request URL and is checked when
// the capture is fetched, so the import does not verify it again.
func standingsResource(season, leagueID int, date time.Time) resource.LeagueStandings {
	return resource.LeagueStandings{Season: season, LeagueID: leagueID, Date: date}
}

// leagueStandingRows is what one league's standings snapshot of a date
// imports: the rows of the league's teams in teams, by team.
type leagueStandingRows struct {
	leagueID int
	rows     []sqlcdb.UpsertYahooTeamStandingBatchParams
}

// collectStandingParams reads the standings snapshot each league of teams
// captured on date, and returns the rows of the teams in teams and how many
// existing snapshot files could not be read. A league without a snapshot for
// the date has no entry: its rows are left as they are, never filled from
// another day's capture.
//
// The file is parsed straight from storage, without Redis: standings take the
// coherent resource lock (cache.RequiresCoherentResourceLock), which a
// storage-first read (ImportDayIfChanged) would take on every read.
func (a *ImportActivities) collectStandingParams(ctx context.Context, input ImportYahooDataForDateInput, origins core.OriginCounts) ([]leagueStandingRows, int) {
	var leagues []leagueStandingRows
	readFailures := 0
	for _, leagueID := range teamLeagueIDs(input.Teams) {
		res := standingsResource(input.Season, leagueID, input.Date)
		if !resource.Exists(ctx, a.Storage, res) {
			continue
		}
		rows, err := a.readLeagueStandings(ctx, res, input.Teams)
		if err != nil {
			log.Warn().Err(err).Int("leagueID", leagueID).
				Str("date", input.Date.Format(config.DateFormat)).
				Msg("Failed to read standings snapshot")
			readFailures++
			continue
		}
		origins.Record(core.OriginFileSystem)
		leagues = append(leagues, leagueStandingRows{leagueID: leagueID, rows: rows})
	}
	return leagues, readFailures
}

// readLeagueStandings converts one league's snapshot file into the rows of
// its teams in teams. Teams of the file outside teams are left out: their
// yahoo_teams rows are not imported.
func (a *ImportActivities) readLeagueStandings(ctx context.Context, res resource.LeagueStandings, teams []TeamInfo) ([]sqlcdb.UpsertYahooTeamStandingBatchParams, error) {
	// Stat before reading: a file rewritten in between pairs the older
	// capture time with the newer content, and the day's fingerprint, taken
	// before both, has the older file, so the next run imports it again.
	info, err := resource.Stat(ctx, a.Storage, res)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", res.Path(), err)
	}
	fantasy, err := resource.ReadParsed(ctx, a.Storage, res)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", res.Path(), err)
	}
	records, err := resource.TeamStandingRecords(fantasy.League)
	if err != nil {
		return nil, fmt.Errorf("convert %s: %w", res.Path(), err)
	}
	wanted := make(map[int]struct{}, len(teams))
	for _, team := range teams {
		if team.LeagueID == res.LeagueID {
			wanted[team.TeamID] = struct{}{}
		}
	}
	date := pgtype.Date{Time: res.Date, Valid: true}
	capturedAt := pgtype.Timestamptz{Time: info.ModTime().UTC(), Valid: true}
	var rows []sqlcdb.UpsertYahooTeamStandingBatchParams
	for _, record := range records {
		if _, ok := wanted[record.TeamID]; !ok {
			continue
		}
		row, err := standingParams(res.LeagueID, date, capturedAt, record)
		if err != nil {
			return nil, fmt.Errorf("convert %s: %w", res.Path(), err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// standingParams is the row of one team's standing record. A rank or seed
// Yahoo left empty (0) is NULL, as is a record the league does not keep.
func standingParams(leagueID int, date pgtype.Date, capturedAt pgtype.Timestamptz, record resource.TeamStandingRecord) (sqlcdb.UpsertYahooTeamStandingBatchParams, error) {
	if err := standingCountsFit(record); err != nil {
		return sqlcdb.UpsertYahooTeamStandingBatchParams{}, err
	}
	row := sqlcdb.UpsertYahooTeamStandingBatchParams{
		LeagueID:             int32(leagueID),
		TeamID:               int32(record.TeamID),
		Date:                 date,
		StandingsCapturedAt:  capturedAt,
		StandingsRank:        positiveInt4(record.Rank),
		StandingsPlayoffSeed: positiveInt4(record.PlayoffSeed),
	}
	if outcome := record.Outcome; outcome != nil {
		percentage, err := outcomePercentage(outcome)
		if err != nil {
			return row, fmt.Errorf("team %d outcome_totals: %w", record.TeamID, err)
		}
		row.H2hWins = countInt4(outcome.Wins)
		row.H2hLosses = countInt4(outcome.Losses)
		row.H2hTies = countInt4(outcome.Ties)
		row.H2hPercentage = percentage
	}
	if outcome := record.DivisionalOutcome; outcome != nil {
		percentage, err := outcomePercentage(outcome)
		if err != nil {
			return row, fmt.Errorf("team %d divisional_outcome_totals: %w", record.TeamID, err)
		}
		row.H2hDivisionWins = countInt4(outcome.Wins)
		row.H2hDivisionLosses = countInt4(outcome.Losses)
		row.H2hDivisionTies = countInt4(outcome.Ties)
		row.H2hDivisionPercentage = percentage
	}
	return row, nil
}

// standingCountsFit refuses a rank, seed or record count an integer column
// cannot hold, so such a file fails only its own league's snapshot instead of
// the whole batch.
func standingCountsFit(record resource.TeamStandingRecord) error {
	counts := []int{record.Rank, record.PlayoffSeed}
	for _, outcome := range []*resource.OutcomeRecord{record.Outcome, record.DivisionalOutcome} {
		if outcome != nil {
			counts = append(counts, outcome.Wins, outcome.Losses, outcome.Ties)
		}
	}
	for _, n := range counts {
		if n > math.MaxInt32 {
			return fmt.Errorf("team %d: %d does not fit an integer column", record.TeamID, n)
		}
	}
	return nil
}

// Bounds of a winning percentage, a fraction of the games played.
const (
	minOutcomePercentage = 0
	maxOutcomePercentage = 1
)

// outcomePercentage is a record's percentage, NULL when Yahoo serves none.
// One outside [0, 1] is refused here rather than by the column's CHECK, so a
// bad file fails only its own league's snapshot.
func outcomePercentage(outcome *resource.OutcomeRecord) (pgtype.Float8, error) {
	if outcome.Percentage == nil {
		return pgtype.Float8{}, nil
	}
	p := *outcome.Percentage
	if p < minOutcomePercentage || p > maxOutcomePercentage {
		return pgtype.Float8{}, fmt.Errorf("percentage %g is outside [%d, %d]", p, minOutcomePercentage, maxOutcomePercentage)
	}
	return pgtype.Float8{Float64: p, Valid: true}, nil
}

// positiveInt4 is n, or NULL when n is 0 (Yahoo served none).
func positiveInt4(n int) pgtype.Int4 {
	return pgtype.Int4{Int32: int32(n), Valid: n > 0}
}

// countInt4 is a count Yahoo served, 0 included.
func countInt4(n int) pgtype.Int4 {
	return pgtype.Int4{Int32: int32(n), Valid: true}
}

// importStandings writes each league's standings rows and clears the
// standings of the league's other teams on that date, so a re-captured file
// replaces the whole snapshot. It returns how many team rows it wrote.
func importStandings(ctx context.Context, queries YahooDataUpserter, date time.Time, leagues []leagueStandingRows) (int, error) {
	var rows []sqlcdb.UpsertYahooTeamStandingBatchParams
	for _, league := range leagues {
		rows = append(rows, league.rows...)
	}
	if len(rows) > 0 {
		if err := shared.ExecBatch(queries.UpsertYahooTeamStandingBatch(ctx, rows), func(i int) string {
			return fmt.Sprintf("standing league_id=%d team_id=%d date=%s",
				rows[i].LeagueID, rows[i].TeamID, rows[i].Date.Time.Format(config.DateFormat))
		}); err != nil {
			return 0, err
		}
	}
	pgDate := pgtype.Date{Time: date, Valid: true}
	for _, league := range leagues {
		// Never nil: a nil array is NULL in SQL, and "<> ALL(NULL)" would
		// clear nothing instead of every other team.
		teamIDs := make([]int32, 0, len(league.rows))
		for _, row := range league.rows {
			teamIDs = append(teamIDs, row.TeamID)
		}
		if err := queries.ClearYahooTeamStandingsExcept(ctx, sqlcdb.ClearYahooTeamStandingsExceptParams{
			LeagueID: int32(league.leagueID), Date: pgDate, TeamIds: teamIDs,
		}); err != nil {
			return 0, fmt.Errorf("clear stale standings of league %d on %s: %w",
				league.leagueID, date.Format(config.DateFormat), err)
		}
	}
	return len(rows), nil
}
