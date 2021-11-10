package model

import (
	"encoding/xml"
)

type FantasyGame struct {
	XMLName xml.Name `xml:"game"`
	ID      int      `xml:"game_id"`
	Key     int      `xml:"game_key"`
}
