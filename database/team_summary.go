package database

import (
	"gorm.io/gorm/clause"
	"time"

	"gorm.io/gorm"
)

var tsummaryUpdateCols = []string{
	CGoalAgainst, CShotsAgainst, CSaves, CGoalieTimeOnIce,
	CGoals, CAssists, CPlusMinus, CPenaltyMinutes, CShotsOnGoal, CFaceoffsWon, CFaceoffsLost,
	CHits, CBlocks, CTimeOnIce, CShifts, CTakeAways, CGiveAways, CSourceVersion,
}

type TeamSummary struct {
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     gorm.DeletedAt `gorm:"index"`
	Date          time.Time      `gorm:"primaryKey"`
	TeamID        uint           `gorm:"primaryKey"`
	SourceVersion time.Time
	StatsGoaler
	StatsSkater
}

func (ts *TeamSummary) SetSourceVersion(time time.Time) {
	ts.SourceVersion = time
}

func (ts *TeamSummary) Ensure(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: CDate}, {Name: CTeamID}},
		DoUpdates: clause.AssignmentColumns(tsummaryUpdateCols),
	}).Create(ts).Error
}

func DiffTeamSummary(current *TeamSummary, previous *TeamSummary) []DifferentAttr {
	diffs := make([]DifferentAttr, 0)
	if current.Date != previous.Date {
		diffs = append(diffs, &Different[time.Time]{name: ADate, previous: previous.Date, current: current.Date})
	}
	if current.TeamID != previous.TeamID {
		diffs = append(diffs, &Different[uint]{name: "TeamID", previous: previous.TeamID, current: current.TeamID})
	}
	diffs = append(diffs, DiffStatsGoaler(&current.StatsGoaler, &previous.StatsGoaler)...)
	return append(diffs, DiffStatsSkater(&current.StatsSkater, &previous.StatsSkater)...)
}
