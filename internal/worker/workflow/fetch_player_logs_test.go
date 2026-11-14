package workflow

import (
	"context"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/store"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/workflow"
)

// playerLogsTestPlayerBase is subtracted from a start year to give the
// season's cached boxscore players.
const playerLogsTestPlayerBase = 2020

func playerLogsTestSeasons() []nhl.SeasonInfo {
	return []nhl.SeasonInfo{
		testSeasonW(2022, "2022-10-07", "2022-10-09"),
		testSeasonW(2023, "2023-10-10", "2023-10-12"),
	}
}

func playerLogsTestPlayers(startYear int) int { return startYear - playerLogsTestPlayerBase }

// newFetchPlayerLogsParent stubs each season's boxscore players and each
// FetchSeasonPlayerLogsWorkflow child, recording its ID and input; the
// tests check which children started. Every boxscore player count must run
// after the stale report cleanup.
func newFetchPlayerLogsParent(t *testing.T, seasons []nhl.SeasonInfo) *fanOutParent {
	p := newFanOutParent(t, FetchPlayerLogsWorkflow, seasons)
	var pa *workplayer.Activities
	for _, s := range seasons {
		year := s.ID.StartYear()
		p.env.OnActivity(pa.LoadSeasonBoxscorePlayers, mock.Anything, year).Return(
			func(context.Context, int) ([]store.BoxscorePlayer, error) {
				p.mu.Lock()
				defer p.mu.Unlock()
				assert.Len(t, p.staleKeys, 1, "season %d counted before the stale report cleanup", year)
				return make([]store.BoxscorePlayer, playerLogsTestPlayers(year)), nil
			}).Once()
	}
	p.env.RegisterWorkflow(FetchSeasonPlayerLogsWorkflow)
	p.env.OnWorkflow(FetchSeasonPlayerLogsWorkflow, mock.Anything, mock.Anything).Return(
		func(ctx workflow.Context, in FetchSeasonPlayerLogsInput) error {
			p.recordChild(ctx, in)
			return nil
		}).Maybe()
	return p
}

// FetchPlayerLogsWorkflow sizes each season's bar by its boxscore players,
// scopes the refresh flag to the last season without a range and to every
// season with one, and keys each bar and stale report to the child's
// workflow ID.
func TestFetchPlayerLogsWorkflowFetchesEverySeason(t *testing.T) {
	seasons := playerLogsTestSeasons()
	for _, tc := range fanOutRefreshCases(2022) {
		t.Run(tc.name, func(t *testing.T) {
			p := newFetchPlayerLogsParent(t, seasons)
			group := p.run(t, FetchPlayerLogsWorkflow, &model.SeasonsInput{
				StartSeason: tc.start, EndSeason: tc.end, RefreshCurrentPlayerLogs: tc.flag,
			})

			var want []fanOutChild
			for i, s := range seasons {
				want = append(want, fanOutChild{WorkflowIDFetchSeasonPlayerLogs(s.ID.StartYear()),
					FetchSeasonPlayerLogsInput{Season: s, RefreshCurrent: tc.want[i]}})
			}
			assert.ElementsMatch(t, want, p.children)
			assert.Equal(t, fanOutSourceKeys(seasons, WorkflowIDFetchSeasonPlayerLogs), p.staleKeys)
			assert.Equal(t, fanOutBars(seasons, playerLogsTestPlayers, WorkflowIDFetchSeasonPlayerLogs), group.Bars)
			elapsed := shared.FormatElapsedMilli(group.CompletedAt - group.StartedAt)
			assert.Equal(t, "Fetched player logs for 2 seasons in "+elapsed+".", group.CompletedMsg)
		})
	}
}

// An empty manifest counts no boxscore players and starts nothing.
func TestFetchPlayerLogsWorkflowWithoutSeasons(t *testing.T) {
	p := newFetchPlayerLogsParent(t, nil)
	group := p.run(t, FetchPlayerLogsWorkflow, nil)

	assert.Empty(t, p.children)
	assert.Empty(t, p.staleKeys)
	assert.Empty(t, group.Bars)
	assert.Empty(t, group.CompletedMsg)
}
