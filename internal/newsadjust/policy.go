package newsadjust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
)

// MethodVersion identifies the adjustment method independently of policy.
const MethodVersion = "news-adjust-v1"

// Scenario is one set of assumptions about uncertain news effects.
type Scenario string

const (
	ScenarioConservative Scenario = "conservative"
	ScenarioBase         Scenario = "base"
	ScenarioOptimistic   Scenario = "optimistic"
)

// Scenarios lists every scenario in display order.
var Scenarios = []Scenario{ScenarioConservative, ScenarioBase, ScenarioOptimistic}

// RumorPolicy says whether an unconfirmed report may change a projection.
type RumorPolicy string

const (
	// RumorAlert keeps rumors as alerts; no scenario changes.
	RumorAlert RumorPolicy = "alert"
	// RumorConservative applies a rumor's effect in the conservative
	// scenario only, and says so in the player's assumptions.
	RumorConservative RumorPolicy = "conservative"
)

// Range is one assumption's value under each scenario.
type Range struct {
	Conservative float64 `json:"conservative"`
	Base         float64 `json:"base"`
	Optimistic   float64 `json:"optimistic"`
}

func (r Range) at(s Scenario) float64 {
	switch s {
	case ScenarioConservative:
		return r.Conservative
	case ScenarioOptimistic:
		return r.Optimistic
	default:
		return r.Base
	}
}

// Default assumptions. None is calibrated: PuckDB has no labeled outcome
// data linking news to later games, ice time or starts. They are labeled as
// such in every adjustment that uses them and can be replaced per policy.
const (
	DefaultCalibration = "uncalibrated defaults: no labeled news-outcome data is available"

	defaultDayToDayConservative     = 3
	defaultDayToDayBase             = 1
	defaultWeekToWeekConservative   = 12
	defaultWeekToWeekBase           = 6
	defaultWeekToWeekOptimistic     = 2
	defaultMonthToMonthConservative = 30
	defaultMonthToMonthBase         = 15
	defaultMonthToMonthOptimistic   = 8
	defaultIndefiniteConservative   = 41
	defaultIndefiniteBase           = 15
	defaultUnknownConservative      = 20
	defaultUnknownBase              = 5

	defaultIceTimeUpBase           = 1.08
	defaultIceTimeUpOptimistic     = 1.15
	defaultIceTimeDownConservative = 0.85
	defaultIceTimeDownBase         = 0.92
	defaultPowerPlayUpBase         = 1.40
	defaultPowerPlayUpOptimistic   = 1.80
	defaultPowerPlayDownConserv    = 0.40
	defaultPowerPlayDownBase       = 0.65

	defaultStarterShareConservative = 0.55
	defaultStarterShareBase         = 0.63
	defaultStarterShareOptimistic   = 0.72
	defaultTandemShareConservative  = 0.38
	defaultTandemShareBase          = 0.46
	defaultTandemShareOptimistic    = 0.54
	defaultBackupShareConservative  = 0.18
	defaultBackupShareBase          = 0.25
	defaultBackupShareOptimistic    = 0.32

	// defaultOffseasonDays: news from more than this long before the opener
	// (about the start of July for an October opener) is last season's.
	defaultOffseasonDays = 90
	maximumOffseasonDays = 365

	noChange = 1.0
)

// Policy holds every assumption that turns an event into numbers.
// OffseasonDays separates the previous season's news from offseason news
// about the target season.
// MissedGames counts regular-season team games missed from the later of the
// season start and the event's effective start; DurationGames, DurationUntil
// and DurationSeason events never use it. Multipliers apply to per-game ice
// time and to power-play production; goalie shares are the fraction of the
// season's games a goalie in that role starts.
type Policy struct {
	Version          string                 `json:"version"`
	Calibration      string                 `json:"calibration"`
	Rumors           RumorPolicy            `json:"rumors"`
	OffseasonDays    float64                `json:"offseason_days"`
	MissedGames      map[DurationKind]Range `json:"missed_games"`
	IceTimeUp        Range                  `json:"ice_time_up"`
	IceTimeDown      Range                  `json:"ice_time_down"`
	PowerPlayUp      Range                  `json:"power_play_up"`
	PowerPlayDown    Range                  `json:"power_play_down"`
	GoalieStartShare map[GoalieRole]Range   `json:"goalie_start_share"`
}

