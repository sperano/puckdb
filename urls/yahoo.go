package urls

import (
	"fmt"
	"time"
)

const (
	BaseAPIURL    = "https://fantasysports.yahooapis.com/fantasy/v2"
	BaseSportsURL = "https://sports.yahoo.com"
)

func YahooFantasyGameURL() string {
	return fmt.Sprintf("%s/game/nhl", BaseAPIURL)
}

func YahooFantasyGameBySeasonURL(season int) string {
	return fmt.Sprintf("%s/games;game_codes=nhl;seasons=%d", BaseAPIURL, season)
}

func YahooLeagueURL(gameKey int, leagueID int) string {
	return fmt.Sprintf("%s/league/%d.l.%d/settings", BaseAPIURL, gameKey, leagueID)
}

func YahooTeamURL(gameKey int, leagueID int, teamID int) string {
	return fmt.Sprintf("%s/team/%d.l.%d.t.%d", BaseAPIURL, gameKey, leagueID, teamID)
}

func YahooRosterURL(gameKey int, leagueID int, teamID int, date time.Time) string {
	return fmt.Sprintf("%s/team/%d.l.%d.t.%d/roster;date=%d-%02d-%02d/players", BaseAPIURL, gameKey, leagueID, teamID, date.Year(), date.Month(), date.Day())
}

func YahooTeamSummaryURL(gameKey int, leagueID int, teamID int, date time.Time) string {
	return fmt.Sprintf("%s/team/%d.l.%d.t.%d/stats;type=date;date=%d-%02d-%02d", BaseAPIURL, gameKey, leagueID, teamID, date.Year(), date.Month(), date.Day())
}

func YahooPlayerURL(playerID int) string {
	return fmt.Sprintf("%s/nhl/players/%d/", BaseSportsURL, playerID)
}
