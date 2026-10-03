package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// yahooSeasonSourceKey maps a start year to the child workflow whose progress
// report feeds that season's league bars in the parent.
func yahooSeasonSourceKey(mode seasonSyncMode) shared.ProgressSourceKeyFunc {
	if mode == seasonSyncImport {
		return WorkflowIDImportYahooSeason
	}
	return WorkflowIDFetchYahooSeason
}

// addYahooSeasonBars adds one bar per (season, league), labelled
// "<season> · <league ID>" and sized with the same estimate the child starts
// from. Each bar mirrors its league's bar in the child's report (same index),
// so the resolver shows the child's live Current and corrected Total. Returns
// each season's bars, in season order.
func addYahooSeasonBars(tracker *shared.ReportTracker, seasons []YahooSeasonWorkflowInput,
	mode seasonSyncMode) []shared.BarRange {
	sourceKey := yahooSeasonSourceKey(mode)
	ranges := make([]shared.BarRange, len(seasons))
	next := 0
	for i, season := range seasons {
		seasonLabel := nhl.SeasonInfo{ID: nhl.NewSeason(season.StartYear)}.Label()
		ranges[i] = shared.BarRange{Start: next, Count: len(season.Season.Leagues)}
		for leagueIdx, league := range season.Season.Leagues {
			tracker.AddBar(groupYahooMetadata, shared.ProgressBar{
				Label:             fmt.Sprintf("%s · %d", seasonLabel, league.LeagueID),
				Total:             yahooLeagueUnits(league, mode),
				ProgressSourceKey: sourceKey(season.StartYear),
				MirrorSourceBar:   true,
				ProgressSourceBar: leagueIdx,
			})
		}
		next += len(season.Season.Leagues)
	}
	return ranges
}

// applyYahooLeagueTotals sizes a season's league bars with the totals its
// child finished with, so completing them (Current = Total) saves the same
// numbers the child reported. A result without per-league totals (or with a
// mismatched count) leaves the estimates in place.
func applyYahooLeagueTotals(tracker *shared.ReportTracker, bars shared.BarRange, totals []int) {
	if len(totals) != bars.Count {
		return
	}
	for i, total := range totals {
		tracker.SetBarTotal(groupYahooMetadata, bars.Start+i, total)
	}
	tracker.RecalcTotal()
}

// cleanupStaleYahooReports drops child reports left by a previous run so the
// resolver never merges them into this run's bars.
func cleanupStaleYahooReports(ctx workflow.Context, seasons []YahooSeasonWorkflowInput, mode seasonSyncMode) {
	sourceKey := yahooSeasonSourceKey(mode)
	staleIDs := make([]string, len(seasons))
	for i, season := range seasons {
		staleIDs[i] = sourceKey(season.StartYear)
	}
	deleteStaleProgressReports(ctx, staleIDs)
}