// DefaultPolicy returns the labeled, uncalibrated default assumptions.
func DefaultPolicy() Policy {
	return Policy{
		Version: MethodVersion, Calibration: DefaultCalibration, Rumors: RumorAlert,
		OffseasonDays: defaultOffseasonDays,
		MissedGames: map[DurationKind]Range{
			DurationDayToDay:     {defaultDayToDayConservative, defaultDayToDayBase, 0},
			DurationWeekToWeek:   {defaultWeekToWeekConservative, defaultWeekToWeekBase, defaultWeekToWeekOptimistic},
			DurationMonthToMonth: {defaultMonthToMonthConservative, defaultMonthToMonthBase, defaultMonthToMonthOptimistic},
			DurationIndefinite:   {defaultIndefiniteConservative, defaultIndefiniteBase, 0},
			DurationUnknown:      {defaultUnknownConservative, defaultUnknownBase, 0},
		},
		IceTimeUp:     Range{noChange, defaultIceTimeUpBase, defaultIceTimeUpOptimistic},
		IceTimeDown:   Range{defaultIceTimeDownConservative, defaultIceTimeDownBase, noChange},
		PowerPlayUp:   Range{noChange, defaultPowerPlayUpBase, defaultPowerPlayUpOptimistic},
		PowerPlayDown: Range{defaultPowerPlayDownConserv, defaultPowerPlayDownBase, noChange},
		GoalieStartShare: map[GoalieRole]Range{
			GoalieRoleStarter: {defaultStarterShareConservative, defaultStarterShareBase, defaultStarterShareOptimistic},
			GoalieRoleTandem:  {defaultTandemShareConservative, defaultTandemShareBase, defaultTandemShareOptimistic},
			GoalieRoleBackup:  {defaultBackupShareConservative, defaultBackupShareBase, defaultBackupShareOptimistic},
		},
	}
}

// unknownDurations are the kinds whose length comes from MissedGames.
var unknownDurations = []DurationKind{
	DurationDayToDay, DurationWeekToWeek, DurationMonthToMonth, DurationIndefinite, DurationUnknown,
}

// Validate checks that every assumption is present, finite and ordered so
// the conservative scenario is never better for the player than the base,
// and the base never better than the optimistic one.
func (p Policy) Validate() error {
	switch {
	case p.Version == "":
		return fmt.Errorf("policy version is required")
	case p.Calibration == "":
		return fmt.Errorf("policy must say how its assumptions were calibrated")
	case p.Rumors != RumorAlert && p.Rumors != RumorConservative:
		return fmt.Errorf("unknown rumor policy %q", p.Rumors)
	case math.IsNaN(p.OffseasonDays) || p.OffseasonDays <= 0 || p.OffseasonDays > maximumOffseasonDays:
		return fmt.Errorf("offseason days must be in (0, %d]", maximumOffseasonDays)
	}
	for _, kind := range unknownDurations {
		r, ok := p.MissedGames[kind]
		if !ok || !orderedRange(r, false) || r.Optimistic < 0 {
			return fmt.Errorf("missed games for %s must be finite, nonnegative and conservative >= base >= optimistic", kind)
		}
	}
	multipliers := []struct {
		name  string
		value Range
	}{
		{"ice_time_up", p.IceTimeUp}, {"ice_time_down", p.IceTimeDown},
		{"power_play_up", p.PowerPlayUp}, {"power_play_down", p.PowerPlayDown},
	}
	for _, m := range multipliers {
		if !orderedRange(m.value, true) || m.value.Conservative < 0 {
			return fmt.Errorf("%s multipliers must be finite, nonnegative and conservative <= base <= optimistic", m.name)
		}
	}
	for _, role := range goalieRoles[1:] {
		r, ok := p.GoalieStartShare[role]
		if !ok || !orderedRange(r, true) || r.Conservative < 0 || r.Optimistic > 1 {
			return fmt.Errorf("%s start share must be in [0, 1] and conservative <= base <= optimistic", role)
		}
	}
	return nil
}

// orderedRange checks the scenario order; ascending means the optimistic
// value is the largest.
func orderedRange(r Range, ascending bool) bool {
	values := []float64{r.Conservative, r.Base, r.Optimistic}
	if slices.ContainsFunc(values, func(v float64) bool { return math.IsNaN(v) || math.IsInf(v, 0) }) {
		return false
	}
	if ascending {
		return r.Conservative <= r.Base && r.Base <= r.Optimistic
	}
	return r.Conservative >= r.Base && r.Base >= r.Optimistic
}

// Hash identifies the policy's content.
func (p Policy) Hash() string {
	return hashJSON(p)
}

func hashJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal adjustment hash input: %v", err))
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
