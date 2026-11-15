package workflow

import (
	"context"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/graph/model"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

const (
	// extractTestTotalPlayers and extractTestUniquePlayers are the stubbed
	// consolidation result.
	extractTestTotalPlayers  = 5
	extractTestUniquePlayers = 4
	// extractTestFailingYear is the season whose extraction fails in the
	// failure test.
	extractTestFailingYear = 2023
)

// extractTestSeasons are past seasons of different lengths, so each bar's
// total is its own season's day count.
func extractTestSeasons() []nhl.SeasonInfo {
	return []nhl.SeasonInfo{
		testSeasonW(2022, "2022-10-07", "2022-10-09"),
		testSeasonW(2023, "2023-10-10", "2023-10-13"),
	}
}

// extractTestDays is the day count of the extractTestSeasons season
// starting in startYear.
func extractTestDays(startYear int) int {
	return map[int]int{2022: 3, 2023: 4}[startYear]
}

// extractParent runs ExtractBoxscorePlayersWorkflow with each season's
// extraction returning one boxscore read per day, records the extraction
// inputs and every consolidation input.
type extractParent struct {
	*fanOutParent
	extracted    []worknhl.ExtractAndSaveInput
	consolidated []workplayer.ConsolidatePlayersInput
}

func newExtractParent(t *testing.T, seasons []nhl.SeasonInfo, extractErr, consolidateErr error) *extractParent {
	p := &extractParent{fanOutParent: newFanOutParent(t, ExtractBoxscorePlayersWorkflow, seasons)}
	var ba *worknhl.BoxscoreActivities
	p.env.OnActivity(ba.ExtractAndSaveBoxscorePlayers, mock.Anything, mock.Anything).Return(
		func(_ context.Context, in worknhl.ExtractAndSaveInput) (core.OriginCounts, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.extracted = append(p.extracted, in)
			year := in.Season.ID.StartYear()
			if extractErr != nil && year == extractTestFailingYear {
				return nil, extractErr
			}
			return core.OriginCounts{core.OriginFileSystem: extractTestDays(year)}, nil
		}).Maybe()
	var pa *workplayer.Activities
	p.env.OnActivity(pa.ConsolidateBoxscorePlayersActivity, mock.Anything, mock.Anything).Return(
		func(_ context.Context, in workplayer.ConsolidatePlayersInput) (workplayer.ConsolidatePlayersResult, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.consolidated = append(p.consolidated, in)
			if consolidateErr != nil {
				return workplayer.ConsolidatePlayersResult{}, consolidateErr
			}
			return workplayer.ConsolidatePlayersResult{
				TotalPlayers: extractTestTotalPlayers, UniquePlayers: extractTestUniquePlayers,
			}, nil
		}).Maybe()
	return p
}

// ExtractBoxscorePlayersWorkflow extracts every season up to its standings
// end, sizes and keys each bar to the season, then consolidates the
// selected seasons once, in order.
func TestExtractBoxscorePlayersWorkflowConsolidatesAfterExtraction(t *testing.T) {
	seasons := extractTestSeasons()
	p := newExtractParent(t, seasons, nil, nil)
	groups := p.runGroups(t, ExtractBoxscorePlayersWorkflow, nil)
	require.Len(t, groups, 2)

	var want []worknhl.ExtractAndSaveInput
	for _, s := range seasons {
		want = append(want, worknhl.ExtractAndSaveInput{Season: s, EndDate: s.StandingsEnd.Time})
	}
	assert.ElementsMatch(t, want, p.extracted)
	assert.Equal(t, fanOutSourceKeys(seasons, WorkflowIDExtractSeason), p.staleKeys)

	extract := groups[GroupExtractBoxscorePlayers]
	assert.Equal(t, fanOutBars(seasons, extractTestDays, WorkflowIDExtractSeason), extract.Bars)
	elapsed := shared.FormatElapsedMilli(extract.CompletedAt - extract.StartedAt)
	assert.Equal(t, "Extracted players for 2 seasons in "+elapsed+". (boxscore reads: 100% filesystem)",
		extract.CompletedMsg)

	assert.Equal(t, []workplayer.ConsolidatePlayersInput{{Seasons: []nhl.Season{seasons[0].ID, seasons[1].ID}}},
		p.consolidated)
	consolidate := groups[GroupConsolidatePlayers]
	require.Len(t, consolidate.Bars, 1)
	assert.Equal(t, [2]int{1, 1}, [2]int{consolidate.Bars[0].Current, consolidate.Bars[0].Total})
	elapsed = shared.FormatElapsedMilli(consolidate.CompletedAt - consolidate.StartedAt)
	assert.Equal(t, "Consolidated 4 unique players from 5 total in "+elapsed+".", consolidate.CompletedMsg)
}

// An empty manifest, whether nothing is configured or an inverted range
// (start after end, passed to the manifest unchanged) selects nothing, is a
// successful no-op: no extraction, no consolidation, no group started.
func TestExtractBoxscorePlayersWorkflowWithoutSeasons(t *testing.T) {
	start, end := 2023, 2022
	for _, tc := range []struct {
		name                string
		input, wantManifest *model.SeasonsInput
	}{
		{"omitted input", nil, &model.SeasonsInput{}},
		{"inverted range", &model.SeasonsInput{StartSeason: &start, EndSeason: &end},
			&model.SeasonsInput{StartSeason: &start, EndSeason: &end}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newExtractParent(t, nil, nil, nil)
			groups := p.runGroups(t, ExtractBoxscorePlayersWorkflow, tc.input)

			assert.Equal(t, tc.wantManifest, p.manifestInput)
			assert.Empty(t, p.extracted)
			assert.Empty(t, p.consolidated)
			assert.Empty(t, p.staleKeys)
			assert.False(t, anyGroupStarted(groups))
		})
	}
}

// A failed season extraction (not retried) fails the workflow without
// consolidating.
func TestExtractBoxscorePlayersWorkflowExtractionFailure(t *testing.T) {
	p := newExtractParent(t, extractTestSeasons(), temporal.NewNonRetryableApplicationError("boxscores unreadable", "test", nil), nil)
	groups, err := p.runFailing(t, ExtractBoxscorePlayersWorkflow, nil)

	assert.ErrorContains(t, err, "boxscores unreadable")
	assert.Len(t, p.extracted, len(extractTestSeasons()))
	assert.Empty(t, p.consolidated)
	assert.Empty(t, groups[GroupExtractBoxscorePlayers].CompletedMsg)
	assert.Zero(t, groups[GroupConsolidatePlayers].StartedAt)
}

// A failed consolidation (not retried) fails the workflow and leaves its
// group started but not completed.
func TestExtractBoxscorePlayersWorkflowConsolidationFailure(t *testing.T) {
	p := newExtractParent(t, extractTestSeasons(), nil, temporal.NewNonRetryableApplicationError("redis down", "test", nil))
	groups, err := p.runFailing(t, ExtractBoxscorePlayersWorkflow, nil)

	assert.ErrorContains(t, err, "redis down")
	assert.Len(t, p.consolidated, 1)
	assert.NotEmpty(t, groups[GroupExtractBoxscorePlayers].CompletedMsg)
	consolidate := groups[GroupConsolidatePlayers]
	assert.NotZero(t, consolidate.StartedAt)
	assert.Empty(t, consolidate.CompletedMsg)
	assert.Zero(t, consolidate.Bars[0].Current)
}
