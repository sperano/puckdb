package config

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetSeasons(t *testing.T) {
	t.Parallel()
	seasons, err := getYahooSeasons("../test-data/config/seasons.yaml")
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, 2, len(seasons))

	// Check 2021 season
	season2021, ok := seasons[2021]
	assert.True(t, ok)
	assert.Equal(t, 1, len(season2021.Leagues))
	assert.Equal(t, 22030, season2021.Leagues[0].LeagueID)
	assert.Equal(t, []int{1, 2, 3, 4, 6, 7, 8, 9, 10, 11}, season2021.Leagues[0].TeamIDs)

	// Check 2022 season
	season2022, ok := seasons[2022]
	assert.True(t, ok)
	assert.Equal(t, 2, len(season2022.Leagues))
	assert.Equal(t, 1003, season2022.Leagues[0].LeagueID)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, season2022.Leagues[0].TeamIDs)
	assert.Equal(t, 12345, season2022.Leagues[1].LeagueID)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, season2022.Leagues[1].TeamIDs)
}

func TestGetSeasons_FileNotFound(t *testing.T) {
	t.Parallel()
	seasons, err := getYahooSeasons("foo")
	assert.Nil(t, seasons)
	assert.True(t, strings.HasPrefix(err.Error(), "can't read seasons config file foo: "))
	assert.True(t, errors.Is(err, os.ErrNotExist))
}

func TestGetSeasons_UnmarshallErr(t *testing.T) {
	t.Parallel()
	seasons, err := getYahooSeasons("../test-data/cache/2022/teams/team-07/team-07_20221025112231.xml")
	assert.Nil(t, seasons)
	assert.True(t, strings.HasPrefix(err.Error(), "can't unmarshal seasons config file ../test-data/cache/2022/teams/team-07/team-07_20221025112231.xml: "))
}

func TestSeasonsMapAccess(t *testing.T) {
	t.Parallel()
	seasons, err := getYahooSeasons("../test-data/config/seasons.yaml")
	if err != nil {
		t.Fatal(err)
	}

	// Test direct map access
	season, ok := seasons[2021]
	assert.True(t, ok)
	assert.Equal(t, 1, len(season.Leagues))

	// Test non-existent season
	_, ok = seasons[9999]
	assert.False(t, ok)
}

func TestLeagueGet(t *testing.T) {
	t.Parallel()
	seasons, err := getYahooSeasons("../test-data/config/seasons.yaml")
	if err != nil {
		t.Fatal(err)
	}
	season, ok := seasons[2021]
	if !ok {
		t.Fatal("season 2021 not found")
	}
	league, err := season.GetLeague(22030)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, 22030, league.LeagueID)
}

func TestLeagueGet_Error(t *testing.T) {
	t.Parallel()
	seasons, err := getYahooSeasons("../test-data/config/seasons.yaml")
	if err != nil {
		t.Fatal(err)
	}
	season, ok := seasons[2021]
	if !ok {
		t.Fatal("season 2021 not found")
	}
	_, err = season.GetLeague(9999)
	assert.Equal(t, "league not found: 9999", err.Error())
}
