package model

import (
	"encoding/xml"
)

type TeamLogo struct {
	XMLName xml.Name `xml:"team_logo"`
	Size    string   `xml:"size"`
	URL     string   `xml:"url"`
}

type TeamLogos struct {
	XMLName xml.Name   `xml:"team_logos"`
	Slice   []TeamLogo `xml:"team_logo"`
}

type Team struct {
	XMLName        xml.Name `xml:"team"`
	Key            string   `xml:"team_key"`
	ID             int      `xml:"team_id"`
	Name           string   `xml:"name"`
	URL            string   `xml:"url"`
	TeamLogos      TeamLogos
	WaiverPriority int `xml:"waiver_priority"`
	NumberOfMoves  int `xml:"number_of_moves"`
	NumberOfTrades int `xml:"number_of_trades"`
}
