package xmlmodel

import (
	"encoding/xml"

	"github.com/ericsperano/yfh/core/model"
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

func (fg *FantasyGame) ToGormModel() *model.FantasyGame {
	return &model.FantasyGame{
		ID:                 fg.ID,
		Key:                fg.Key,
		Name:               fg.Name,
		Code:               fg.Code,
		Type:               fg.Type,
		URL:                fg.URL,
		Season:             fg.Season,
		IsRegistrationOver: fg.IsRegistrationOver,
		IsGameOver:         fg.IsGameOver,
		IsOffseason:        fg.IsOffseason,
	}
}
