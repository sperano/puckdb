package model

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type League struct {
	gorm.Model
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

func (l *League) Ensure(db *gorm.DB) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}}, // TODO constants
		// TODO move that list elsewhere
		DoUpdates: clause.AssignmentColumns([]string{"key", "name",
			"url", "logo_url", "draft_status", "num_teams", "edit_key",
			"league_update_timestamp", "scoring_type", "league_type", "is_pro_league",
			"is_cash_league", "start_date", "end_date", "game_code", "season",
			"github_timestamp"}),
	}).Create(l).Error
}
