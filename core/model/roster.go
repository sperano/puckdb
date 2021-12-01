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
	Month             time.Month `gorm:"primaryKey"`
	Day               uint8      `gorm:"primaryKey"`
	EligiblePositions string
	SelectedPosition  string
	FeloScore         int
	FeloTier          string
	WaiverPriority    int
	NumberOfMoves     int
	NumberOfTrades    int
	EditorialTeamKey  string
	EditorialTeamAbbr string
	UniformNumber     int
}
