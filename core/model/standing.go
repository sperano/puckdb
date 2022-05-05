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
