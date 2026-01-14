package config

import (
	"errors"
	"github.com/golang-module/carbon/v2"
	"github.com/stretchr/testify/assert"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGetSeasons(t *testing.T) {
	t.Parallel()
	seasons, err := getSeasons("../test-data/config/seasons.yaml")
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, 2, len(seasons))
	assert.Equal(t, 1, len(seasons[0].Leagues))
	assert.Equal(t, time.Date(2021, time.October, 12, 0, 0, 0, 0, time.UTC), seasons[0].Start)
	assert.Equal(t, time.Date(2022, time.April, 29, 0, 0, 0, 0, time.UTC), seasons[0].End)
	assert.Equal(t, 123, seasons[0].GameKey)
	assert.Equal(t, 22030, seasons[0].Leagues[0].LeagueID)
	assert.Equal(t, []int{1, 2, 3, 4, 6, 7, 8, 9, 10, 11}, seasons[0].Leagues[0].TeamIDs)
	assert.Equal(t, time.Date(2022, time.October, 7, 0, 0, 0, 0, time.UTC), seasons[1].Start)
	assert.Equal(t, time.Date(2023, time.April, 13, 0, 0, 0, 0, time.UTC), seasons[1].End)
	assert.Equal(t, 456, seasons[1].GameKey)
	assert.Equal(t, 1003, seasons[1].Leagues[0].LeagueID)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, seasons[1].Leagues[0].TeamIDs)
	assert.Equal(t, 12345, seasons[1].Leagues[1].LeagueID)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, seasons[1].Leagues[1].TeamIDs)
}

func TestSeason_StartYear(t *testing.T) {
	t.Parallel()
	s := Season{Start: carbon.Parse("2021-10-10").ToStdTime()}
	assert.Equal(t, 2021, s.StartYear())
}

func TestSeason_AsInts(t *testing.T) {
	t.Parallel()
	ts1 := carbon.Parse("2021-10-10").ToStdTime()
	s1 := Season{Start: ts1}
	ts2 := carbon.Parse("2022-10-10").ToStdTime()
	s2 := Season{Start: ts2}
	seasons := Seasons{s1, s2}
	assert.Equal(t, []int{2021, 2022}, seasons.AsInts())
}

func TestGetSeasons_FileNotFound(t *testing.T) {
	t.Parallel()
	seasons, err := getSeasons("foo")
	assert.Nil(t, seasons)
	assert.True(t, strings.HasPrefix(err.Error(), "can't read seasons config file foo: "))
	assert.True(t, errors.Is(err, os.ErrNotExist))
}

func TestGetSeasons_UnmarshallErr(t *testing.T) {
	t.Parallel()
	seasons, err := getSeasons("../test-data/cache/2022/teams/team-07/team-07_20221025112231.xml")
	assert.Nil(t, seasons)
	assert.True(t, strings.HasPrefix(err.Error(), "can't unmarshal seasons config file ../test-data/cache/2022/teams/team-07/team-07_20221025112231.xml: "))
	//TODO test wrapped error
	//assert.True(t, errors.Is(err, &yaml.TypeError{}))
}

func TestSeasonsGet(t *testing.T) {
	t.Parallel()
	seasons, err := getSeasons("../test-data/config/seasons.yaml")
	if err != nil {
		t.Fatal(err)
	}
	season, err := seasons.Get(2021)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, 2021, season.StartYear())
}

func TestSeasonsGet_Error(t *testing.T) {
	t.Parallel()
	seasons, err := getSeasons("../test-data/config/seasons.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = seasons.Get(9999)
	assert.Equal(t, "season not found: 9999", err.Error())
}

func TestLeagueGet(t *testing.T) {
	t.Parallel()
	seasons, err := getSeasons("../test-data/config/seasons.yaml")
	if err != nil {
		t.Fatal(err)
	}
	season, err := seasons.Get(2021)
	if err != nil {
		t.Fatal(err)
	}
	league, err := season.GetLeague(22030)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, 22030, league.LeagueID)
}

func TestLeagueGet_Error(t *testing.T) {
	t.Parallel()
	seasons, err := getSeasons("../test-data/config/seasons.yaml")
	if err != nil {
		t.Fatal(err)
	}
	season, err := seasons.Get(2021)
	if err != nil {
		t.Fatal(err)
	}
	_, err = season.GetLeague(9999)
	assert.Equal(t, "league not found: 9999", err.Error())
}
