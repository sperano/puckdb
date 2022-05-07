package model

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var TeamSummaryCols = []string{
	"goal_against", "shots_against", "saves", "goalie_time_on_ice",
	"goals", "assists", "plus_minus", "penalty_minutes", "shots_on_goal", "faceoffs_won", "faceoffs_lost",
	"hits", "blocks", "time_on_ice", "shifts", "take_aways", "give_aways",
}

type TeamSummary struct {
	Date   time.Time `gorm:"primaryKey"`
	TeamID uint      `gorm:"primaryKey"`
	StatsSkater
	StatsGoaler
}

func (ts *TeamSummary) Ensure(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "date"}, {Name: "team_id"}}, // TODO constants
		DoUpdates: clause.AssignmentColumns(TeamSummaryCols),
	}).Create(ts).Error
}
