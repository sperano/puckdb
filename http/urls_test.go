package http

import (
	"github.com/stretchr/testify/assert"
	"testing"
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
