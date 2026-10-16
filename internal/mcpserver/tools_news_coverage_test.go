package mcpserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCoverageQueries serves a fixed set of news fetch states; the embedded
// ReportQueries interface is nil, so any other query panics instead of
// silently passing.
type fakeCoverageQueries struct {
	news.ReportQueries
	states []sqlcdb.NewsFetchState
	err    error
	calls  int
}

func (f *fakeCoverageQueries) ListNewsFetchStates(context.Context) ([]sqlcdb.NewsFetchState, error) {
	f.calls++
	return f.states, f.err
}

func TestNewsCoverageTextRendersHeaderAndRows(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	coverage := []news.SourceCoverage{
		{
			Source: news.Source{ID: "nhl-injury", Publisher: "NHL.com", Kind: news.KindOfficial, Priority: 20},
			Scope:  news.ScopeFeed, Status: news.CoverageFresh, AsOf: now.Add(-time.Hour),
			State: news.FetchState{LastAttemptAt: now.Add(-time.Hour), LastSuccessAt: now.Add(-time.Hour)},
		},
		{
			Source: news.Source{ID: "rotowire-nhl", Publisher: "RotoWire", Kind: news.KindReporting, Priority: 60},
			Scope:  news.ScopeFeed, Status: news.CoverageStale, AsOf: now.Add(-48 * time.Hour),
			State: news.FetchState{
				LastAttemptAt: now.Add(-time.Hour), LastSuccessAt: now.Add(-48 * time.Hour),
				ConsecutiveFailures: 3, LastError: "boom",
			},
		},
	}

	text, err := newsCoverageText(2026, now, coverage)
	require.NoError(t, err)

	header, rest, ok := strings.Cut(text, "\n")
	require.True(t, ok, text)
	section, csvText, ok := strings.Cut(rest, "\n")
	require.True(t, ok, text)
	assert.Equal(t, "# season=2026 sources=2 generated_at=2026-10-16T12:00:00Z", header)
	assert.Equal(t, commentPrefix+newsCoverageSection, section)
	rows := parseCSV(t, csvText)
	require.Len(t, rows, 3)
	assert.Equal(t, []string{
		"source_id", "publisher", "kind", "priority", "scope", "status",
		"as_of", "last_attempt_at", "last_success_at", "consecutive_failures", "last_error",
	}, rows[0])
	assert.Equal(t, []string{
		"nhl-injury", "NHL.com", "official", "20", "feed", "fresh",
		"2026-10-16T11:00:00Z", "2026-10-16T11:00:00Z", "2026-10-16T11:00:00Z", "0", "",
	}, rows[1])
	assert.Equal(t, []string{
		"rotowire-nhl", "RotoWire", "reporting", "60", "feed", "stale",
		"2026-10-14T12:00:00Z", "2026-10-16T11:00:00Z", "2026-10-14T12:00:00Z", "3", "boom",
	}, rows[2])
}

func TestNewsCoverageToolReportsMissingSource(t *testing.T) {
	q := &fakeCoverageQueries{}
	sources := []news.Source{
		{ID: "rotowire-nhl", Publisher: "RotoWire", Kind: news.KindReporting, Adapter: news.AdapterRSS, Priority: 60, RefreshMinutes: 15},
	}
	tool := newsCoverageTool(q, sources)
	result := callTool(t, tool.Handler, map[string]any{"season": float64(2026)})
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, 1, q.calls)
	assert.Contains(t, resultText(t, result), "rotowire-nhl")
	assert.Contains(t, resultText(t, result), "missing")
}

func TestNewsCoverageToolRejectsInvalidSeasonBeforeLoading(t *testing.T) {
	q := &fakeCoverageQueries{}
	tool := newsCoverageTool(q, nil)
	result := callTool(t, tool.Handler, map[string]any{"season": float64(20252026.5)})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "invalid season")
	assert.Zero(t, q.calls)
}

func TestNewsCoverageToolReportsLoadError(t *testing.T) {
	q := &fakeCoverageQueries{err: errors.New("db down")}
	tool := newsCoverageTool(q, nil)
	result := callTool(t, tool.Handler, map[string]any{"season": float64(2026)})
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "db down")
}
