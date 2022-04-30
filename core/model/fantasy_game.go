package model

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type FantasyGame struct {
	gorm.Model
	Key                int
	Name               string
	Code               string
	Type               string
	URL                string
	Season             int
	IsRegistrationOver bool
	IsGameOver         bool
	IsOffseason        bool
	GithubTimestamp    time.Time
}

func (fg *FantasyGame) Ensure(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}}, // TODO constants
		// TODO move that list elsewhere
		DoUpdates: clause.AssignmentColumns([]string{"key", "name", "code", "type",
			"url", "season", "is_registration_over", "is_game_over", "is_offseason",
			"github_timestamp"}),
	}).Create(fg).Error
}
