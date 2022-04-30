package model

import (
	"time"

	"gorm.io/gorm"
)

type Manager struct {
	ManagerID       uint
	Nickname        string
	GUID            string
	EMail           string
	ImageURL        string
	GithubTimestamp time.Time
}

type Team struct {
	gorm.Model
	Key                   string
	Name                  string
	IsOwnedByCurrentLogin bool
	URL                   string
	DraftPosition         int
	HasDraftGrade         bool
	Manager               Manager `gorm:"embedded"`
	GithubTimestamp       time.Time
}
