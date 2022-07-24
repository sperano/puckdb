package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/ericsperano/yfh/core/model"
	"github.com/ericsperano/yfh/core/xmlmodel"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

func EnsureNHLPlayers(ctx context.Context, db *gorm.DB, players []xmlmodel.PlayerInfo) error {
	for _, pi := range players {
		player, err := pi.ToPlayerModel()
		if err != nil {
			return fmt.Errorf("error while converting player model %s (%s %s) -> %w", pi.PlayerID, pi.FirstName, pi.LastName, err)
		}
		log.Debug().Msgf("Ensuring %s %s in db", player.FirstName, player.LastName)
		if err := player.Ensure(db); err != nil {
			return err
		}
	}
	return nil
}

func logPlayerStats(date time.Time, playerStats *model.PlayerStats) {
	log.Debug().
		Str("date", getDateStr(date)).
		Uint("pid", playerStats.PlayerID).
		Uint("nhl_team_id", playerStats.NHLTeamID).
		Uint("ga", playerStats.GoalAgainst).
		Uint("sa", playerStats.ShotsAgainst).
		Uint("sv", playerStats.Saves).
		Uint("gtoi", playerStats.GoalieTimeOnIce).
		Uint("g", playerStats.Goals).
		Uint("a", playerStats.Assists).
		Int("+-", playerStats.PlusMinus).
		Uint("pim", playerStats.PenaltyMinutes).
		Uint("sog", playerStats.ShotsOnGoal).
		Uint("fw", playerStats.FaceoffsWon).
		Uint("fl", playerStats.FaceoffsLost).
		Uint("h", playerStats.Hits).
		Uint("b", playerStats.Blocks).
		Uint("toi", playerStats.TimeOnIce).
		Uint("sh", playerStats.Shifts).
		Uint("taw", playerStats.TakeAways).
		Uint("gaw", playerStats.GiveAways).
		Msg("Ensuring player stats")
}
