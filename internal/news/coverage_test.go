package news

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var coverageNow = time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)

func rssSource(id string) Source {
	return Source{ID: id, Publisher: "P", Kind: KindReporting, Adapter: AdapterRSS, URL: "https://x.example.com",
		RefreshMinutes: 60, Priority: 1, Enabled: true}
}

func TestEvaluateCoverageNeverFetchedIsMissing(t *testing.T) {
	src := rssSource("a")
	out := EvaluateCoverage([]Source{src}, nil, testSeason, coverageNow)
	require.Len(t, out, 1)
	assert.Equal(t, CoverageMissing, out[0].Status)
	assert.True(t, out[0].AsOf.IsZero())
}

func TestEvaluateCoverageFresh(t *testing.T) {
	src := rssSource("a")
	state := FetchState{SourceID: "a", Scope: ScopeFeed, LastSuccessAt: coverageNow.Add(-10 * time.Minute)}
	out := EvaluateCoverage([]Source{src}, []FetchState{state}, testSeason, coverageNow)
	require.Len(t, out, 1)
	assert.Equal(t, CoverageFresh, out[0].Status)
	assert.True(t, out[0].Current())
}

func TestEvaluateCoverageFailingWithinStaleThreshold(t *testing.T) {
	src := rssSource("a")
	state := FetchState{SourceID: "a", Scope: ScopeFeed, LastSuccessAt: coverageNow.Add(-10 * time.Minute), ConsecutiveFailures: 2}
	out := EvaluateCoverage([]Source{src}, []FetchState{state}, testSeason, coverageNow)
	require.Len(t, out, 1)
	assert.Equal(t, CoverageFailing, out[0].Status)
	assert.True(t, out[0].Current())
}

func TestEvaluateCoverageStaleEvenWhileFailing(t *testing.T) {
	src := rssSource("a") // StaleAfter defaults to 3x60min = 180min
	state := FetchState{SourceID: "a", Scope: ScopeFeed, LastSuccessAt: coverageNow.Add(-4 * time.Hour), ConsecutiveFailures: 3}
	out := EvaluateCoverage([]Source{src}, []FetchState{state}, testSeason, coverageNow)
	require.Len(t, out, 1)
	assert.Equal(t, CoverageStale, out[0].Status, "old data is stale regardless of the failure count")
	assert.False(t, out[0].Current())
}

func TestEvaluateCoverageDataAsOfTakesPrecedenceOverLastSuccessAt(t *testing.T) {
	src := Source{ID: "yahoo-status", Publisher: "Yahoo Fantasy", Kind: KindStructured, Adapter: AdapterYahooStatus,
		RefreshMinutes: 60, StaleAfterMinutes: 1440, Enabled: true}
	state := FetchState{
		SourceID: "yahoo-status", Scope: src.Scope(testSeason),
		LastSuccessAt: coverageNow.Add(-1 * time.Hour), DataAsOf: coverageNow.Add(-48 * time.Hour),
	}
	out := EvaluateCoverage([]Source{src}, []FetchState{state}, testSeason, coverageNow)
	require.Len(t, out, 1)
	assert.Equal(t, CoverageStale, out[0].Status, "the underlying Yahoo data is 48h old even though the fetch itself just succeeded")
	assert.Equal(t, state.DataAsOf, out[0].AsOf)
}

func TestEvaluateCoverageScopeMatchesPerSeason(t *testing.T) {
	src := Source{ID: "yahoo-status", Publisher: "Yahoo Fantasy", Kind: KindStructured, Adapter: AdapterYahooStatus,
		RefreshMinutes: 60, StaleAfterMinutes: 1440, Enabled: true}
	otherSeasonState := FetchState{SourceID: "yahoo-status", Scope: "season:2025", LastSuccessAt: coverageNow}
	out := EvaluateCoverage([]Source{src}, []FetchState{otherSeasonState}, testSeason, coverageNow)
	require.Len(t, out, 1)
	assert.Equal(t, CoverageMissing, out[0].Status, "a previous season's fetch state must not count as coverage")
}

func TestIndependentPublishersDedupesAndIgnoresRepeats(t *testing.T) {
	inc := Incident{Evidence: []Evidence{
		{Relation: RelationIndependent, Publisher: "NHL.com"},
		{Relation: RelationIndependent, Publisher: "NHL.com"},
		{Relation: RelationSyndicated, Publisher: "Team Site"},
		{Relation: RelationIndependent, Publisher: "RotoWire"},
	}}
	assert.Equal(t, []string{"NHL.com", "RotoWire"}, inc.IndependentPublishers())
}

func TestSupersededOnlyWhenEveryEvidenceIsOutdated(t *testing.T) {
	assert.False(t, Incident{}.Superseded(), "no evidence at all is not a superseded incident")

	mixed := Incident{Evidence: []Evidence{{Version: 1, LatestVersion: 2}, {Version: 1, LatestVersion: 1}}}
	assert.False(t, mixed.Superseded(), "one current report still supports the incident")

	allOutdated := Incident{Evidence: []Evidence{{Version: 1, LatestVersion: 2}, {Version: 1, LatestVersion: 3}}}
	assert.True(t, allOutdated.Superseded())
}

func TestEvidenceOutdated(t *testing.T) {
	assert.True(t, Evidence{Version: 1, LatestVersion: 2}.Outdated())
	assert.False(t, Evidence{Version: 2, LatestVersion: 2}.Outdated())
}

func TestPlayerNewsAssessmentWithIncidents(t *testing.T) {
	p := PlayerNews{Incidents: []Incident{{}, {}}}
	assert.Contains(t, p.Assessment(), "2 incident candidate(s) on record")
}

func TestPlayerNewsAssessmentNoIncidentsAllFresh(t *testing.T) {
	p := PlayerNews{Coverage: []SourceCoverage{{Status: CoverageFresh}}}
	msg := p.Assessment()
	assert.Contains(t, msg, "not evidence that the player is healthy")
}

func TestPlayerNewsAssessmentNoIncidentsWithGap(t *testing.T) {
	p := PlayerNews{Coverage: []SourceCoverage{
		{Source: Source{ID: "rotowire-nhl"}, Status: CoverageStale},
		{Source: Source{ID: "nhl-injury"}, Status: CoverageFresh},
	}}
	msg := p.Assessment()
	assert.Contains(t, msg, "rotowire-nhl")
	assert.Contains(t, msg, string(CoverageStale))
	assert.Contains(t, msg, "not evidence that the player is healthy")
	assert.NotContains(t, msg, "nhl-injury", "only the source with a coverage gap is listed")
}

func TestPlayerNewsAssessmentNeverImpliesHealthWithoutTheCaveat(t *testing.T) {
	scenarios := []PlayerNews{
		{Incidents: []Incident{{}}},
		{Coverage: []SourceCoverage{{Status: CoverageFresh}}},
		{Coverage: []SourceCoverage{{Source: Source{ID: "a"}, Status: CoverageMissing}}},
	}
	for _, p := range scenarios {
		msg := p.Assessment()
		if strings.Contains(msg, "healthy") {
			assert.Contains(t, msg, "not evidence", "any mention of health must carry the caveat: %q", msg)
		}
	}
}
