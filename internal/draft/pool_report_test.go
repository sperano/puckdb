package draft

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func poolReportTestReport(pool PoolCoverage) LeagueReport {
	return LeagueReport{
		Season: compareTestSeason, LeagueID: compareTestLeagueID,
		Snapshot: Snapshot{Rules: Rules{LeagueKey: "465.l.1"}},
		Pool:     pool,
	}
}

func TestWritePoolReport_Counts(t *testing.T) {
	t.Parallel()
	fetchedAt := time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC)
	pool := PoolCoverage{
		Players: 5, MultiPosition: 2, FetchedAt: fetchedAt,
		Unresolved: []PoolPlayer{{Name: "Ghost", YahooPlayerID: 1}},
		Unmatched:  []PoolPlayer{{Name: "Rookie", YahooPlayerID: 2, Team: "TOR"}},
	}
	var b strings.Builder
	require.NoError(t, WritePoolReport(&b, poolReportTestReport(pool)))
	out := b.String()

	assert.Contains(t, out, "Players: 5")
	assert.Contains(t, out, "Eligible at more than one position: 2")
	assert.Contains(t, out, "Without Yahoo eligibility: 1")
	assert.Contains(t, out, "Without NHL player mapping: 1")
	assert.Contains(t, out, "Ghost")
	assert.Contains(t, out, "Rookie")
}

func TestWritePoolReport_ListCap(t *testing.T) {
	t.Parallel()
	const total = poolReportListLimit + 7
	players := make([]PoolPlayer, total)
	for i := range players {
		players[i] = PoolPlayer{Name: fmt.Sprintf("Player %d", i), YahooPlayerID: i}
	}
	pool := PoolCoverage{Players: total, Unresolved: players}

	var b strings.Builder
	require.NoError(t, WritePoolReport(&b, poolReportTestReport(pool)))
	out := b.String()

	assert.Contains(t, out, fmt.Sprintf("... and %d more", total-poolReportListLimit))
	assert.Equal(t, poolReportListLimit, strings.Count(out, "no positions)"), "the list stops at the cap")
}

func TestWritePoolReport_EmptyPool(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	require.NoError(t, WritePoolReport(&b, poolReportTestReport(PoolCoverage{})))
	out := b.String()
	assert.Contains(t, out, "No player pool imported.")
	assert.Contains(t, out, "player pool is empty")
}
