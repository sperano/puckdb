package workflow

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const (
	gameListKeptSeason        = 2024
	gameListUnavailableSeason = 2025
	gameListCurrentSeason     = 2026
	gameListCurrentGameKey    = 477
	gameListShortPagePlayers  = 3
	// gameListPreviousPlayers predicts five pages (four full and a short one).
	gameListPreviousPlayers = 4 * resource.GamePlayersPageSize
)

var errGameListPlan = errors.New("plan failed")

// gameListFakes serves the game player list activities: plans per season
// and, for a season with pages, the player count of each page in order.
type gameListFakes struct {
	seasons []int
	plans   map[int]yahoo.YahooGamePlayerListPlan
	planErr map[int]error
	pages   map[int][]int
	// fullPages makes every page of every season full, for the page cap.
	fullPages bool
	// endPastPages answers the offset after a season's last page with End,
	// as Yahoo's 400 does, instead of indexing past pages.
	endPastPages bool
	// pageErr fails the page at an offset of a season; commitErr a commit.
	pageErr   map[int]map[int]error
	commitErr map[int]error

	mu       sync.Mutex
	commits  []yahoo.CommitYahooGamePlayerListInput
	discards []yahoo.DiscardYahooGamePlayerListInput
	starts   map[int][]int
	// inFlight counts the seasons between their plan and their commit (or
	// failed page); maxInFlight is the most there ever were.
	inFlight, maxInFlight int
}

func (f *gameListFakes) enter() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inFlight++
	f.maxInFlight = max(f.maxInFlight, f.inFlight)
}

func (f *gameListFakes) register(env *testsuite.TestWorkflowEnvironment) {
	var act *yahoo.FetchActivities
	env.OnActivity(act.ListYahooNHLGameSeasons, mock.Anything).Return(f.seasons, nil)
	env.OnActivity(act.PlanYahooGamePlayerList, mock.Anything, mock.Anything).Return(
		func(_ context.Context, in yahoo.PlanYahooGamePlayerListInput) (yahoo.YahooGamePlayerListPlan, error) {
			if f.plans[in.Season].Refresh && f.planErr[in.Season] == nil {
				f.enter()
			}
			return f.plans[in.Season], f.planErr[in.Season]
		})
	env.OnActivity(act.FetchYahooGamePlayersPage, mock.Anything, mock.Anything).Return(
		func(_ context.Context, in yahoo.FetchYahooGamePlayersPageInput) (yahoo.FetchYahooGamePlayersPageResult, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.starts == nil {
				f.starts = map[int][]int{}
			}
			f.starts[in.Season] = append(f.starts[in.Season], in.Start)
			if err := f.pageErr[in.Season][in.Start]; err != nil {
				f.inFlight--
				return yahoo.FetchYahooGamePlayersPageResult{}, err
			}
			if f.fullPages {
				return yahoo.FetchYahooGamePlayersPageResult{Players: resource.GamePlayersPageSize}, nil
			}
			page := in.Start / resource.GamePlayersPageSize
			if f.endPastPages && page >= len(f.pages[in.Season]) {
				return yahoo.FetchYahooGamePlayersPageResult{End: true}, nil
			}
			return yahoo.FetchYahooGamePlayersPageResult{Players: f.pages[in.Season][page]}, nil
		})
	env.OnActivity(act.CommitYahooGamePlayerList, mock.Anything, mock.Anything).Return(
		func(_ context.Context, in yahoo.CommitYahooGamePlayerListInput) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.inFlight--
			if err := f.commitErr[in.Season]; err != nil {
				return err
			}
			f.commits = append(f.commits, in)
			return nil
		}).Maybe()
	env.OnActivity(act.DiscardYahooGamePlayerList, mock.Anything, mock.Anything).Return(
		func(_ context.Context, in yahoo.DiscardYahooGamePlayerListInput) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.discards = append(f.discards, in)
			return nil
		}).Maybe()
}

func runGameListWorkflow(t *testing.T, f *gameListFakes, input *FetchYahooPlayersInput) (*testsuite.TestWorkflowEnvironment, error) {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooPlayersWorkflow)
	f.register(env)
	env.ExecuteWorkflow(FetchYahooPlayersWorkflow, input)
	require.True(t, env.IsWorkflowCompleted())
	return env, env.GetWorkflowError()
}

