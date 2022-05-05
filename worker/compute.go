package worker

import (
	"context"
	"fmt"

	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/model"
)

func HandleComputeForTeam(ctx context.Context, yfh *core.YFH, teamID uint) error {
	// type computeStats struct {
	// 	Date      time.Time
	// 	PlayerID  uint
	// 	NHLTeamID uint
	// 	model.Stats
	// }
	dates, err := core.GetDateRange("", "")
	if err != nil {
		return err
	}
	db := yfh.GormDB
	for _, date := range dates {
		dateStr := fmt.Sprintf("%d-%02d-%02d", date.Year(), date.Month(), date.Day())
		//var rosterItems []model.RosterPlayer
		//,SUM(assists),
		var statsSkaters []model.StatsSkater
		query := `SELECT SUM(goals) AS goals, SUM(assists) AS assists, SUM(plus_minus) AS plus_minus, 
		SUM(penalty_minutes) AS penalty_minutes, SUM(shots_on_goal) AS shots_on_goal, 
		SUM(faceoffs_won) AS faceoofs_won, SUM(faceoffs_lost) AS faceoffs_lost, SUM(hits) AS hits, 
		SUM(blocks) AS blocks, SUM(time_on_ice) AS time_on_ice,	SUM(shifts) AS shifts, 
		SUM(take_aways) AS take_aways, SUM(give_aways) AS give_aways
		FROM roster_players rp JOIN player_stats ps ON rp.player_id=ps.player_id AND rp.date=ps.date
		WHERE rp.date=? AND rp.team_id=? AND selected_position>? AND selected_position<?`
		db.Raw(query, date, teamID, model.IR, model.G).Scan(&statsSkaters)
		if db.Error != nil {
			return db.Error
		}
		var statsGoalers []model.StatsGoaler
		query = `SELECT SUM(goal_against) AS goal_against, SUM(shots_against) AS shot_against, 
		SUM(saves) AS saves, SUM(goalie_time_on_ice) goalie_time_on_ice
		FROM roster_players rp JOIN player_stats ps ON rp.player_id=ps.player_id AND rp.date=ps.date
		WHERE rp.date=? AND rp.team_id=? AND selected_position=?`
		db.Raw(query, date, teamID, model.G).Scan(&statsGoalers)
		if db.Error != nil {
			return db.Error
		}
		fmt.Printf("%s teamID=%d skaters=%v goalers=%v\n", dateStr, teamID, statsSkaters, statsGoalers)
	}
	return nil
}
