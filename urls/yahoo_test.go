package urls

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestYahooFantasyGameURL(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "https://fantasysports.yahooapis.com/fantasy/v2/game/nhl", YahooFantasyGameURL())
}

func TestYahooLeagueURL(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "https://fantasysports.yahooapis.com/fantasy/v2/league/411.l.123/settings", YahooLeagueURL(411, 123))
}

func TestYahooTeamURL(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "https://fantasysports.yahooapis.com/fantasy/v2/team/***REMOVED***.t.1", YahooTeamURL(223, 431, 1))
}

func TestYahooFantasyGameBySeasonURL(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "https://fantasysports.yahooapis.com/fantasy/v2/games;game_codes=nhl;seasons=2023", YahooFantasyGameBySeasonURL(2023))
	assert.Equal(t, "https://fantasysports.yahooapis.com/fantasy/v2/games;game_codes=nhl;seasons=2020", YahooFantasyGameBySeasonURL(2020))
}

func TestYahooRosterURL(t *testing.T) {
	t.Parallel()
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	assert.Equal(t,
		"https://fantasysports.yahooapis.com/fantasy/v2/team/423.l.12345.t.3/roster;date=2024-01-15/players",
		YahooRosterURL(423, 12345, 3, date))
}

func TestYahooTeamSummaryURL(t *testing.T) {
	t.Parallel()
	date := time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC)
	assert.Equal(t,
		"https://fantasysports.yahooapis.com/fantasy/v2/team/253.l.1004.t.10/stats;type=date;date=2024-03-05",
		YahooTeamSummaryURL(253, 102614, 10, date))
}

func TestYahooPlayerURL(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "https://sports.yahoo.com/nhl/players/12345/", YahooPlayerURL(12345))
	assert.Equal(t, "https://sports.yahoo.com/nhl/players/1/", YahooPlayerURL(1))
}