// threeSeasonFakes: one season kept, one unavailable, the current one
// downloaded in three pages (two full, one short) with no previous list.
func threeSeasonFakes() *gameListFakes {
	return &gameListFakes{
		seasons: []int{gameListKeptSeason, gameListUnavailableSeason, gameListCurrentSeason},
		plans: map[int]yahoo.YahooGamePlayerListPlan{
			gameListKeptSeason:        {Reason: "final", GameKey: 453},
			gameListUnavailableSeason: {Unavailable: true, Reason: "no game"},
			gameListCurrentSeason:     {Refresh: true, GameKey: gameListCurrentGameKey},
		},
		pages: map[int][]int{gameListCurrentSeason: {
			resource.GamePlayersPageSize, resource.GamePlayersPageSize, gameListShortPagePlayers,
		}},
	}
}

func TestFetchYahooPlayersWorkflow_DownloadsDueGameListsOnly(t *testing.T) {
	f := threeSeasonFakes()

	env, err := runGameListWorkflow(t, f, nil)

	require.NoError(t, err)
	require.Len(t, f.commits, 1, "only the due season is committed")
	commit := f.commits[0]
	assert.Equal(t, gameListCurrentSeason, commit.Season)
	assert.Equal(t, gameListCurrentGameKey, commit.GameKey)
	assert.Equal(t, []int{0, resource.GamePlayersPageSize, 2 * resource.GamePlayersPageSize}, commit.Starts)
	assert.Equal(t, 2*resource.GamePlayersPageSize+gameListShortPagePlayers, commit.Players)
	assert.Equal(t, commit.FetchedAt.UnixMilli(), commit.DownloadID)
	assert.Empty(t, commit.Discarded)
	assert.Equal(t, commit.Starts, f.starts[gameListCurrentSeason], "no page past the short one is requested")
	assert.NotContains(t, f.starts, gameListKeptSeason)
	assert.Empty(t, f.discards, "a committed download discards nothing")

	report := queryProgress(t, env)
	group := report.Groups[GroupFetchYahooPlayers]
	assert.Contains(t, group.CompletedMsg, "Downloaded 1 of 3 Yahoo! game player lists in ")
	require.Len(t, group.Bars, 3)
	wantTotals := map[string]int{"2024": 1, "2025": 1, "2026": 1 + 3 + 1}
	for _, bar := range group.Bars {
		assert.Equal(t, wantTotals[bar.Label], bar.Total, bar.Label)
	}
	assert.Equal(t, 7, report.Total)
}

func TestFetchYahooPlayersWorkflow_ShrinksBarToPagesDownloaded(t *testing.T) {
	f := &gameListFakes{
		seasons: []int{gameListCurrentSeason},
		plans: map[int]yahoo.YahooGamePlayerListPlan{gameListCurrentSeason: {
			Refresh: true, GameKey: gameListCurrentGameKey, PreviousPlayerCount: gameListPreviousPlayers,
		}},
		pages: map[int][]int{gameListCurrentSeason: {resource.GamePlayersPageSize, gameListShortPagePlayers}},
	}

	env, err := runGameListWorkflow(t, f, nil)

	require.NoError(t, err)
	assert.Equal(t, []int{0, resource.GamePlayersPageSize}, f.starts[gameListCurrentSeason])
	report := queryProgress(t, env)
	assert.Equal(t, 1+2+1, report.Groups[GroupFetchYahooPlayers].Bars[0].Total,
		"the five estimated pages shrink to the two downloaded")
	assert.Equal(t, 1+2+1, report.Total)
}

// A list whose size is a multiple of the page size ends with the offset
// Yahoo rejects: committed without it, the bar shrunk to the pages stored.
func TestFetchYahooPlayersWorkflow_EndPastFullLastPageCommits(t *testing.T) {
	f := &gameListFakes{
		seasons: []int{gameListCurrentSeason},
		plans: map[int]yahoo.YahooGamePlayerListPlan{gameListCurrentSeason: {
			Refresh: true, GameKey: gameListCurrentGameKey, PreviousPlayerCount: gameListPreviousPlayers,
		}},
		pages:        map[int][]int{gameListCurrentSeason: {resource.GamePlayersPageSize, resource.GamePlayersPageSize}},
		endPastPages: true,
	}

	env, err := runGameListWorkflow(t, f, nil)

	require.NoError(t, err)
	require.Len(t, f.commits, 1)
	commit := f.commits[0]
	assert.Equal(t, []int{0, resource.GamePlayersPageSize}, commit.Starts, "the rejected offset is not named")
	assert.Equal(t, 2*resource.GamePlayersPageSize, commit.Players)
	assert.Equal(t, []int{0, resource.GamePlayersPageSize, 2 * resource.GamePlayersPageSize},
		f.starts[gameListCurrentSeason])
	assert.Empty(t, f.discards)
	report := queryProgress(t, env)
	bar := report.Groups[GroupFetchYahooPlayers].Bars[0]
	assert.Equal(t, 1+2+1, bar.Total, "the five estimated pages shrink to the two stored")
	assert.Equal(t, bar.Total, bar.Current)
}

