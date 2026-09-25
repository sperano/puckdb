package draft

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const poolTestMaxAge = 24 * time.Hour

var poolTestNow = time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)

func TestCoverage(t *testing.T) {
	t.Parallel()
	oldest := poolTestNow.Add(-2 * time.Hour)
	players := []PoolPlayer{
		{YahooPlayerID: 1, Name: "Multi", EligiblePositions: []string{PositionCenter, PositionLeftWing, SlotUtility}, NHLPlayerID: 100, FetchedAt: poolTestNow},
		{YahooPlayerID: 2, Name: "NoPositions", EligiblePositions: nil, NHLPlayerID: 200, FetchedAt: oldest},
		{YahooPlayerID: 3, Name: "Unmatched", EligiblePositions: []string{PositionDefense}, NHLPlayerID: 0, FetchedAt: poolTestNow},
	}

	coverage := Coverage(players, poolTestNow, poolTestMaxAge)
	assert.Equal(t, 3, coverage.Players)
	assert.Equal(t, 1, coverage.MultiPosition, "C/LW/Util counts as 2 base positions, so only one player qualifies")
	require.Len(t, coverage.Unresolved, 1)
	assert.Equal(t, "NoPositions", coverage.Unresolved[0].Name)
	require.Len(t, coverage.Unmatched, 1)
	assert.Equal(t, "Unmatched", coverage.Unmatched[0].Name)
	assert.True(t, coverage.FetchedAt.Equal(oldest), "FetchedAt is the oldest fetch time")
	assert.False(t, coverage.Stale)
}

func TestCoverage_Stale(t *testing.T) {
	t.Parallel()
	players := []PoolPlayer{
		{YahooPlayerID: 1, Name: "Old", EligiblePositions: []string{PositionCenter}, NHLPlayerID: 1, FetchedAt: poolTestNow.Add(-poolTestMaxAge - time.Hour)},
	}
	coverage := Coverage(players, poolTestNow, poolTestMaxAge)
	assert.True(t, coverage.Stale)
}

func TestPoolCoverage_Warnings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		c       PoolCoverage
		want    string
		wantLen int
	}{
		{
			name:    "empty pool",
			c:       PoolCoverage{},
			want:    "player pool is empty: run a Yahoo sync for this league",
			wantLen: 1,
		},
		{
			name:    "stale",
			c:       PoolCoverage{Players: 1, Stale: true, FetchedAt: poolTestNow},
			want:    "player pool is stale: fetched " + poolTestNow.UTC().Format(timeLayout),
			wantLen: 1,
		},
		{
			name: "unresolved",
			c: PoolCoverage{Players: 2, Unresolved: []PoolPlayer{
				{Name: "A"}, {Name: "B"},
			}},
			want:    "2 of 2 players have no Yahoo position eligibility",
			wantLen: 1,
		},
		{
			name: "unmatched",
			c: PoolCoverage{Players: 3, Unmatched: []PoolPlayer{
				{Name: "A"},
			}},
			want:    "1 of 3 players have no NHL player mapping (no NHL stats until matched)",
			wantLen: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			warnings := tt.c.Warnings()
			require.Len(t, warnings, tt.wantLen)
			assert.Contains(t, warnings, tt.want)
		})
	}
}

func TestPoolPlayer_RosterPlayer(t *testing.T) {
	t.Parallel()
	p := PoolPlayer{YahooPlayerID: 42, Name: "Test Player", EligiblePositions: []string{PositionCenter, PositionLeftWing}}
	want := RosterPlayer{YahooPlayerID: 42, Name: "Test Player", EligiblePositions: []string{PositionCenter, PositionLeftWing}}
	assert.Equal(t, want, p.RosterPlayer())
}

func TestPoolPlayer_Matched(t *testing.T) {
	t.Parallel()
	assert.True(t, PoolPlayer{NHLPlayerID: 1}.Matched())
	assert.False(t, PoolPlayer{NHLPlayerID: 0}.Matched())
}
