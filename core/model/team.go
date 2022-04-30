package model

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Manager struct {
	ManagerID uint
	Nickname  string
	GUID      string
	EMail     string
	ImageURL  string
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

func (t *Team) Ensure(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}}, // TODO constants
		// TODO move that list elsewhere
		DoUpdates: clause.AssignmentColumns([]string{"key", "name", "is_owned_by_current_login", "url",
			"draft_position", "has_draft_grade", "manager_id", "nickname", "guid", "e_mail", "image_url",
			"github_timestamp"}),
	}).Create(t).Error
}
