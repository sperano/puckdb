package xmlmodel

import (
	"encoding/xml"
	"fmt"

	"github.com/ericsperano/yfh/core/model"
)

//////////////////////////////////////////////////////////////////////////////
type ErrInvalidFullName struct {
	Full  string
	First string
	Last  string
}

func (e *ErrInvalidFullName) Error() string {
	return fmt.Sprintf("\"%s\" != \"%s\" + \" \" + \"/%s\"", e.Full, e.First, e.Last)
}

//////////////////////////////////////////////////////////////////////////////
type ErrASCIIFirstName struct {
	Name  string
	ASCII string
}

func (e *ErrASCIIFirstName) Error() string {
	return fmt.Sprintf("error ASCII First Name\"%s\" != \"%s\"", e.Name, e.ASCII)
}

type ErrASCIILastName struct {
	Name  string
	ASCII string
}

func (e *ErrASCIILastName) Error() string {
	return fmt.Sprintf("error ASCII Last Name \"%s\" != \"%s\"", e.Name, e.ASCII)
}

//////////////////////////////////////////////////////////////////////////////

type PlayerName struct {
	XMLName    xml.Name `xml:"name"`
	Full       string   `xml:"full"`
	First      string   `xml:"first"`
	Last       string   `xml:"last"`
	ASCIIFirst string   `xml:"ascii_first"`
	ASCIILast  string   `xml:"ascii_last"`
}

func (n *PlayerName) Validate() error {
	full := fmt.Sprintf("%s %s", n.First, n.Last)
	if full != n.Full {
		return &ErrInvalidFullName{n.Full, n.First, n.Last}
	}
	if n.First != n.ASCIIFirst {
		return &ErrASCIIFirstName{n.First, n.ASCIIFirst}
	}
	if n.Last != n.ASCIILast {
		return &ErrASCIILastName{n.Last, n.ASCIILast}
	}
	return nil
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

func (p *Player) ToGormModel() (*model.Player, error) {
	if err := p.Name.Validate(); err != nil {
		return nil, err
	}
	return &model.Player{
		ID:        p.ID,
		Key:       p.Key,
		FirstName: p.Name.First,
		LastName:  p.Name.Last,
	}, nil
}

type Players struct {
	XMLName xml.Name `xml:"players"`
	Slice   []Player `xml:"player"`
	Count   int      `xml:"count,attr"`
}

//if model, err := xmlroster.ToGormModel(xmlplayer); err != nil {
