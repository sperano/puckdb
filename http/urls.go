package http

import (
	"fmt"
	"github.com/sperano/puckdb/cache"
	"time"
)

const (
	baseAPIURL    = "https://fantasysports.yahooapis.com/fantasy/v2"
	baseSportsURL = "https://sports.yahoo.com"
)

func YahooFantasyGameURL() string {
	return fmt.Sprintf("%s/game/nhl", baseAPIURL)
}

func YahooLeagueURL(gameKey int, leagueID int) string {
	return fmt.Sprintf("%s/league/%d.l.%d/settings", baseAPIURL, gameKey, leagueID)
}

func YahooTeamURL(gameKey int, leagueID int, teamID int) string {
	//https://fantasysports.yahooapis.com/fantasy/v2/team/***REMOVED***.t.1
	return fmt.Sprintf("%s/team/%d.l.%d.t.%d", baseAPIURL, gameKey, leagueID, teamID)
}

func YahooRosterURL(gameKey int, leagueID int, teamID int, date time.Time) string {
	//https://fantasysports.yahooapis.com/fantasy/v2/team/***REMOVED***.t.1/stats;type=season
	return fmt.Sprintf("%s/team/%d.l.%d.t.%d/roster;date=%d-%02d-%02d/players", baseAPIURL, gameKey, leagueID, teamID, date.Year(), date.Month(), date.Day())
}

func YahooTeamSummaryURL(gameKey int, leagueID int, teamID int, date time.Time) string {
	//https://fantasysports.yahooapis.com/fantasy/v2/team/253.l.1004.t.10/stats;type=date;date=2011-07-06
	return fmt.Sprintf("%s/team/%d.l.%d.t.%d/stats;type=date;date=%d-%02d-%02d", baseAPIURL, gameKey, leagueID, teamID, date.Year(), date.Month(), date.Day())
}

func YahooGamesListURL(date time.Time) string {
	// https://sports.yahoo.com/nhl/scoreboard/?confId=&dateRange=2022-4-9&schedState=2
	return fmt.Sprintf("%s/nhl/scoreboard/?confId=&dateRange=%4d-%02d-%02d&schedState=2", baseSportsURL, date.Year(), date.Month(), date.Day())
}

func YahooGameURL(gameLink cache.GameLink) string {
	return baseSportsURL + string(gameLink)
}
