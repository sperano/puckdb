package database

import (
	gqlmodel "github.com/sperano/yfh/graph/model"
	"gorm.io/gorm"
)

// NHLConference is the GORM model for nhl_conferences table.
// Used for GORM migrations and foreign key relationships.
// Query operations should use SQLC instead.
type NHLConference struct {
	gorm.Model
	Name string `json:"name"`
}

func (c *NHLConference) GraphQLModel() *gqlmodel.NHLConference {
	return &gqlmodel.NHLConference{
		ID:   int(c.ID),
		Name: c.Name,
	}
}

// NHLDivision is the GORM model for nhl_divisions table.
// Used for GORM migrations and foreign key relationships.
// Query operations should use SQLC instead.
type NHLDivision struct {
	gorm.Model
	Name            string        `json:"name"`
	NHLConferenceID uint          `json:"nhl_conference_id"`
	NHLConference   NHLConference `json:"nhl_conference"`
}

func (d *NHLDivision) GraphQLModel() *gqlmodel.NHLDivision {
	return &gqlmodel.NHLDivision{
		ID:         int(d.ID),
		Name:       d.Name,
		Conference: d.NHLConference.GraphQLModel(),
	}
}

// NHLTeam is the GORM model for nhl_teams table.
// Used for GORM migrations and foreign key relationships.
// Query operations should use SQLC instead.
type NHLTeam struct {
	gorm.Model
	City          string      `json:"city"`
	Name          string      `json:"name"`
	Abbreviation  string      `json:"abbreviation"`
	NHLDivisionID uint        `json:"nhl_division_id"`
	NHLDivision   NHLDivision `json:"nhl_division"`
	NHLHomeLink   string      `json:"nhl_home_link"`
	YahooHomeLink string      `json:"yahoo_home_link"`
	SmallLogoURL  string      `json:"small_logo_url"`
	LargeLogoURL  string      `json:"large_logo_url"`
	AllStars      bool        `json:"all_stars"`
}

func (t *NHLTeam) GraphQLModel() *gqlmodel.NHLTeam {
	return &gqlmodel.NHLTeam{
		ID:            int(t.ID),
		City:          t.City,
		Abbreviation:  t.Abbreviation,
		Name:          t.Name,
		NhlHomeLink:   t.NHLHomeLink,
		YahooHomeLink: t.YahooHomeLink,
		Division:      t.NHLDivision.GraphQLModel(),
		SmallLogoURL:  t.SmallLogoURL,
		LargeLogoURL:  t.LargeLogoURL,
	}
}

func DiffNHLTeam(current *NHLTeam, previous *NHLTeam) []DifferentAttr {
	diffs := make([]DifferentAttr, 0)
	if current.ID != previous.ID {
		diffs = append(diffs, &Different[uint]{
			name:     AID,
			previous: previous.ID,
			current:  current.ID,
		})
	}
	return diffs
}