func TestFetchYahooPlayersWorkflow_SeasonFailureLetsOthersCommit(t *testing.T) {
	f := threeSeasonFakes()
	f.planErr = map[int]error{gameListKeptSeason: errGameListPlan}

	env, err := runGameListWorkflow(t, f, nil)

	require.ErrorContains(t, err, "fetch Yahoo game player list 2024")
	require.ErrorContains(t, err, errGameListPlan.Error())
	require.Len(t, f.commits, 1, "the current season is still committed")
	assert.Equal(t, gameListCurrentSeason, f.commits[0].Season)
	group := queryProgress(t, env).Groups[GroupFetchYahooPlayers]
	assert.Zero(t, group.CompletedAt, "a failed run leaves the group open")
	// Open, the bars show the progress each season made.
	assert.Equal(t, []shared.ProgressBar{
		{Label: "2024", Total: 1, Started: true},
		{Label: "2025", Total: 1, Current: 1, Started: true},
		{Label: "2026", Total: 1 + 3 + 1, Current: 1 + 3 + 1, Started: true},
	}, group.Bars)
}

func TestFetchYahooPlayersWorkflow_EndlessListStopsAtPageCap(t *testing.T) {
	f := &gameListFakes{
		seasons:   []int{gameListCurrentSeason},
		plans:     map[int]yahoo.YahooGamePlayerListPlan{gameListCurrentSeason: {Refresh: true, GameKey: gameListCurrentGameKey}},
		fullPages: true,
	}

	_, err := runGameListWorkflow(t, f, nil)

	require.ErrorContains(t, err, "list has more than 200 pages")
	assert.Len(t, f.starts[gameListCurrentSeason], yahoo.MaxGamePlayerListPages)
	assert.Empty(t, f.commits)
	require.Len(t, f.discards, 1, "the capped download's pages are discarded")
	assert.Equal(t, f.starts[gameListCurrentSeason], f.discards[0].Starts)
}

func TestFetchYahooPlayersWorkflow_ListErrorFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooPlayersWorkflow)
	var act *yahoo.FetchActivities
	env.OnActivity(act.ListYahooNHLGameSeasons, mock.Anything).Return(nil, errGameListPlan)

	env.ExecuteWorkflow(FetchYahooPlayersWorkflow, (*FetchYahooPlayersInput)(nil))

	require.True(t, env.IsWorkflowCompleted())
	require.ErrorContains(t, env.GetWorkflowError(), "list Yahoo NHL games")
}

// TestFetchYahooPlayersWorkflow_ScanInputRunsGameLists checks that an
// execution a scan continued into (its input carries the scan's position and
// config) downloads the game lists instead of scanning.
func TestFetchYahooPlayersWorkflow_ScanInputRunsGameLists(t *testing.T) {
	f := threeSeasonFakes()
	input := &FetchYahooPlayersInput{
		StartPlayerID: legacyScanPlayersPerExecution + 1, TotalCompleted: legacyScanPlayersPerExecution,
		StartedAt: time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC),
		Config:    &FetchYahooPlayersScanConfig{MaxPlayerID: legacyScanMaxPlayerID, Concurrency: 1, ActivityBatchSize: 1, PlayersPerExecution: 1},
	}

	_, err := runGameListWorkflow(t, f, input)

	require.NoError(t, err, "no FetchYahooPlayerBatch is mocked, so a scan would fail")
	require.Len(t, f.commits, 1)
}

func TestGameListPages(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 1, gameListPages(0), "no previous list: one page")
	assert.Equal(t, 1, gameListPages(resource.GamePlayersPageSize-1))
	assert.Equal(t, 2, gameListPages(resource.GamePlayersPageSize), "a full last page is followed by an empty one")
	assert.Equal(t, 65, gameListPages(1613))
}
