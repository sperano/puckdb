package yahoo

import (
	"context"
	"time"

	"github.com/sperano/puckdb/internal/sqlcdb"
)

// dateRows is everything ImportYahooDataForDate writes for one date.
type dateRows struct {
	date      time.Time
	summaries summaryRows
	standings []leagueStandingRows
	rosters   []sqlcdb.UpsertYahooTeamRosterBatchParams
}

// empty reports whether the date has nothing to write: no summary, no
// roster, and no league standings snapshot (whose clearing of the other
// teams is a write even without rows).
func (r dateRows) empty() bool {
	return len(r.summaries.summaries) == 0 && len(r.standings) == 0 && len(r.rosters) == 0
}

// dateCounts is how many rows of each kind writeDateRows wrote.
type dateCounts struct {
	summaries, standings, rosters int
}

// writeDateRows writes a date's summaries (their fixed category columns, then
// their category rows, which reference the summary rows), its standings and
// its rosters. ImportYahooDataForDate runs it in one transaction, so a reader
// never sees a summary whose category rows or standings belong to another
// import, and a failure leaves the date as it was until the retry.
func writeDateRows(ctx context.Context, q YahooDataUpserter, rows dateRows) (dateCounts, error) {
	var counts dateCounts
	if len(rows.summaries.summaries) > 0 {
		if err := upsertSummaries(ctx, q, rows.summaries.summaries); err != nil {
			return dateCounts{}, err
		}
		if err := replaceSummaryStats(ctx, q, rows.summaries.stats); err != nil {
			return dateCounts{}, err
		}
		counts.summaries = len(rows.summaries.summaries)
	}
	standings, err := importStandings(ctx, q, rows.date, rows.standings)
	if err != nil {
		return dateCounts{}, err
	}
	counts.standings = standings
	if len(rows.rosters) > 0 {
		if err := upsertRosters(ctx, q, rows.rosters); err != nil {
			return dateCounts{}, err
		}
		counts.rosters = len(rows.rosters)
	}
	return counts, nil
}
