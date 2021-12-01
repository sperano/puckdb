package xmlmodel

import (
	"encoding/xml"
	"strings"
	"time"

	"github.com/ericsperano/yfh/core/model"
)

type Roster struct {
	XMLName      xml.Name `xml:"roster"`
	CoverageType string   `xml:"coverage_type"`
	Date         string   `xml:"date"`
	IsEditable   bool     `xml:"is_editable"`
	Players      Players
}

type TeamLogo struct {
	XMLName xml.Name `xml:"team_logo"`
	Size    string   `xml:"size"`
	URL     string   `xml:"url"`
}

type TeamLogos struct {
	XMLName xml.Name   `xml:"team_logos"`
	Slice   []TeamLogo `xml:"team_logo"`
}

type Manager struct {
	XMLName        xml.Name `xml:"manager"`
	ID             int      `xml:"manager_id"`
	Nickname       string   `xml:"nickname"`
	GUID           string   `xml:"guid"`
	IsCurrentLogin bool     `xml:"is_current_login"`
	EMail          string   `xml:"email"`
	ImageURL       string   `xml:"image_url"`
	FeloScore      int      `xml:"felo_score"`
	FeloTier       string   `xml:"felo_tier"`
}

type Managers struct {
	XMLName xml.Name  `xml:"managers"`
	Slice   []Manager `xml:"manager"`
}

type Team struct {
	XMLName               xml.Name `xml:"team"`
	Key                   string   `xml:"team_key"`
	ID                    int      `xml:"team_id"`
	Name                  string   `xml:"name"`
	IsOwnedByCurrentLogin bool     `xml:"is_owned_by_current_login"`
	URL                   string   `xml:"url"`
	TeamLogos             TeamLogos
	WaiverPriority        int `xml:"waiver_priority"`
	NumberOfMoves         int `xml:"number_of_moves"`
	NumberOfTrades        int `xml:"number_of_trades"`
	/*
	  <roster_adds>
	   <coverage_type>week</coverage_type>
	   <coverage_value>5</coverage_value>
	   <value>0</value>
	  </roster_adds>
	*/
	LeagueScoringType string `xml:"league_scoring_type"`
	DraftPosition     int    `xml:"draft_position"`
	HasDraftGrade     bool   `xml:"has_draft_grade"`
	Managers          Managers
	Roster            Roster
}

func (t *Team) ToTeamModel() *model.Team {
	man := t.Managers.Slice[0]
	return &model.Team{
		ID:                    t.ID,
		Key:                   t.Key,
		Name:                  t.Name,
		IsOwnedByCurrentLogin: t.IsOwnedByCurrentLogin,
		URL:                   t.URL,
		DraftPosition:         t.DraftPosition,
		HasDraftGrade:         t.HasDraftGrade,
		Manager: model.Manager{
			ManagerID: man.ID,
			Nickname:  man.Nickname,
			GUID:      man.GUID,
			EMail:     man.EMail,
			ImageURL:  man.ImageURL,
		},
	}
}

func (t *Team) ToRosterPlayerModel(xmlplayer *Player) (*model.RosterPlayer, error) {
	date, err := time.Parse("2006-01-02", t.Roster.Date)
	if err != nil {
		return nil, err
	}
	return &model.RosterPlayer{
		PlayerID:          uint(xmlplayer.ID),
		Month:             date.Month(),
		Day:               uint8(date.Day()),
		EligiblePositions: strings.Join(xmlplayer.EligiblePositions, " "),
		SelectedPosition:  xmlplayer.SelectedPosition.Position,
		FeloScore:         t.Managers.Slice[0].FeloScore,
		FeloTier:          t.Managers.Slice[0].FeloTier,
		WaiverPriority:    t.WaiverPriority,
		NumberOfMoves:     t.NumberOfMoves,
		NumberOfTrades:    t.NumberOfTrades,
		EditorialTeamKey:  xmlplayer.EditorialTeamKey,
		EditorialTeamAbbr: xmlplayer.EditorialTeamAbbr,
		UniformNumber:     xmlplayer.UniformNumber,
	}, nil
}
