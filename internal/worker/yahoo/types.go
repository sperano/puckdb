package yahoo

import (
	"time"

	"github.com/sperano/puckdb/internal/core"
)

// TeamInfo identifies a team within a league for Yahoo downloads.
type TeamInfo struct {
	LeagueID int
	TeamID   int
}

// FetchTeamsInput contains parameters for fetching multiple teams in a single activity.
type FetchTeamsInput struct {
	StartSeason int // Season start year (e.g., 2023 for 2023-2024 season)
	Teams       []TeamInfo
}

// FetchYahooLeagueDataInput contains parameters for fetching league-level Yahoo data.
type FetchYahooLeagueDataInput struct {
	Season   int
	LeagueID int
}

// FetchYahooLeagueDataResult reports optional preseason resources that Yahoo
// has not published yet. Required league and team resources still fail.
type FetchYahooLeagueDataResult struct {
	UnavailableResources []string `json:"unavailableResources,omitempty"`
}

// ImportYahooLeagueInput contains parameters for importing a Yahoo league.
type ImportYahooLeagueInput struct {
	Season   int
	LeagueID int
}

// ImportYahooLeagueResult contains the results of importing a Yahoo league.
type ImportYahooLeagueResult struct {
	RosterPositions int
	StatCategories  int
}

// ImportYahooTeamsInput contains parameters for importing Yahoo teams.
type ImportYahooTeamsInput struct {
	Season int
	Teams  []TeamInfo
}

// ImportYahooTeamsResult contains the results of importing Yahoo teams.
type ImportYahooTeamsResult struct {
	TeamsImported    int
	ManagersImported int
}

// ImportYahooDataForDateInput contains parameters for importing Yahoo team data for a single date.
type ImportYahooDataForDateInput struct {
	Season int
	Teams  []TeamInfo
	Date   time.Time
}

// ImportYahooDataForDateResult contains the results of importing Yahoo team data for a date.
type ImportYahooDataForDateResult struct {
	SummariesImported int
	RostersImported   int
	Origins           core.OriginCounts
}

// ImportYahooLeagueDataInput contains parameters for importing league-level Yahoo data.
type ImportYahooLeagueDataInput struct {
	Season   int
	LeagueID int
}

// ImportYahooLeagueDataResult contains the results of importing league-level Yahoo data.
type ImportYahooLeagueDataResult struct {
	TransactionsImported int      `json:"transactionsImported"`
	DraftPicksImported   int      `json:"draftPicksImported"`
	MatchupsImported     int      `json:"matchupsImported"`
	UnavailableResources []string `json:"unavailableResources,omitempty"`
}
