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
func fetchYahooPlayerPools(ctx workflow.Context, startYear int, leagues []config.League, step yahooStepFunc) error {
	for _, league := range leagues {
		if league.UsesTemporaryMetadata() {
			continue
		}
		if err := fetchYahooPlayerPool(ctx, startYear, league.LeagueID); err != nil {
			return fmt.Errorf("fetch Yahoo player pool %d/%d: %w", startYear, league.LeagueID, err)
		}
		step(ctx)
	}
	return nil
}

// fetchYahooPlayerPool downloads one page per activity (each is throttled
// like every Yahoo download) until a short page, then commits the manifest
// that makes the pages the league's current snapshot. The download ID, taken
// from the workflow clock, keeps each download's pages apart.
func fetchYahooPlayerPool(ctx workflow.Context, startYear, leagueID int) error {
	var act *yahoo.FetchActivities
	var plan yahoo.YahooLeaguePlayerPoolPlan
	planInput := yahoo.PlanYahooLeaguePlayerPoolInput{Season: startYear, LeagueID: leagueID}
	if err := workflow.ExecuteActivity(ctx, act.PlanYahooLeaguePlayerPool, planInput).Get(ctx, &plan); err != nil {
		return err
	}
	logger := workflow.GetLogger(ctx)
	if !plan.Refresh {
		logger.Info("Skipping Yahoo player pool download", "season", startYear, "leagueID", leagueID, "reason", plan.Reason)
		return nil
	}
	fetchedAt := workflow.Now(ctx)
	commit := yahoo.CommitYahooLeaguePlayerPoolInput{
		Season: startYear, LeagueID: leagueID, DownloadID: fetchedAt.UnixMilli(), FetchedAt: fetchedAt,
	}
	for page := 0; ; page++ {
		if page == yahoo.MaxLeaguePlayerPoolPages {
			return fmt.Errorf("pool has more than %d pages", yahoo.MaxLeaguePlayerPoolPages)
		}
		start := page * resource.LeaguePlayersPageSize
		var result yahoo.FetchYahooLeaguePlayersPageResult
		pageInput := yahoo.FetchYahooLeaguePlayersPageInput{
			Season: startYear, LeagueID: leagueID, DownloadID: commit.DownloadID, Start: start,
		}
		if err := workflow.ExecuteActivity(ctx, act.FetchYahooLeaguePlayersPage, pageInput).Get(ctx, &result); err != nil {
			return err
		}
		commit.LeagueKey, commit.GameKey = result.LeagueKey, result.GameKey
		commit.Starts = append(commit.Starts, start)
		commit.Players += result.Players
		if result.Players < resource.LeaguePlayersPageSize {
			break
		}
	}
	return workflow.ExecuteActivity(ctx, act.CommitYahooLeaguePlayerPool, commit).Get(ctx, nil)
}

// importYahooPlayerPools imports each API league's latest pool snapshot and
// returns the leagues that have none yet.
func importYahooPlayerPools(ctx workflow.Context, startYear int, leagues []config.League,
	step yahooStepFunc) ([]string, error) {
	var act *yahoo.ImportActivities
	var unavailable []string
	for _, league := range leagues {
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
		step(ctx)
	}
	return unavailable, nil
}
