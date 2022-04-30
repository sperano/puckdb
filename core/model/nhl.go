package model

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type NHLConference struct {
	gorm.Model
	Name string `json:"name"`
}

func (c *NHLConference) Ensure(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"name"}),
	}).Create(c).Error
}

type NHLDivision struct {
	gorm.Model
	Name            string        `json:"name"`
	NHLConferenceID uint          `json:"nhl_conference_id"`
	NHLConference   NHLConference `json:"nhl_conference"`
}

func (d *NHLDivision) Ensure(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"name", "nhl_conference_id"}),
	}).Create(d).Error
}

type NHLTeam struct {
	gorm.Model
	City            string        `json:"city"`
	Name            string        `json:"name"`
	NHLDivisionID   uint          `json:"nhl_division_id"`
	NHLDivision     NHLDivision   `json:"nhl_division"`
	NHLConferenceID uint          `json:"nhl_conference_id"`
	NHLConference   NHLConference `json:"nhl_conference"`
	ScheduleLink    string
	HomeLink        string
}

func (t *NHLTeam) Ensure(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"city", "name", "nhl_division_id", "nhl_conference_id"}),
	}).Create(t).Error
}
