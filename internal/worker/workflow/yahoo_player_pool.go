package workflow

import (
	"errors"
	"fmt"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"go.temporal.io/sdk/workflow"
)

// yahooPoolLeaguesChangeID gates fetching the leagues' player pools
// concurrently. Histories recorded before it fetched them one after another
// and carry no marker for it, so they replay the sequential loop.
const (
	yahooPoolLeaguesChangeID          = "yahoo-pool-concurrent-leagues"
	yahooPoolLeaguesConcurrentVersion = 1
)

// fetchYahooPlayerPools downloads the draftable player pool of every league
// that calls the Yahoo API (stand-in leagues make no Yahoo calls).
func fetchYahooPlayerPools(ctx workflow.Context, startYear int, leagues []config.League,
	progress yahooSeasonProgress) ([]string, error) {
	version := workflow.GetVersion(ctx, yahooPoolLeaguesChangeID, workflow.DefaultVersion,
		yahooPoolLeaguesConcurrentVersion)
	if version == workflow.DefaultVersion {
		return fetchYahooPlayerPoolsSequentially(ctx, startYear, leagues, progress)
	}
	concurrency, err := shared.SnapshotConfigInt(ctx, workflow.GetLogger(ctx), shared.YahooPoolLeagueConcurrencyParam, nil)
	if err != nil {
		return nil, err
	}
	return fetchYahooPlayerPoolsConcurrently(ctx, startYear, leagues, progress, concurrency)
}

// yahooPoolOutcome is one league's pool download result.
type yahooPoolOutcome struct {
	unavailable bool
	err         error
}

// fetchYahooPlayerPoolsConcurrently downloads up to concurrency leagues'
// pools at a time. A league's failure neither cancels nor skips the others,
// so every other league still commits its manifest; the returned error joins
// each failed league's error, in configuration order.
func fetchYahooPlayerPoolsConcurrently(ctx workflow.Context, startYear int, leagues []config.League,
	progress yahooSeasonProgress, concurrency int) ([]string, error) {
	outcomes := make([]yahooPoolOutcome, len(leagues))
	slots := workflow.NewSemaphore(ctx, int64(concurrency))
	pending := workflow.NewWaitGroup(ctx)
	var canceled error
	for i, league := range leagues {
		if league.UsesTemporaryMetadata() {
			continue
		}
		if canceled = slots.Acquire(ctx, 1); canceled != nil {
			break
		}
		pending.Add(1)
		workflow.Go(ctx, func(ctx workflow.Context) {
			defer pending.Done()
			defer slots.Release(1)
			bar := yahooLeagueBar{progress: progress, index: i}
			outcomes[i].unavailable, outcomes[i].err = fetchYahooPlayerPool(ctx, startYear, league.LeagueID, bar)
		})
	}
	pending.Wait(ctx)
	unavailable, err := collectYahooPoolOutcomes(startYear, leagues, outcomes)
	return unavailable, errors.Join(canceled, err)
}

// collectYahooPoolOutcomes lists, in configuration order, the leagues whose
// pool Yahoo has not published and the error of every league that failed.
func collectYahooPoolOutcomes(startYear int, leagues []config.League, outcomes []yahooPoolOutcome) ([]string, error) {
	var unavailable []string
	var errs []error
	for i, outcome := range outcomes {
		leagueID := leagues[i].LeagueID
		if outcome.err != nil {
			errs = append(errs, fmt.Errorf("fetch Yahoo player pool %d/%d: %w", startYear, leagueID, outcome.err))
		}
		if outcome.unavailable {
			unavailable = append(unavailable, fmt.Sprintf("league %d player pool", leagueID))
		}
	}
	return unavailable, errors.Join(errs...)
}

// fetchYahooPlayerPoolsSequentially is the loop executions recorded before
// yahooPoolLeaguesChangeID replay: one league after another, stopping at the
// first failure.
func fetchYahooPlayerPoolsSequentially(ctx workflow.Context, startYear int, leagues []config.League,
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
