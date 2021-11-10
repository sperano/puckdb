package model

import (
	"encoding/xml"
	"fmt"
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

func (t *Team) String() string {
	str := fmt.Sprintf("%-10s%d\n%-10s%s\n%-10s%s\n%-10s%s\nTeam Logos:\n", "ID:", t.ID, "Key:", t.Key, "Name:", t.Name, "URL:", t.URL)
	for _, logo := range t.TeamLogos.Slice {
		str += fmt.Sprintf("  Size:  %s\n  URL:   %s\n", logo.Size, logo.URL)
	}
	str += fmt.Sprintf("Waiver priority: %d\n", t.WaiverPriority)
	str += fmt.Sprintf("Number of moves: %d\n", t.NumberOfMoves)
	str += fmt.Sprintf("Number of trades: %d\n", t.NumberOfTrades)
	return str
}
