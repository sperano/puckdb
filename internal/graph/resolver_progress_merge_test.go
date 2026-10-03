package graph

import (
	"bytes"
	"context"
	"encoding/gob"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	mergeParentID     = "season-sync"
	mergeNHLChildID   = "fetch-nhl-season-2025"
	mergeYahooChildID = "fetch-yahoo-season-2026"
	mergeRunID        = "run-1"
	// mergeStartedAt marks the parent group as started so the resolver shows it.
	mergeStartedAt = 1
)

func saveGobReport(t *testing.T, rc *redis.Client, workflowID string, report *shared.ProgressReport) {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(report))
	require.NoError(t, cache.SaveProgressReport(context.Background(), rc, workflowID, mergeRunID, buf.Bytes()))
}

// mergeParentReport has an NHL season bar (summing merge) and two Yahoo league
// bars mirroring child bars 0 and 1, all at their initial estimates.
func mergeParentReport() *shared.ProgressReport {
	return &shared.ProgressReport{
		Total: 100 + 80 + 50,
		Groups: []shared.ProgressGroup{{
			StartedAt: mergeStartedAt,
			Bars: []shared.ProgressBar{
				{Label: "2025-26", Total: 100, ProgressSourceKey: mergeNHLChildID},
				{Label: "2026-27 · 1001", Total: 80, ProgressSourceKey: mergeYahooChildID, MirrorSourceBar: true, ProgressSourceBar: 0},
				{Label: "2026-27 · 1002", Total: 50, ProgressSourceKey: mergeYahooChildID, MirrorSourceBar: true, ProgressSourceBar: 1},
			},
		}},
	}
}

func TestQueryProgressReport_MirrorsSourceBarAndSumsOthers(t *testing.T) {
	t.Parallel()
	r, _, rc := newProgressResolver(t)
	saveGobReport(t, rc, mergeParentID, mergeParentReport())
	// NHL child: two groups, summed into the season bar.
	saveGobReport(t, rc, mergeNHLChildID, &shared.ProgressReport{Groups: []shared.ProgressGroup{
		{Bars: []shared.ProgressBar{{Current: 30, Total: 90}}},
		{Bars: []shared.ProgressBar{{Current: 5, Total: 10}}},
	}})
	// Yahoo child: per-league bars whose totals were corrected at runtime.
	saveGobReport(t, rc, mergeYahooChildID, &shared.ProgressReport{Groups: []shared.ProgressGroup{
		{Bars: []shared.ProgressBar{{Current: 12, Total: 61}, {Current: 6, Total: 6}}},
	}})

	report, err := r.queryProgressReport(context.Background(), mergeParentID)

	require.NoError(t, err)
	bars := report.Groups[0].Bars
	assert.Equal(t, [2]int{35, 100}, [2]int{bars[0].Current, bars[0].Total}, "NHL bar sums every child bar")
	assert.Equal(t, [2]int{12, 61}, [2]int{bars[1].Current, bars[1].Total}, "mirrors child bar 0")
	assert.Equal(t, [2]int{6, 6}, [2]int{bars[2].Current, bars[2].Total}, "mirrors child bar 1")
	for _, bar := range bars {
		assert.True(t, bar.Started)
	}
	assert.Equal(t, 100+61+6, report.Total, "report Total follows the mirrored totals")
	assert.Equal(t, 35+12+6, report.Completed)
}

// A bar the parent completed with the child's final totals is final: a
// stale or diverging child report must not move it.
func TestQueryProgressReport_CompletedMirrorBarIsFinal(t *testing.T) {
	t.Parallel()
	r, _, rc := newProgressResolver(t)
	parent := mergeParentReport()
	parent.Groups[0].Bars[1].Current, parent.Groups[0].Bars[1].Total = 14, 14
	parent.Total = 100 + 14 + 50
	saveGobReport(t, rc, mergeParentID, parent)
	saveGobReport(t, rc, mergeYahooChildID, &shared.ProgressReport{Groups: []shared.ProgressGroup{
		{Bars: []shared.ProgressBar{{Current: 3, Total: 20}}},
	}})

	report, err := r.queryProgressReport(context.Background(), mergeParentID)

	require.NoError(t, err)
	bars := report.Groups[0].Bars
	assert.Equal(t, [2]int{14, 14}, [2]int{bars[1].Current, bars[1].Total})
	assert.Equal(t, [2]int{0, 50}, [2]int{bars[2].Current, bars[2].Total},
		"a source bar the child report lacks leaves the estimate")
	assert.Equal(t, 100+14+50, report.Total)
}
