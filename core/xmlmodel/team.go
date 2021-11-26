package xmlmodel

import (
	"encoding/xml"

	"github.com/ericsperano/yfh/core/model"
)

type PlayerName struct {
	XMLName    xml.Name `xml:"name"`
	Full       string   `xml:"full"`
	First      string   `xml:"first"`
	Last       string   `xml:"last"`
	ASCIIFirst string   `xml:"ascii_first"`
	ASCIILast  string   `xml:"ascii_last"`
}

type PlayerHeadshot struct {
	XMLName xml.Name `xml:"headshot"`
	URL     string   `xml:"url"`
	Size    string   `xml:"size"`
}

type PlayerSelectedPosition struct {
	XMLName      xml.Name `xml:"selected_position"`
	CoverageType string   `xml:"coverage_type"`
	Date         string   `xml:"date"`
	Position     string   `xml:"position"`
	IsFlex       bool     `xml:"is_flex"`
}

/*
<>
<position>C</position>
<position>Util</position>
</>
*/

type Player struct {
	XMLName                  xml.Name `xml:"player"`
	Key                      string   `xml:"player_key"`
	ID                       int      `xml:"player_id"`
	Name                     PlayerName
	EditorialPlayerKey       string `xml:"editorial_player_key"`
	EditorialTeamKey         string `xml:"editorial_team_key"`
	EditorialTeamFullName    string `xml:"editorial_team_full_name"`
	EditorialTeamAbbr        string `xml:"editorial_team_abbr"`
	UniformNumber            int    `xml:"uniform_number"`
	DisplayPosition          string `xml:"display_position"`
	Headshot                 PlayerHeadshot
	ImageURL                 string   `xml:"image_url"`
	IsUndroppable            bool     `xml:"is_undroppable"`
	PositionType             string   `xml:"position_type"`
	PrimaryPosition          string   `xml:"primary_position"`
	EligiblePositions        []string `xml:"eligible_positions>position"`
	HasPlayerNotes           bool     `xml:"has_player_notes"`
	PlayerNotesLastTimestamp int      `xml:"player_notes_last_timestamp"`
	SelectedPosition         PlayerSelectedPosition
	IsEditable               bool `xml:"is_editable"`
}

func (p *Player) ToGormModel() *model.Player {
	return &model.Player{
		ID:  p.ID,
		Key: p.Key,
	}
}

type Players struct {
	XMLName xml.Name `xml:"players"`
	Slice   []Player `xml:"player"`
	Count   int      `xml:"count,attr"`
}

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
