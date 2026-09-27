package projection

import "fmt"

// NoTeamEnvironmentComparison names the comparison model of
// EvaluateTeamChanges: the same config with the team-environment
// adjustment disabled.
const NoTeamEnvironmentComparison = "no-team-environment"

// teamDependentStats are the skater stats the team-environment adjustment
// scales (points follow goals and assists).
var teamDependentStats = []Stat{StatGoals, StatAssists, StatPoints, StatShotsOnGoal, StatPowerPlayPoints}

// EvaluateTeamChanges is the held-out backtest of the team-environment
// adjustment. It scores the team-dependent stats of the skaters who moved:
// those whose target-season club (input.TargetTeams) differs from their
// most recent history club. The adjusted model is compared against the same
// config with the adjustment disabled and against the previous-season
// totals, both over the same players (movers with a previous-season row).
func EvaluateTeamChanges(cfg Config, input Input) ([]Evaluation, error) {
	if cfg.TeamEnvironmentMaxChange <= 0 {
		return nil, fmt.Errorf("team-change evaluation requires the team-environment adjustment")
	}
	if err := validateHeldOutInput(input); err != nil {
		return nil, err
	}
	adjusted, err := Generate(cfg, input)
	if err != nil {
		return nil, err
	}
	disabled := cfg
	disabled.TeamEnvironmentMaxChange = 0
	unadjusted, err := Generate(disabled, input)
	if err != nil {
		return nil, err
	}
	movers := teamMovers(adjusted)
	previous := onlyPlayers(aggregateSkaterValues(input.Skaters, historyFloorSeason(input.TargetSeason, 1)), movers)
	actual := onlyPlayers(aggregateSkaterValues(input.Skaters, input.TargetSeason), previous)
	unadjustedValues := onlyPlayers(snapshotMeans(unadjusted), previous)
	hash := evaluationDataHash(cfg, input)
	result := make([]Evaluation, 0, 2)
	for _, comparison := range []struct {
		model  string
		values map[string]map[Stat]float64
	}{
		{NoTeamEnvironmentComparison, unadjustedValues},
		{PreviousSeasonComparison, previous},
	} {
		evaluation := evaluateKind(adjusted, input.ObservedAt, hash, PlayerKindSkater,
			actual, comparison.values, teamDependentStats, comparison.model)
		if len(evaluation.Metrics) == 0 {
			return nil, fmt.Errorf("no skaters changed teams with outcomes in season %d", input.TargetSeason)
		}
		result = append(result, evaluation)
	}
	return result, nil
}

// teamMovers are the skaters whose target club is not their most recent
// history club.
func teamMovers(snapshot Snapshot) map[string]struct{} {
	movers := make(map[string]struct{})
	for _, player := range snapshot.Players {
		if env := player.TeamEnvironment; env != nil && env.FromTeamID != env.ToTeamID {
			movers[player.PlayerKey] = struct{}{}
		}
	}
	return movers
}

func onlyPlayers[V any](values map[string]map[Stat]float64, keep map[string]V) map[string]map[Stat]float64 {
	out := make(map[string]map[Stat]float64, len(keep))
	for key, playerValues := range values {
		if _, kept := keep[key]; kept {
			out[key] = playerValues
		}
	}
	return out
}

func snapshotMeans(snapshot Snapshot) map[string]map[Stat]float64 {
	out := make(map[string]map[Stat]float64, len(snapshot.Players))
	for _, player := range snapshot.Players {
		values := make(map[Stat]float64, len(player.Values))
		for stat, value := range player.Values {
			values[stat] = value.Mean
		}
		out[player.PlayerKey] = values
	}
	return out
}
