package workflow

import (
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

// gameListOtherDueSeason is a second season whose list is due.
const gameListOtherDueSeason = 2025

// Non-retryable, so each failing activity runs once.
var (
	errGameListPage   = temporal.NewNonRetryableApplicationError("page failed", "test", nil)
	errGameListCommit = temporal.NewNonRetryableApplicationError("commit failed", "test", nil)
)

// twoDueSeasonFakes: two due seasons, each downloaded in two pages (one
// full, one short).
func twoDueSeasonFakes() *gameListFakes {
	twoPages := []int{resource.GamePlayersPageSize, gameListShortPagePlayers}
	return &gameListFakes{
		seasons: []int{gameListOtherDueSeason, gameListCurrentSeason},
		plans: map[int]yahoo.YahooGamePlayerListPlan{
			gameListOtherDueSeason: {Refresh: true, GameKey: gameListCurrentGameKey - 1},
			gameListCurrentSeason:  {Refresh: true, GameKey: gameListCurrentGameKey},
		},
		pages: map[int][]int{gameListOtherDueSeason: twoPages, gameListCurrentSeason: twoPages},
	}
}

func TestFetchYahooPlayersWorkflow_PageFailureDiscardsSeasonDownload(t *testing.T) {
	f := twoDueSeasonFakes()
	f.pageErr = map[int]map[int]error{gameListCurrentSeason: {resource.GamePlayersPageSize: errGameListPage}}

	env, err := runGameListWorkflow(t, f, nil)

	require.ErrorContains(t, err, "fetch Yahoo game player list 2026")
	require.ErrorContains(t, err, errGameListPage.Error())
	require.Len(t, f.commits, 1, "the other season is still committed")
	assert.Equal(t, gameListOtherDueSeason, f.commits[0].Season)
	assert.Equal(t, []int{0, resource.GamePlayersPageSize}, f.starts[gameListCurrentSeason],
		"no page is requested after the failed one")
	require.Len(t, f.discards, 1)
	assert.Equal(t, yahoo.DiscardYahooGamePlayerListInput{
		Season: gameListCurrentSeason, DownloadID: f.discards[0].DownloadID,
		Starts: []int{0, resource.GamePlayersPageSize},
	}, f.discards[0], "the failed page is discarded with the stored one")
	assert.Positive(t, f.discards[0].DownloadID)
	assert.Zero(t, queryProgress(t, env).Groups[GroupFetchYahooPlayers].CompletedAt)
}

func TestFetchYahooPlayersWorkflow_CommitFailureDiscardsSeasonDownload(t *testing.T) {
	f := twoDueSeasonFakes()
	f.commitErr = map[int]error{gameListCurrentSeason: errGameListCommit}

	env, err := runGameListWorkflow(t, f, nil)

	require.ErrorContains(t, err, "fetch Yahoo game player list 2026")
	require.ErrorContains(t, err, errGameListCommit.Error())
	require.Len(t, f.commits, 1, "the other season is still committed")
	assert.Equal(t, gameListOtherDueSeason, f.commits[0].Season)
	require.Len(t, f.discards, 1)
	assert.Equal(t, gameListCurrentSeason, f.discards[0].Season)
	assert.Equal(t, []int{0, resource.GamePlayersPageSize}, f.discards[0].Starts)
	group := queryProgress(t, env).Groups[GroupFetchYahooPlayers]
	assert.Zero(t, group.CompletedAt, "a failed run leaves the group open")
	assert.Equal(t, 1+2+1, group.Bars[1].Total)
	assert.Equal(t, 1+2, group.Bars[1].Current, "the failed commit does not advance the bar")
}

func TestFetchYahooPlayersWorkflow_SeasonConcurrencyLimit(t *testing.T) {
	setViperInt(t, config.FlagYahooGamePlayerListConcurrency, 1)
	seasons := []int{2023, 2024, 2025, 2026}
	f := &gameListFakes{seasons: seasons, plans: map[int]yahoo.YahooGamePlayerListPlan{}, pages: map[int][]int{}}
	for _, season := range seasons {
		f.plans[season] = yahoo.YahooGamePlayerListPlan{Refresh: true, GameKey: gameListCurrentGameKey}
		f.pages[season] = []int{gameListShortPagePlayers}
	}

	_, err := runGameListWorkflow(t, f, nil)

	require.NoError(t, err)
	require.Len(t, f.commits, len(seasons))
	assert.Equal(t, 1, f.maxInFlight, "one season at a time")
	for i, commit := range f.commits {
		assert.Equal(t, seasons[i], commit.Season, "seasons run in order")
	}
}
