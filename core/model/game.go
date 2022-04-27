package model

import (
	"time"

	"gorm.io/gorm"
)

type Game struct {
	Date            time.Time `json:"date" gorm:"primaryKey"`
	HomeTeamID      uint      `json:"home_team_id" gorm:"primaryKey"`
	HomeTeam        NHLTeam   `json:"home_team"`
	HomeTeamScore   uint      `json:"home_team_score"`
	AwayTeamID      uint      `json:"away_team_id" gorm:"primaryKey"`
	AwayTeam        NHLTeam   `json:"away_team"`
	AwayTeamScore   uint      `json:"away_team_score"`
	State           string    `json:"state"`
	Name            string    `json:"name" gorm:"index:,unique"`
	GithubTimestamp time.Time `json:"github_timestamp"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       gorm.DeletedAt `gorm:"index"`
}
