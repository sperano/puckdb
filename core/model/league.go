package model

import "gorm.io/gorm"

type League struct {
	gorm.Model
	ID                    int
	Key                   int
	Name                  string
	URL                   string
	LogoURL               string
	DraftStatus           string
	NumTeams              int
	EditKey               string
	LeagueUpdateTimestamp int
	ScoringType           string
	LeagueType            string
	IsProLeague           bool
	IsCashLeague          bool
	StartDate             string
	EndDate               string
	GameCode              string
	Season                int
}
