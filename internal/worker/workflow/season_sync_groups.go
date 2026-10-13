package workflow

import (
	"fmt"

	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// seasonSyncConcurrentGroupsChange gates running the Yahoo and NHL season
// groups of FetchSeasonsWorkflow at the same time. A history recorded before
// it has no marker, gets workflow.DefaultVersion and replays the serial order.
const (
	seasonSyncConcurrentGroupsChange  = "season-sync-concurrent-groups"
	seasonSyncConcurrentGroupsVersion = 1
)

// seasonSyncGroups runs the Yahoo season group and the NHL season group of
// one season sync.
//
// Dependencies between them, by mode:
//   - Fetch: none. FetchNHLSeasonWorkflow takes its per-day Yahoo team IDs
//     from the snapshotted Yahoo config and FetchDay resolves the Yahoo game
//     key itself (cache or download); neither reads what
//     FetchYahooSeasonWorkflow writes. Both groups only fill the file cache.
//   - Import: the NHL group needs the Yahoo group. ImportDay upserts
//     yahoo_team_rosters and yahoo_team_summaries, whose (league_id, team_id)
//     foreign keys point at the yahoo_teams rows ImportYahooSeasonWorkflow
//     imports. On an empty database the day loop would fail without them.
//
// Steps after the season sync that read Yahoo data (process-players, the
// import-seasons step, refresh-news, refresh-draft-rankings) run after the
// whole FetchSeasonsWorkflow returns, which happens only once both groups
// have finished.
type seasonSyncGroups struct {
	tracker      *shared.ReportTracker
	input        *model.SeasonsInput
	yahooSeasons []YahooSeasonWorkflowInput
	concurrency  int
	rangeLabel   string
	mode         seasonSyncMode
}

// seasonGroupsRunConcurrently reports whether this run starts the NHL group
// without waiting for the Yahoo group. Only the fetch has no dependency
// between them, so the import keeps the serial order and records no marker.
func seasonGroupsRunConcurrently(ctx workflow.Context, mode seasonSyncMode) bool {
	if mode != seasonSyncFetch {
		return false
	}
	version := workflow.GetVersion(ctx, seasonSyncConcurrentGroupsChange,
		workflow.DefaultVersion, seasonSyncConcurrentGroupsVersion)
	return version >= seasonSyncConcurrentGroupsVersion
}

// runSerially runs the Yahoo group, then the NHL group, and returns the
// number of NHL seasons. A Yahoo failure stops before the NHL group starts.
func (g seasonSyncGroups) runSerially(ctx workflow.Context) (int, error) {
	if err := g.runYahoo(ctx); err != nil {
		return 0, err
	}
	return g.runNHL(ctx, "Yahoo metadata finished, but NHL season selection failed")
}

// runConcurrently starts the NHL group in its own coroutine, runs the Yahoo
// group, and returns once both have finished, with the number of NHL seasons.
//
// A failed group does not cancel the other: what a fetch caches stays valid,
// and canceling would only make the next sync download it again. The Yahoo
// error is returned when there is one, as in the serial order, so a Yahoo
// failure fails the sync with the same error and progress message; an NHL
// error is returned otherwise. When both fail, the message names both.
func (g seasonSyncGroups) runConcurrently(ctx workflow.Context) (int, error) {
	g.tracker.SerializeSaves(ctx)
	nhlDone, settleNHL := workflow.NewFuture(ctx)
	workflow.Go(ctx, func(ctx workflow.Context) {
		count, err := g.runNHL(ctx, "NHL season selection failed")
		settleNHL.Set(count, err)
	})
	yahooErr := g.runYahoo(ctx)
	var nhlCount int
	nhlErr := nhlDone.Get(ctx, &nhlCount)
	switch {
	case yahooErr != nil && nhlErr != nil:
		g.tracker.SetMessage(ctx, fmt.Sprintf("Yahoo metadata and NHL data both failed for %s. Yahoo: %v. NHL: %v.",
			g.rangeLabel, yahooErr, nhlErr))
		return 0, yahooErr
	case yahooErr != nil:
		return 0, yahooErr
	case nhlErr != nil:
		return 0, nhlErr
	}
	return nhlCount, nil
}

func (g seasonSyncGroups) runYahoo(ctx workflow.Context) error {
	return processYahooSeasonGroup(ctx, g.tracker, g.yahooSeasons, g.concurrency, g.rangeLabel, g.mode)
}

// runNHL selects the started NHL seasons and runs their group. selectionFailure
// prefixes the progress message when the selection fails.
func (g seasonSyncGroups) runNHL(ctx workflow.Context, selectionFailure string) (int, error) {
	seasons, err := loadSeasonsManifest(ctx, workflow.GetLogger(ctx), g.input)
	if err != nil {
		g.tracker.SetMessage(ctx, fmt.Sprintf("%s for %s: %v", selectionFailure, g.rangeLabel, err))
		return 0, err
	}
	if err := processNHLSeasonGroup(ctx, g.tracker, seasons, g.concurrency, g.rangeLabel, g.mode); err != nil {
		return 0, err
	}
	return len(seasons), nil
}
