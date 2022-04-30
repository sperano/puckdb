package model

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TODO No ID attr please
type RosterPlayer struct {
	Date              time.Time `gorm:"primaryKey"`
	TeamID            uint      `gorm:"primaryKey"`
	Team              Team
	PlayerID          uint `gorm:"primaryKey"`
	Player            Player
	NHLTeamID         uint
	NHLTeam           NHLTeam
	EligiblePositions string
	SelectedPosition  string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	DeletedAt         gorm.DeletedAt `gorm:"index"`
}

func (rp *RosterPlayer) Ensure(db *gorm.DB) error {
	p := Player{
		Model: gorm.Model{
			ID: uint(rp.PlayerID),
		},
		NHLTeamID: rp.NHLTeamID,
	}
	if err := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}}, // TODO constants
		// TODO move that list elsewhere
		DoUpdates: clause.AssignmentColumns([]string{"nhl_team_id"}),
	}).Create(&p).Error; err != nil {
		return err
	}
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "date"}, {Name: "team_id"}, {Name: "player_id"}}, // TODO constants
		// TODO move that list elsewhere
		DoUpdates: clause.AssignmentColumns([]string{"nhl_team_id", "eligible_positions", "selected_position"}),
	}).Create(rp).Error
}
