package xmlmodel

import "encoding/xml"

type FantasyContent struct {
	XMLName xml.Name    `xml:"fantasy_content"`
	Game    FantasyGame `xml:"game"`
	League  League      `xml:"league"`
	Team    Team        `xml:"team"`
}
