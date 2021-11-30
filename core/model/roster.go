package model

import (
	"time"

	"gorm.io/gorm"
)

type RosterPlayer struct {
	gorm.Model
	//Team Team
	PlayerID          uint `gorm:"primaryKey"`
	Player            Player
	Month             time.Month `gorm:"primaryKey"`
	Day               uint8      `gorm:"primaryKey"`
	EligiblePositions string
	SelectedPosition  string
}
