package model

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

func (g *Game) Ensure(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "date"}, {Name: "home_team_id"}, {Name: "away_team_id"}}, // TODO constants
		// TODO move that list elsewhere
		DoUpdates: clause.AssignmentColumns([]string{"home_team_score", "away_team_score", "state", "name", "github_timestamp"}),
	}).Create(g).Error
}
