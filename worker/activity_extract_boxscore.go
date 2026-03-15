package worker

import (
	"strings"

	"github.com/sperano/nhl-api-go/nhl"
)

func addSkaterPlayer(players map[int64]PartialPlayer, s nhl.SkaterStats, teamID int64) {
	id := s.PlayerID.AsInt64()
	firstName, lastName := parseLocalizedName(s.Name)

	players[id] = PartialPlayer{
		ID:              id,
		FirstName:       firstName,
		LastName:        lastName,
		Position:        string(s.Position),
		SweaterNumber:   s.SweaterNumber,
		TeamID:          teamID,
		HasBoxscoreData: true,
	}
}

func addGoaliePlayer(players map[int64]PartialPlayer, g nhl.GoalieStats, teamID int64) {
	id := g.PlayerID.AsInt64()
	firstName, lastName := parseLocalizedName(g.Name)

	players[id] = PartialPlayer{
		ID:              id,
		FirstName:       firstName,
		LastName:        lastName,
		Position:        string(g.Position),
		SweaterNumber:   g.SweaterNumber,
		TeamID:          teamID,
		HasBoxscoreData: true,
	}
}

// parseLocalizedName splits "FirstName LastName" from nhl.LocalizedString.
func parseLocalizedName(name nhl.LocalizedString) (first, last string) {
	parts := strings.SplitN(name.Default, " ", 2)
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return name.Default, ""
}
