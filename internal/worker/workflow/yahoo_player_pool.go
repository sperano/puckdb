package workflow

import (
	"fmt"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"go.temporal.io/sdk/workflow"
)

// fetchYahooPlayerPools downloads the draftable player pool of every league
// that calls the Yahoo API (stand-in leagues make no Yahoo calls).
func fetchYahooPlayerPools(ctx workflow.Context, startYear int, leagues []config.League,
	progress yahooSeasonProgress) ([]string, error) {
	var unavailable []string
	for i, league := range leagues {
		if league.UsesTemporaryMetadata() {
			continue
		}
		bar := yahooLeagueBar{progress: progress, index: i}
		missing, err := fetchYahooPlayerPool(ctx, startYear, league.LeagueID, bar)
		if err != nil {
			return unavailable, fmt.Errorf("fetch Yahoo player pool %d/%d: %w", startYear, league.LeagueID, err)
		}
		if missing {
			unavailable = append(unavailable, fmt.Sprintf("league %d player pool", league.LeagueID))
		}
	}
	return unavailable, nil
}

// fetchYahooPlayerPool downloads one page per activity (each is throttled
// like every Yahoo download) until a short page, then commits the manifest
// that makes the pages the league's current snapshot. The download ID, taken
// from the workflow clock, keeps each download's pages apart. The plan, every
// page, and the commit each advance the league's bar; the page estimate comes
// from the previous snapshot's size and is corrected by the short page.
func fetchYahooPlayerPool(ctx workflow.Context, startYear, leagueID int, bar yahooLeagueBar) (bool, error) {
	var act *yahoo.FetchActivities
	var plan yahoo.YahooLeaguePlayerPoolPlan
	planInput := yahoo.PlanYahooLeaguePlayerPoolInput{Season: startYear, LeagueID: leagueID}
	if err := workflow.ExecuteActivity(ctx, act.PlanYahooLeaguePlayerPool, planInput).Get(ctx, &plan); err != nil {
		return false, err
	}
	bar.advance(ctx)
	pages := yahooPoolPages(plan.PreviousPlayerCount)
	bar.resize(ctx, yahooPoolUnits(plan.Refresh, pages)-yahooPoolUnits(true, yahooPoolPages(0)))
	if !plan.Refresh {
		workflow.GetLogger(ctx).Info("Skipping Yahoo player pool download",
			"season", startYear, "leagueID", leagueID, "reason", plan.Reason)
		return false, nil
	}
	fetchedAt := workflow.Now(ctx)
	commit := yahoo.CommitYahooLeaguePlayerPoolInput{
		Season: startYear, LeagueID: leagueID, DownloadID: fetchedAt.UnixMilli(), FetchedAt: fetchedAt,
	}
	unavailable, err := fetchYahooPoolPages(ctx, &commit, bar.estimate(pages))
	if err != nil {
		return false, err
	}
	if unavailable {
		return true, nil
	}
	if err := workflow.ExecuteActivity(ctx, act.CommitYahooLeaguePlayerPool, commit).Get(ctx, nil); err != nil {
		return false, err
	}
	bar.advance(ctx)
	return false, nil
}

// fetchYahooPoolPages downloads pages until a short one, recording each in
// commit. On failure the page estimate is left as is, so the bar stays
// incomplete.
func fetchYahooPoolPages(ctx workflow.Context, commit *yahoo.CommitYahooLeaguePlayerPoolInput,
	pages *yahooEstimate) (bool, error) {
	var act *yahoo.FetchActivities
	for page := 0; ; page++ {
		if page == yahoo.MaxLeaguePlayerPoolPages {
			return false, fmt.Errorf("pool has more than %d pages", yahoo.MaxLeaguePlayerPoolPages)
		}
		pages.beforeCall(ctx)
		start := page * resource.LeaguePlayersPageSize
		var result yahoo.FetchYahooLeaguePlayersPageResult
		pageInput := yahoo.FetchYahooLeaguePlayersPageInput{
			Season: commit.Season, LeagueID: commit.LeagueID, DownloadID: commit.DownloadID, Start: start,
		}
		if err := workflow.ExecuteActivity(ctx, act.FetchYahooLeaguePlayersPage, pageInput).Get(ctx, &result); err != nil {
			return false, err
		}
		pages.afterCall(ctx)
		if result.Unavailable {
			pages.finish(ctx)
			pages.bar.resize(ctx, -yahooPoolCommitUnits)
			return true, nil
		}
		commit.LeagueKey, commit.GameKey = result.LeagueKey, result.GameKey
		commit.Starts = append(commit.Starts, start)
		commit.Players += result.Players
		if result.Players < resource.LeaguePlayersPageSize {
			pages.finish(ctx)
			return false, nil
		}
	}
}

// importYahooPlayerPools imports each API league's latest pool snapshot and
// returns the leagues that have none yet.
func importYahooPlayerPools(ctx workflow.Context, startYear int, leagues []config.League,
	progress yahooSeasonProgress) ([]string, error) {
	var act *yahoo.ImportActivities
	var unavailable []string
	for i, league := range leagues {
		if league.UsesTemporaryMetadata() {
			continue
		}
		input := yahoo.ImportYahooLeaguePlayersInput{Season: startYear, LeagueID: league.LeagueID}
		var result yahoo.ImportYahooLeaguePlayersResult
		if err := workflow.ExecuteActivity(ctx, act.ImportYahooLeaguePlayers, input).Get(ctx, &result); err != nil {
			return unavailable, fmt.Errorf("import Yahoo player pool %d/%d: %w", startYear, league.LeagueID, err)
		}
		if result.Unavailable {
			unavailable = append(unavailable, fmt.Sprintf("league %d player pool", league.LeagueID))
		}
		progress.advance(ctx, i)
	}
	return unavailable, nil
}
