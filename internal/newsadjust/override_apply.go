package newsadjust

import (
	"fmt"

	"github.com/sperano/puckdb/internal/projection"
)

// AppliedOverride records the value an override replaced in one scenario,
// so the original stays visible next to the manager's choice.
type AppliedOverride struct {
	Override Override `json:"override"`
	Scenario Scenario `json:"scenario"`
	Original float64  `json:"original"`
	Value    float64  `json:"value"`
}

// inputOverride returns the most specific active override of an input.
func (in effectInputs) inputOverride(input Input, s Scenario) (Override, bool) {
	return matchOverride(in.overrides, overrideTarget(in.player.PlayerKey, OverrideInput, "", input), s)
}

// applyInputOverrides replaces computed factors with the manager's inputs.
// An input that does not fit the player is ignored with an alert.
func (in effectInputs) applyInputOverrides(s Scenario, result *effectResult) {
	for _, input := range inputs {
		o, exists := in.inputOverride(input, s)
		if !exists {
			continue
		}
		original, value, err := in.applyInput(o, &result.effect)
		if err != nil {
			result.addAlert(fmt.Sprintf("override %s ignored: %v", o.ID, err))
			continue
		}
		result.applied = append(result.applied, AppliedOverride{Override: o, Scenario: s, Original: original, Value: value})
	}
}

func (in effectInputs) applyInput(o Override, effect *Effect) (original, value float64, err error) {
	kind := in.player.Kind
	switch o.Input {
	case InputGamesPlayed:
		if kind != projection.PlayerKindSkater {
			return 0, 0, fmt.Errorf("a goalie's workload is set with games_started")
		}
		return in.replaceWorkload(o.Value, projection.StatGamesPlayed, effect)
	case InputGamesStarted:
		if kind != projection.PlayerKindGoalie {
			return 0, 0, fmt.Errorf("games_started applies to goalies")
		}
		return in.replaceWorkload(o.Value, projection.StatGamesStarted, effect)
	case InputTOIPerGame:
		games := in.player.Values[projection.StatGamesPlayed].Mean
		toi := in.player.Values[projection.StatTOISeconds].Mean
		if kind != projection.PlayerKindSkater || games <= 0 || toi <= 0 {
			return 0, 0, fmt.Errorf("toi_per_game needs a skater with baseline games and ice time")
		}
		perGame := toi / games
		original = perGame * effect.IceTime
		effect.IceTime = o.Value / perGame
		return original, o.Value, nil
	case InputPowerPlayFactor:
		if kind != projection.PlayerKindSkater {
			return 0, 0, fmt.Errorf("power_play_factor applies to skaters")
		}
		original = effect.PowerPlay
		effect.PowerPlay = o.Value
		return original, o.Value, nil
	default:
		return 0, 0, fmt.Errorf("unknown input %q", o.Input)
	}
}

// replaceWorkload sets games (skaters) or starts (goalies) directly; the
// override replaces both availability and any role-based workload.
func (in effectInputs) replaceWorkload(value float64, stat projection.Stat, effect *Effect) (original, applied float64, err error) {
	baseline := in.player.Values[stat].Mean
	switch {
	case baseline <= 0:
		return 0, 0, fmt.Errorf("%s override needs a positive baseline %s", stat, stat)
	case value > in.season.Games:
		return 0, 0, fmt.Errorf("%s override %.1f exceeds the season's %.0f games", stat, value, in.season.Games)
	}
	original = baseline * effect.GamesFactor * effect.GoalieStarts
	effect.GamesFactor = value / baseline
	effect.GoalieStarts = noChange
	return original, value, nil
}
