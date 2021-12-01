package xmlmodel

import (
	"encoding/xml"

	"github.com/ericsperano/yfh/core/model"
)

type League struct {
	XMLName               xml.Name `xml:"league"`
	ID                    int      `xml:"league_id"`
	Key                   int      `xml:"league_key"`
	Name                  string   `xml:"name"`
	URL                   string   `xml:"url"`
	LogoURL               string   `xml:"logo_url"`
	DraftStatus           string   `xml:"draft_status"`
	NumTeams              int      `xml:"num_teams"`
	EditKey               string   `xml:"edit_key"`
	LeagueUpdateTimestamp int      `xml:"league_update_timestamp"`
	ScoringType           string   `xml:"scoring_type"`
	LeagueType            string   `xml:"league_type"`
	IsProLeague           bool     `xml:"is_pro_league"`
	IsCashLeague          bool     `xml:"is_cash_league"`
	StartDate             string   `xml:"start_date"`
	EndDate               string   `xml:"end_date"`
	GameCode              string   `xml:"game_code"`
	Season                int      `xml:"season"`
}

func (l *League) ToLeagueModel() *model.League {
	return &model.League{
		ID:                    l.ID,
		Key:                   l.Key,
		Name:                  l.Name,
		URL:                   l.URL,
		LogoURL:               l.LogoURL,
		DraftStatus:           l.DraftStatus,
		NumTeams:              l.NumTeams,
		EditKey:               l.EditKey,
		LeagueUpdateTimestamp: l.LeagueUpdateTimestamp,
		ScoringType:           l.ScoringType,
		LeagueType:            l.LeagueType,
		IsProLeague:           l.IsProLeague,
		IsCashLeague:          l.IsCashLeague,
		StartDate:             l.StartDate,
		EndDate:               l.EndDate,
		GameCode:              l.GameCode,
		Season:                l.Season,
	}
}
