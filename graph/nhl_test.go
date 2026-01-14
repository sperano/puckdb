package graph

import (
	"testing"

	"github.com/sperano/yfh/sqlcdb"
	"github.com/stretchr/testify/assert"
)

func TestSqlcDivisionToGQL(t *testing.T) {
	t.Parallel()
	row := sqlcdb.GetAllNHLDivisionsRow{
		ID:       2,
		Name:     "Atlantic",
		ConfID:   1,
		ConfName: "Eastern",
	}

	result := sqlcDivisionToGQL(row)

	assert.Equal(t, 2, result.ID)
	assert.Equal(t, "Atlantic", result.Name)
	assert.NotNil(t, result.Conference)
	assert.Equal(t, 1, result.Conference.ID)
	assert.Equal(t, "Eastern", result.Conference.Name)
}

func TestSqlcTeamRowToGQL(t *testing.T) {
	t.Parallel()
	row := sqlcdb.GetAllNHLTeamsRow{
		ID:            1,
		City:          "Boston",
		Name:          "Bruins",
		Abbreviation:  "BOS",
		NhlHomeLink:   "https://www.nhl.com/bruins",
		YahooHomeLink: "https://sports.yahoo.com/nhl/teams/boston/",
		SmallLogoUrl:  "https://example.com/small.png",
		LargeLogoUrl:  "https://example.com/large.png",
		DivID:         2,
		DivName:       "Atlantic",
		ConfID:        1,
		ConfName:      "Eastern",
	}

	result := sqlcTeamRowToGQL(row)

	assert.Equal(t, 1, result.ID)
	assert.Equal(t, "Boston", result.City)
	assert.Equal(t, "Bruins", result.Name)
	assert.Equal(t, "BOS", result.Abbreviation)
	assert.Equal(t, "https://www.nhl.com/bruins", result.NhlHomeLink)
	assert.Equal(t, "https://sports.yahoo.com/nhl/teams/boston/", result.YahooHomeLink)
	assert.Equal(t, "https://example.com/small.png", result.SmallLogoURL)
	assert.Equal(t, "https://example.com/large.png", result.LargeLogoURL)
	assert.NotNil(t, result.Division)
	assert.Equal(t, 2, result.Division.ID)
	assert.Equal(t, "Atlantic", result.Division.Name)
	assert.NotNil(t, result.Division.Conference)
	assert.Equal(t, 1, result.Division.Conference.ID)
	assert.Equal(t, "Eastern", result.Division.Conference.Name)
}

func TestSqlcSingleTeamRowToGQL(t *testing.T) {
	t.Parallel()
	row := sqlcdb.GetNHLTeamRow{
		ID:            58,
		City:          "Vegas",
		Name:          "Golden Knights",
		Abbreviation:  "VGS",
		NhlHomeLink:   "https://www.nhl.com/goldenknights",
		YahooHomeLink: "https://sports.yahoo.com/nhl/teams/vegas/",
		SmallLogoUrl:  "https://example.com/vgs-small.png",
		LargeLogoUrl:  "https://example.com/vgs-large.png",
		DivID:         4,
		DivName:       "Pacific",
		ConfID:        2,
		ConfName:      "Western",
	}

	result := sqlcSingleTeamRowToGQL(row)

	assert.Equal(t, 58, result.ID)
	assert.Equal(t, "Vegas", result.City)
	assert.Equal(t, "Golden Knights", result.Name)
	assert.Equal(t, "VGS", result.Abbreviation)
	assert.Equal(t, "https://www.nhl.com/goldenknights", result.NhlHomeLink)
	assert.Equal(t, "https://sports.yahoo.com/nhl/teams/vegas/", result.YahooHomeLink)
	assert.Equal(t, "https://example.com/vgs-small.png", result.SmallLogoURL)
	assert.Equal(t, "https://example.com/vgs-large.png", result.LargeLogoURL)
	assert.NotNil(t, result.Division)
	assert.Equal(t, 4, result.Division.ID)
	assert.Equal(t, "Pacific", result.Division.Name)
	assert.NotNil(t, result.Division.Conference)
	assert.Equal(t, 2, result.Division.Conference.ID)
	assert.Equal(t, "Western", result.Division.Conference.Name)
}

func TestSqlcTeamRowToGQL_EmptyFields(t *testing.T) {
	t.Parallel()
	row := sqlcdb.GetAllNHLTeamsRow{
		ID:            31,
		City:          "Atlantic",
		Name:          "All-Stars",
		Abbreviation:  "AAS",
		NhlHomeLink:   "",
		YahooHomeLink: "",
		SmallLogoUrl:  "",
		LargeLogoUrl:  "",
		DivID:         2,
		DivName:       "Atlantic",
		ConfID:        1,
		ConfName:      "Eastern",
	}

	result := sqlcTeamRowToGQL(row)

	assert.Equal(t, 31, result.ID)
	assert.Equal(t, "Atlantic", result.City)
	assert.Equal(t, "All-Stars", result.Name)
	assert.Equal(t, "AAS", result.Abbreviation)
	assert.Equal(t, "", result.NhlHomeLink)
	assert.Equal(t, "", result.YahooHomeLink)
	assert.Equal(t, "", result.SmallLogoURL)
	assert.Equal(t, "", result.LargeLogoURL)
}
