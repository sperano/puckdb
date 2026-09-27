package draftrank

import (
	"fmt"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/projection"
)

// teamEnvironmentNote states the projection's team-environment assumption
// and how many skaters it moved to a new club, or nothing when the model
// ran without it.
func teamEnvironmentNote(baseline projection.Snapshot) string {
	cfg := baseline.Config
	if cfg.TeamEnvironmentMaxChange <= 0 {
		return ""
	}
	var moved int
	for _, player := range baseline.Players {
		if env := player.TeamEnvironment; env != nil && env.FromTeamID != env.ToTeamID {
			moved++
		}
	}
	if moved == 0 {
		return fmt.Sprintf("team environment: no skater has a %s club other than their most recent one "+
			"(team changes are only known once that season's NHL rosters are imported)", draft.SeasonLabel(baseline.TargetSeason))
	}
	return fmt.Sprintf("team environment: %d skater(s) changed clubs for %s; their goals, assists, shots and power-play "+
		"points scale with the new club's scoring, shot and power-play-opportunity rates, regressed toward league "+
		"average by %.0f games and capped at ±%.0f%%",
		moved, draft.SeasonLabel(baseline.TargetSeason), cfg.TeamEnvironmentPriorGames, cfg.TeamEnvironmentMaxChange*percent)
}
