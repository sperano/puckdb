package model

import (
	"time"

	"gorm.io/gorm"
)

type MetaType int32

const (
	MetaFantasyGame MetaType = iota
	MetaLeague
	MetaGame
)

type Meta struct {
	gorm.Model
	Type             MetaType
	Date             time.Time
	Imported         bool
	GithubFilesCount int
}
