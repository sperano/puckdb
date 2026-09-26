package newsadjust

import (
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/news"
	"github.com/stretchr/testify/assert"
)

func TestCoverageWarnings_NameEverySourceThatIsNotFresh(t *testing.T) {
	asOf := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	source := func(id string) news.Source { return news.Source{ID: id, Publisher: id + " publisher"} }
	coverage := []news.SourceCoverage{
		{Source: source("rotowire"), Scope: "all", Status: news.CoverageStale, AsOf: asOf.Add(-48 * time.Hour)},
		{Source: source("nhl"), Scope: "all", Status: news.CoverageFresh, AsOf: asOf},
		{Source: source("yahoo"), Scope: "2026", Status: news.CoverageMissing},
		{Source: source("dailyfaceoff"), Scope: "all", Status: news.CoverageFailing, AsOf: asOf.Add(-time.Hour),
			State: news.FetchState{ConsecutiveFailures: 3}},
	}
	assert.Equal(t, []string{
		"news source dailyfaceoff (dailyfaceoff publisher, all) is failing (3 consecutive failures); its data is from 2026-09-20T11:00:00Z",
		"news source rotowire (rotowire publisher, all) is stale: its newest data is from 2026-09-18T12:00:00Z",
		"news source yahoo (yahoo publisher, 2026) has never been fetched; its news cannot adjust anything",
	}, CoverageWarnings(coverage))
	assert.Empty(t, CoverageWarnings(coverage[1:2]))
}
