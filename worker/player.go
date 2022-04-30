package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/ericsperano/yfh/core/model"
	"github.com/ericsperano/yfh/core/xmlmodel"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

func EnsureNHLPlayers(ctx context.Context, db *gorm.DB, players []xmlmodel.PlayerInfo) error {
	for _, pi := range players {
		player, err := pi.ToPlayerModel()
		if err != nil {
			return fmt.Errorf("error while converting player model %s (%s %s) -> %w", pi.PlayerID, pi.FirstName, pi.LastName, err)
		}
		log.Debugf("Ensuring %s %s in db", player.FirstName, player.LastName)
		if err := player.Ensure(db); err != nil {
			return err
		}
	}
	return nil
}

func logPlayerStats(date time.Time, playerStats *model.PlayerStats) {
	log.Debugf("Ensuring player stats %4d-%02d-%02d pid=%d nhl_team_id=%d ga=%d sa=%d sv=%d sp=%d gtoi=%d g=%d a=%d +-=%d pim=%d sog=%d fw=%d fl=%d h=%d b=%d toi=%d fop=%d sh=%d taw=%d gaw=%d",
		date.Year(), date.Month(), date.Day(), playerStats.PlayerID, playerStats.NHLTeamID,
		playerStats.GoalAgainst,
		playerStats.ShotsAgainst,
		playerStats.Saves,
		playerStats.SavePercentage,
		playerStats.GoalieTimeOnIce,
		playerStats.Goals,
		playerStats.Assists,
		playerStats.PlusMinus,
		playerStats.PenaltyMinutes,
		playerStats.ShotsOnGoal,
		playerStats.FaceoffsWon,
		playerStats.FaceoffsLost,
		playerStats.Hits,
		playerStats.Blocks,
		playerStats.TimeOnIce,
		playerStats.FaceOffPercentage,
		playerStats.Shifts,
		playerStats.TakeAways,
		playerStats.GiveAways)
}
