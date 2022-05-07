package model

import (
	"time"

	"gorm.io/gorm"
)

type Standing struct {
	TeamID uint      `gorm:"primaryKey"`
	Date   time.Time `gorm:"primaryKey"`
	StatsGoaler
	StatsSkater
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (s *Standing) AddSkatersStats(stats *StatsSkater) {
	s.Assists += stats.Assists
	s.Blocks += stats.Blocks
	s.FaceoffsLost += stats.FaceoffsLost
	s.FaceoffsWon += stats.FaceoffsWon
	s.GiveAways += stats.GiveAways
	s.Goals += stats.Goals
	s.Hits += stats.Hits
	s.PenaltyMinutes += stats.PenaltyMinutes
	s.PlusMinus += stats.PlusMinus
	s.Shifts += stats.Shifts
	s.ShotsOnGoal += stats.ShotsOnGoal
	s.TakeAways += stats.TakeAways
	s.TimeOnIce += stats.TimeOnIce
}

func (s *Standing) AddGoalersStats(stats *StatsGoaler) {
	s.GoalAgainst += stats.GoalAgainst
	s.GoalieTimeOnIce += stats.GoalieTimeOnIce
	s.Saves += stats.Saves
	s.ShotsAgainst += stats.ShotsAgainst
}
