package mauricereport

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTokenCountTotalKeepsUnknownApartFromZero(t *testing.T) {
	tests := []struct {
		name     string
		count    TokenCount
		want     *int64
		complete bool
	}{
		{"no call is a measured zero", TokenCount{}, tokens(0), true},
		{"every call reported", reported(30, 2, 0), tokens(30), true},
		{"some calls reported: a lower bound", reported(30, 2, 1), tokens(30), false},
		{"no call reported: unknown", reported(0, 0, 2), nil, false},
		{"reported zeros are zero", reported(0, 2, 0), tokens(0), true},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.count.Total(), tt.name)
		assert.Equal(t, tt.complete, tt.count.Complete(), tt.name)
	}
}

func TestTurnRatesAreOverTerminalTurns(t *testing.T) {
	counts := TurnCounts{Running: 5, Succeeded: 3, Failed: 1}
	assert.Equal(t, int64(4), counts.Terminal())
	assert.Equal(t, f64(0.75), counts.Rate(counts.Succeeded), "running turns are not in the denominator")
	assert.Equal(t, f64(0), counts.Rate(counts.Cancelled))
	assert.Nil(t, TurnCounts{Running: 2}.Rate(0), "no finished turn: no rate")
}
