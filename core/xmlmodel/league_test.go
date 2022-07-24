package xmlmodel

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestToLeagueModel(t *testing.T) {
	t.Parallel()
	xmlleague := League{
		ID:           22030,
		DraftStatus:  "postdraft",
		EditKey:      "2021-11-26",
		EndDate:      "2022-04-29",
		GameCode:     "nhl",
		IsCashLeague: true,
		IsProLeague:  true,
		Key:          "411.l.1005",
		LeagueType:   "private",
		LogoURL:      "foo",
		Name:         "Ligue des crapettes",
		NumTeams:     10,
		ScoringType:  "roto",
		Season:       2021,
		StartDate:    "2021-10-12",
		URL:          "https://hockey.fantasysports.yahoo.com/hockey/22030",
	}
	l, err := xmlleague.ToLeagueModel()
	assert.Nil(t, err)
	assert.Equal(t, 22030, int(l.ID))
	assert.Equal(t, "postdraft", l.DraftStatus)
	assert.Equal(t, "2021-11-26", l.EditKey)
	assert.Equal(t, 2022, l.EndDate.Year())
	assert.Equal(t, time.April, l.EndDate.Month())
	assert.Equal(t, 29, l.EndDate.Day())
	assert.Equal(t, "nhl", l.GameCode)
	assert.True(t, l.IsCashLeague)
	assert.True(t, l.IsProLeague)
	assert.Equal(t, "411.l.1005", l.Key)
	assert.Equal(t, "private", l.LeagueType)
	assert.Equal(t, "foo", l.LogoURL)
	assert.Equal(t, "Ligue des crapettes", l.Name)
	assert.Equal(t, 10, l.NumTeams)
	assert.Equal(t, "roto", l.ScoringType)
	assert.Equal(t, 2021, l.Season)
	assert.Equal(t, 2021, l.StartDate.Year())          // TODO use this instead of the config param
	assert.Equal(t, time.October, l.StartDate.Month()) // TODO use this instead of the config param
	assert.Equal(t, 12, l.StartDate.Day())             // TODO use this instead of the config param
	assert.Equal(t, "https://hockey.fantasysports.yahoo.com/hockey/22030", l.URL)
}

func TestToLeagueModelInvalidStartDate(t *testing.T) {
	t.Parallel()
	xmlleague := League{
		EndDate:   "2020-11-11",
		StartDate: "crap",
	}
	l, err := xmlleague.ToLeagueModel()
	assert.Nil(t, l)
	assert.IsType(t, &time.ParseError{}, err)
}

func TestToLeagueModelInvalidEndDate(t *testing.T) {
	t.Parallel()
	xmlleague := League{
		EndDate:   "crap",
		StartDate: "2020-11-11",
	}
	l, err := xmlleague.ToLeagueModel()
	assert.Nil(t, l)
	assert.IsType(t, &time.ParseError{}, err)
}
