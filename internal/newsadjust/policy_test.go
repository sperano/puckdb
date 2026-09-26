package newsadjust

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultPolicy_IsValidAndLabeled(t *testing.T) {
	policy := DefaultPolicy()
	require.NoError(t, policy.Validate())
	assert.Equal(t, DefaultCalibration, policy.Calibration)
	assert.Equal(t, RumorAlert, policy.Rumors, "rumors never change projections by default")
}

func TestPolicy_ValidateRejectsDisorderedAssumptions(t *testing.T) {
	cases := map[string]func(*Policy){
		"uncalibrated label missing": func(p *Policy) { p.Calibration = "" },
		"unknown rumor policy":       func(p *Policy) { p.Rumors = "always" },
		"no offseason window":        func(p *Policy) { p.OffseasonDays = 0 },
		"offseason over a year":      func(p *Policy) { p.OffseasonDays = maximumOffseasonDays + 1 },
		"missing duration": func(p *Policy) {
			delete(p.MissedGames, DurationIndefinite)
		},
		"optimistic misses more": func(p *Policy) {
			p.MissedGames[DurationUnknown] = Range{Conservative: 1, Base: 2, Optimistic: 3}
		},
		"conservative promotion better": func(p *Policy) { p.PowerPlayUp = Range{Conservative: 2, Base: 1.4, Optimistic: 1.8} },
		"not finite":                    func(p *Policy) { p.IceTimeDown.Base = math.NaN() },
		"share above one":               func(p *Policy) { p.GoalieStartShare[GoalieRoleStarter] = Range{0.6, 0.8, 1.2} },
	}
	for name, mutate := range cases {
		policy := DefaultPolicy()
		mutate(&policy)
		assert.Error(t, policy.Validate(), name)
	}
}

func TestPolicy_HashChangesWithAssumptions(t *testing.T) {
	first, second := DefaultPolicy(), DefaultPolicy()
	assert.Equal(t, first.Hash(), second.Hash())
	second.MissedGames[DurationIndefinite] = Range{Conservative: 60, Base: 20, Optimistic: 0}
	assert.NotEqual(t, first.Hash(), second.Hash())
}
