package draft

import (
	"fmt"
	"strings"

	"github.com/sperano/puckdb/internal/projection"
)

const percent = 100

// teamEnvironmentFactors lists the stats a team-environment adjustment
// scales, in explanation order.
var teamEnvironmentFactors = []struct {
	stat  projection.Stat
	label string
}{
	{projection.StatGoals, "goals"},
	{projection.StatAssists, "assists"},
	{projection.StatShotsOnGoal, "shots"},
	{projection.StatPowerPlayPoints, "power-play points"},
}

// teamEnvironmentExplanation says how a club change scaled a skater's
// projection, e.g. "team environment: NYR → LAK (100% of weighted history
// with other clubs); goals ×1.040, assists ×1.040, shots ×0.970,
// power-play points ×1.020; club rates regressed toward league average".
func teamEnvironmentExplanation(env projection.TeamEnvironment) string {
	move := fmt.Sprintf("%s → %s", teamLabel(env.FromTeam, env.FromTeamID), teamLabel(env.ToTeam, env.ToTeamID))
	if env.FromTeamID == env.ToTeamID {
		move = "stays with " + teamLabel(env.ToTeam, env.ToTeamID)
	}
	factors := make([]string, 0, len(teamEnvironmentFactors))
	for _, f := range teamEnvironmentFactors {
		if factor, exists := env.Factors[f.stat]; exists {
			factors = append(factors, fmt.Sprintf("%s ×%.3f", f.label, factor))
		}
	}
	return fmt.Sprintf("team environment: %s (%.0f%% of weighted history with other clubs); %s; club rates regressed toward league average",
		move, env.OtherClubShare*percent, strings.Join(factors, ", "))
}

func teamLabel(abbrev string, teamID int64) string {
	if abbrev != "" {
		return abbrev
	}
	return fmt.Sprintf("team %d", teamID)
}
