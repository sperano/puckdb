package model

import (
	"encoding/xml"
)

type FantasyGame struct {
	XMLName            xml.Name `xml:"game"`
	ID                 int      `xml:"game_id"`
	Key                int      `xml:"game_key"`
	Name               string   `xml:"name"`
	Code               string   `xml:"code"`
	Type               string   `xml:"type"`
	URL                string   `xml:"url"`
	Season             int      `xml:"season"`
	IsRegistrationOver bool     `xml:"is_registration_over"`
	IsGameOver         bool     `xml:"is_game_over"`
	IsOffseason        bool     `xml:"is_offseason"`
}
