package model

import (
	"time"

	"gorm.io/gorm"
)

type League struct {
	gorm.Model
	ID                    int
	Key                   string
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
	StartDate             time.Time
	EndDate               time.Time // TODO specific sql time type?
	GameCode              string
	Season                int
	GithubTimestamp       time.Time
}
