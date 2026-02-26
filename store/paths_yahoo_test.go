package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLeaguePath(t *testing.T) {
	t.Parallel()
	season := 2024
	leagueID := 12345
	expected := "seasons/2024/yahoo/12345/league/league.xml"
	assert.Equal(t, expected, LeaguePath(season, leagueID))
}

func TestTeamPath(t *testing.T) {
	t.Parallel()
	season := 2024
	leagueID := 12345
	teamID := 1
	expected := "seasons/2024/yahoo/12345/teams/team-01/team-01.xml"
	assert.Equal(t, expected, TeamPath(season, leagueID, teamID))
}

func TestTeamPath_DoubleDigitTeamID(t *testing.T) {
	t.Parallel()
	season := 2024
	leagueID := 12345
	teamID := 12
	expected := "seasons/2024/yahoo/12345/teams/team-12/team-12.xml"
	assert.Equal(t, expected, TeamPath(season, leagueID, teamID))
}

func TestRosterPath(t *testing.T) {
	t.Parallel()
	leagueID := 12345
	teamID := 1
	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	expected := "seasons/2024/yahoo/12345/rosters/team-01/rosters-01-2025-01-15.xml"
	assert.Equal(t, expected, RosterPath(leagueID, teamID, date))
}

func TestTeamSummaryPath(t *testing.T) {
	t.Parallel()
	leagueID := 12345
	teamID := 1
	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	expected := "seasons/2024/yahoo/12345/summaries/team-01/team-01-summary-2025-01-15.xml"
	assert.Equal(t, expected, TeamSummaryPath(leagueID, teamID, date))
}

func TestYahooPlayerPath(t *testing.T) {
	t.Parallel()
	playerID := YahooPlayerID(12345)
	expected := "yahoo-players/player-12345.html"
	assert.Equal(t, expected, YahooPlayerPath(playerID))
}

func TestMissingYahooPlayerPath(t *testing.T) {
	t.Parallel()
	playerID := YahooPlayerID(12345)
	expected := "yahoo-players-missing/player-12345.txt"
	assert.Equal(t, expected, MissingYahooPlayerPath(playerID))
}

func TestGameKeyPath(t *testing.T) {
	t.Parallel()
	season := 2024
	expected := "game-keys/gamekey-2024.xml"
	assert.Equal(t, expected, GameKeyPath(season))
}

func TestYahooPlayersDir(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "yahoo-players", YahooPlayersDir())
}
