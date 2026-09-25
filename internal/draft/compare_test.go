package draft

import (
	"strings"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	compareTestSeason       = 2026
	compareTestLeagueID     = 1001
	compareTestSourceSeason = 2024
	compareTestSourceKey    = "453.l.11111"
)

var compareTestGeneratedAt = time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)

// buildCompareReports returns a stand-in roto report and a Yahoo-API points
// report (missing the BLK weight) sharing the same Season/LeagueID, fetched
// well within the comparison's MaxAge.
func buildCompareReports(t *testing.T) (roto, points LeagueReport) {
	t.Helper()
	fetchedAt := compareTestGeneratedAt.Add(-time.Hour)

	rotoRules := FromLeague(parseRotoLeague(t))
	rotoRules.Name = "Roto | League"
	roto = LeagueReport{
		Season: compareTestSeason, LeagueID: compareTestLeagueID,
		Snapshot: Snapshot{
			Rules: rotoRules, Source: SourceTemporaryStandIn,
			SourceSeason: compareTestSourceSeason, SourceLeagueKey: compareTestSourceKey,
			FetchedAt: fetchedAt, FirstSeenAt: fetchedAt, LastSeenAt: fetchedAt,
		},
	}

	pointsRules := FromLeague(parsePointsLeague(t, yahoofixtures.PointsLeagueMissingWeight))
	points = LeagueReport{
		Season: compareTestSeason, LeagueID: compareTestLeagueID,
		Snapshot: Snapshot{
			Rules: pointsRules, Source: SourceYahooAPI,
			FetchedAt: fetchedAt, FirstSeenAt: fetchedAt, LastSeenAt: fetchedAt,
		},
	}
	return roto, points
}

func TestWriteComparison(t *testing.T) {
	t.Parallel()
	roto, points := buildCompareReports(t)
	opts := ComparisonOptions{GeneratedAt: compareTestGeneratedAt, MaxAge: 24 * time.Hour}

	var b strings.Builder
	require.NoError(t, WriteComparison(&b, []LeagueReport{roto, points}, opts))
	out := b.String()

	header := "2026 league 1001"
	assert.GreaterOrEqual(t, strings.Count(out, header), 2, "both league columns are headed %q", header)

	for _, want := range []string{
		comparisonTitle,
		"UNVERIFIED temporary stand-in",
		"lower is better",
		"-2 pts",
		"display only",
		"NO WEIGHT",
		"Util (flex: C/LW/RW/D)",
		"IR+ (reserve: Yahoo-eligible players only)",
		missingCell,
		"Total slots / starting slots",
		"can_trade_draft_picks",
		"rankings will refuse this league",
		"Roto \\| League",
	} {
		assert.Contains(t, out, want)
	}
}

func TestReportWarnings_UnsupportedSlot(t *testing.T) {
	t.Parallel()
	report := validLeagueReport()
	report.Snapshot.Rules.RosterSlots = append(report.Snapshot.Rules.RosterSlots, RosterSlot{Position: unknownSlot, Count: 1})
	warnings := ReportWarnings(report, validComparisonOptions())
	assert.Contains(t, strings.Join(warnings, "\n"), `roster slot "XYZ" is not modeled`)
}

func TestReportWarnings_NoDraftTime(t *testing.T) {
	t.Parallel()
	report := validLeagueReport()
	report.Snapshot.Rules.Draft.Time = nil
	warnings := ReportWarnings(report, validComparisonOptions())
	assert.Contains(t, warnings, "no scheduled draft time")
}

func TestReportWarnings_StaleSettings(t *testing.T) {
	t.Parallel()
	report := validLeagueReport()
	opts := validComparisonOptions()
	report.Snapshot.FetchedAt = opts.GeneratedAt.Add(-opts.MaxAge - time.Hour)
	warnings := ReportWarnings(report, opts)
	found := false
	for _, w := range warnings {
		if strings.HasPrefix(w, "settings are stale: fetched") {
			found = true
		}
	}
	assert.True(t, found, "warnings %v must include a stale-settings entry", warnings)
}

// validLeagueReport returns a report whose rules pass ScoringFor and whose
// draft time and fetch time are fresh, so a test can flip exactly one thing
// and see exactly one new warning appear.
func validLeagueReport() LeagueReport {
	draftTime := compareTestGeneratedAt.Add(-24 * time.Hour)
	return LeagueReport{
		Season: compareTestSeason, LeagueID: compareTestLeagueID,
		Snapshot: Snapshot{
			Source:    SourceYahooAPI,
			FetchedAt: compareTestGeneratedAt.Add(-time.Hour),
			Rules: Rules{
				LeagueKey: "465.l.1", ScoringType: scoringTypeRoto,
				Categories: []StatCategory{{StatID: statGoals, Abbr: "G", Enabled: true, Direction: HigherIsBetter}},
				Draft:      DraftRules{Time: &draftTime},
			},
		},
	}
}

func validComparisonOptions() ComparisonOptions {
	return ComparisonOptions{GeneratedAt: compareTestGeneratedAt, MaxAge: 24 * time.Hour}
}
