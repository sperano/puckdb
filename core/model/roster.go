package model

import (
	"time"

	"gorm.io/gorm"
)

// TODO No ID attr please
type RosterPlayer struct {
	gorm.Model
	TeamID            uint `gorm:"primaryKey"`
	Team              Team
	PlayerID          uint `gorm:"primaryKey"`
	Player            Player
	NHLTeamID         uint
	NHLTeam           NHLTeam
	Date              time.Time
	EligiblePositions string
	SelectedPosition  string
}
