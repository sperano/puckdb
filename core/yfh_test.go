package core

import (
	"io/fs"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseTimestamp(t *testing.T) {
	t.Parallel()
	timestamp, err := ParseTimestamp("20210930024225")
	assert.Nil(t, err)
	assert.Equal(t, 2021, timestamp.Year())
	assert.Equal(t, time.September, timestamp.Month())
	assert.Equal(t, 30, timestamp.Day())
	assert.Equal(t, 2, timestamp.Hour())
	assert.Equal(t, 42, timestamp.Minute())
	assert.Equal(t, 25, timestamp.Second())
}

func TestGetTimestamp(t *testing.T) {
	t.Parallel()
	str := "20210930024225"
	timestamp, _ := ParseTimestamp(str)
	assert.Equal(t, str, GetTimestamp(timestamp))
}

func TestReaddirBadPath(t *testing.T) {
	t.Parallel()
	_, err := readdir("../test-data/readdir_000")
	assert.IsType(t, &fs.PathError{}, err)
}

func TestReaddir(t *testing.T) {
	t.Parallel()
	cfis, err := readdir("../test-data/readdir_1")
	assert.Nil(t, err)
	assert.Equal(t, 2, len(cfis))
	assert.Equal(t, "bar", cfis[0].Name)
	assert.Equal(t, "foo", cfis[1].Name)
	assert.Equal(t, 2021, cfis[0].Time.Year())
	assert.Equal(t, time.November, cfis[0].Time.Month())
	assert.Equal(t, 25, cfis[0].Time.Day())
	assert.Equal(t, 01, cfis[0].Time.Hour())
	assert.Equal(t, 43, cfis[0].Time.Minute())
	assert.Equal(t, 47, cfis[0].Time.Second())
}

func assertFind(t *testing.T, cfis []*CachedFileInfo) {
	assert.Equal(t, 3, len(cfis))
	assert.Equal(t, "foo", cfis[0].Name)
	assert.Equal(t, 59, cfis[0].Time.Minute())
	assert.Equal(t, 59, cfis[0].Time.Second())
	assert.Equal(t, "foo", cfis[1].Name)
	assert.Equal(t, 43, cfis[1].Time.Minute())
	assert.Equal(t, 47, cfis[1].Time.Second())
	assert.Equal(t, "foo", cfis[2].Name)
	assert.Equal(t, 1, cfis[2].Time.Minute())
	assert.Equal(t, 1, cfis[2].Time.Second())
}

func TestFindWithDir(t *testing.T) {
	t.Parallel()
	cache, err := NewCache(&Config{
		CachePath: "../test-data",
	})
	assert.Nil(t, err)
	cfis, err := cache.find("find_1", "foo")
	assert.Nil(t, err)
	assertFind(t, cfis)
}

func TestFindWithoutDir(t *testing.T) {
	t.Parallel()
	cache, err := NewCache(&Config{
		CachePath: "../test-data/find_1",
	})
	assert.Nil(t, err)
	cfis, err := cache.find("", "foo")
	assert.Nil(t, err)
	assertFind(t, cfis)
}

func TestFindInvalid(t *testing.T) {
	t.Parallel()
	cache, err := NewCache(&Config{
		CachePath: "../test-data",
	})
	assert.Nil(t, err)
	cfis, err := cache.find("find_999", "foo")
	assert.IsType(t, &fs.PathError{}, err)
	assert.Equal(t, 0, len(cfis))
}

func TestHas(t *testing.T) {
	t.Parallel()
	cache, err := NewCache(&Config{
		CachePath: "../test-data",
	})
	assert.Nil(t, err)
	has, err := cache.has("find_1", "foo")
	assert.Nil(t, err)
	assert.True(t, has)
}

func TestHasNot(t *testing.T) {
	t.Parallel()
	cache, err := NewCache(&Config{
		CachePath: "../test-data",
	})
	assert.Nil(t, err)
	has, err := cache.has("find_1", "bar")
	assert.Nil(t, err)
	assert.False(t, has)
}

func TestHasError(t *testing.T) {
	t.Parallel()
	cache, err := NewCache(&Config{
		CachePath: "../test-data",
	})
	assert.Nil(t, err)
	has, err := cache.has("find_999", "foo")
	assert.IsType(t, &fs.PathError{}, err)
	assert.False(t, has)
}

//////////////////////////////////////////////////////////////////////////////
// FANTASY GAME
//////////////////////////////////////////////////////////////////////////////

func TestHasFantasyGame(t *testing.T) {
	t.Parallel()
	cache, err := NewCache(&Config{
		CachePath: "../test-data/cache",
	})
	assert.Nil(t, err)
	has, err := cache.HasFantasyGame()
	assert.Nil(t, err)
	assert.True(t, has)
}

func TestHasNotFantasyGame(t *testing.T) {
	t.Parallel()
	cache, err := NewCache(&Config{
		CachePath: "../test-data",
	})
	assert.Nil(t, err)
	has, err := cache.HasFantasyGame()
	assert.Nil(t, err)
	assert.False(t, has)
}

func TestFantasyGameURL(t *testing.T) {
	t.Parallel()
	cache, err := NewCache(&Config{
		CachePath: "../test-data",
	})
	assert.Nil(t, err)
	assert.Equal(t, "https://fantasysports.yahooapis.com/fantasy/v2/game/nhl", cache.FantasyGameURL())
}

func TestFindFantasyGame(t *testing.T) {
	t.Parallel()
	cache, err := NewCache(&Config{
		CachePath: "../test-data/cache",
	})
	assert.Nil(t, err)
	cfis, err := cache.FindFantasyGame()
	assert.Nil(t, err)
	assert.Equal(t, 1, len(cfis))
	assert.Equal(t, "fantasy-game", cfis[0].Name)
	assert.Equal(t, 19, cfis[0].Time.Minute())
	assert.Equal(t, 46, cfis[0].Time.Second())
}

func TestGetFantasyGame(t *testing.T) {
	t.Parallel()
	cache, err := NewCache(&Config{
		CachePath: "../test-data/cache",
	})
	assert.Nil(t, err)
	fg, err := cache.GetFantasyGame()
	assert.Nil(t, err)
	assert.Equal(t, GameConst, fg.ID)
}

//////////////////////////////////////////////////////////////////////////////
// LEAGUE
//////////////////////////////////////////////////////////////////////////////

func TestHasLeague(t *testing.T) {
	t.Parallel()
	cache, err := NewCache(&Config{
		CachePath: "../test-data/cache",
	})
	assert.Nil(t, err)
	has, err := cache.HasLeague()
	assert.Nil(t, err)
	assert.True(t, has)
}

func TestHasNotLeague(t *testing.T) {
	t.Parallel()
	cache, err := NewCache(&Config{
		CachePath: "../test-data",
	})
	assert.Nil(t, err)
	has, err := cache.HasLeague()
	assert.Nil(t, err)
	assert.False(t, has)
}

func TestLeagueURL(t *testing.T) {
	t.Parallel()
	cache, err := NewCache(&Config{
		CachePath: "../test-data",
		LeagueID:  1976,
	})
	assert.Nil(t, err)
	assert.Equal(t, "https://fantasysports.yahooapis.com/fantasy/v2/league/411.l.1976", cache.LeagueURL())
}

func TestFindLeague(t *testing.T) {
	t.Parallel()
	cache, err := NewCache(&Config{
		CachePath: "../test-data/cache",
	})
	assert.Nil(t, err)
	cfis, err := cache.FindLeague()
	assert.Nil(t, err)
	assert.Equal(t, 1, len(cfis))
	assert.Equal(t, "league", cfis[0].Name)
	assert.Equal(t, 43, cfis[0].Time.Minute())
	assert.Equal(t, 47, cfis[0].Time.Second())
}

//////////////////////////////////////////////////////////////////////////////
// ROSTER
//////////////////////////////////////////////////////////////////////////////
func TestGetTeamDir(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "team-07", GetTeamDir(7))
}

//////////////////////////////////////////////////////////////////////////////
// Games List
//////////////////////////////////////////////////////////////////////////////
