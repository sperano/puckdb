package model

import (
	"gorm.io/gorm"
)

type NHLConference struct {
	gorm.Model
	Name string `json:"name"`
}

type NHLDivision struct {
	gorm.Model
	Name            string        `json:"name"`
	NHLConferenceID uint          `json:"nhl_conference_id"`
	NHLConference   NHLConference `json:"nhl_conference"`
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
