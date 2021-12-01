package model

import "gorm.io/gorm"

type Manager struct {
	ManagerID int
	Nickname  string
	GUID      string
	EMail     string
	ImageURL  string
}

type Team struct {
	gorm.Model
	ID                    int `gorm:"primaryKey"`
	Key                   string
	Name                  string
	IsOwnedByCurrentLogin bool
	URL                   string
	DraftPosition         int
	HasDraftGrade         bool
	Manager               Manager `gorm:"embedded"`
}
