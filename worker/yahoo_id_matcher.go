package worker

import (
	"fmt"
	"strings"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
)

// YahooIDMatchResult contains the result of a Yahoo ID matching attempt.
type YahooIDMatchResult struct {
	YahooID int
	Matched bool
	Reason  string // "name+jersey", "name-only", "team-tiebreaker", "no-match", "ambiguous"
}

// nhlAbbrevToYahooTeam maps NHL team abbreviations to Yahoo team display names
// as they appear in Yahoo Sports HTML page titles.
// Based on YahooHomeLink URLs in database/client.go (e.g., /teams/ny-rangers/ -> "NY Rangers")
var nhlAbbrevToYahooTeam = map[string]string{
	// Atlantic Division
	"BOS": "Boston",
	"BUF": "Buffalo",
	"DET": "Detroit",
	"FLA": "Florida",
	"MTL": "Montreal",
	"OTT": "Ottawa",
	"TBL": "Tampa Bay",
	"TOR": "Toronto",

	// Metropolitan Division
	"CAR": "Carolina",
	"CBJ": "Columbus",
	"NJD": "New Jersey",
	"NYI": "NY Islanders",
	"NYR": "NY Rangers",
	"PHI": "Philadelphia",
	"PIT": "Pittsburgh",
	"WSH": "Washington",

	// Central Division
	"ARI": "Arizona",
	"CHI": "Chicago",
	"COL": "Colorado",
	"DAL": "Dallas",
	"MIN": "Minnesota",
	"NSH": "Nashville",
	"STL": "St. Louis",
	"UTA": "Utah",
	"WPG": "Winnipeg",

	// Pacific Division
	"ANA": "Anaheim",
	"CGY": "Calgary",
	"EDM": "Edmonton",
	"LAK": "Los Angeles",
	"SEA": "Seattle",
	"SJS": "San Jose",
	"VAN": "Vancouver",
	"VGK": "Vegas",
}

// MatchYahooID attempts to find a matching Yahoo player ID for an NHL player.
// The matching algorithm:
// 1. Find all name matches (case-insensitive)
// 2. If NHL player has a jersey number, filter by jersey
// 3. If multiple matches, use team as tiebreaker
// 4. Return error if >1 match after all disambiguation
func MatchYahooID(
	landing *nhl.PlayerLanding,
	nhlTeamAbbrev string,
	pool map[int]*cache.YahooPlayer,
) (YahooIDMatchResult, error) {
	firstName := strings.ToLower(landing.FirstName.Default)
	lastName := strings.ToLower(landing.LastName.Default)

	// Step 1: Find all name matches
	var candidates []*cache.YahooPlayer
	for _, yahoo := range pool {
		if strings.ToLower(yahoo.FirstName) == firstName &&
			strings.ToLower(yahoo.LastName) == lastName {
			candidates = append(candidates, yahoo)
		}
	}

	if len(candidates) == 0 {
		return YahooIDMatchResult{Matched: false, Reason: "no-match"}, nil
	}

	// Step 2: Filter by jersey number if NHL player has one
	hasJersey := landing.SweaterNumber != nil && *landing.SweaterNumber > 0
	if hasJersey {
		var jerseyMatches []*cache.YahooPlayer
		for _, c := range candidates {
			if c.JerseyNumber == *landing.SweaterNumber {
				jerseyMatches = append(jerseyMatches, c)
			}
		}
		if len(jerseyMatches) == 1 {
			return YahooIDMatchResult{
				YahooID: jerseyMatches[0].YahooID,
				Matched: true,
				Reason:  "name+jersey",
			}, nil
		}
		if len(jerseyMatches) > 1 {
			// Multiple jersey matches, continue with these for team tiebreaker
			candidates = jerseyMatches
		}
		// If no jersey matches, continue with all name matches
	}

	// Step 3: Team tiebreaker if multiple candidates
	if len(candidates) > 1 && nhlTeamAbbrev != "" {
		yahooTeamName := nhlAbbrevToYahooTeam[nhlTeamAbbrev]
		if yahooTeamName != "" {
			var teamMatches []*cache.YahooPlayer
			for _, c := range candidates {
				if c.Team == yahooTeamName {
					teamMatches = append(teamMatches, c)
				}
			}
			if len(teamMatches) == 1 {
				return YahooIDMatchResult{
					YahooID: teamMatches[0].YahooID,
					Matched: true,
					Reason:  "team-tiebreaker",
				}, nil
			}
			if len(teamMatches) > 1 {
				// Still ambiguous after team filter
				return YahooIDMatchResult{Matched: false, Reason: "ambiguous"},
					fmt.Errorf("multiple matches for %s %s with team %s: %d candidates",
						landing.FirstName.Default, landing.LastName.Default, nhlTeamAbbrev, len(teamMatches))
			}
			// No team matches, fall through to use all candidates
		}
	}

	// Step 4: Single candidate = match (name-only)
	if len(candidates) == 1 {
		reason := "name-only"
		if hasJersey {
			// We had a jersey number but didn't find a jersey match,
			// so we're falling back to name-only
			reason = "name-only"
		}
		return YahooIDMatchResult{
			YahooID: candidates[0].YahooID,
			Matched: true,
			Reason:  reason,
		}, nil
	}

	// Multiple candidates, no single match found
	return YahooIDMatchResult{Matched: false, Reason: "ambiguous"},
		fmt.Errorf("multiple matches for %s %s: %d candidates, no disambiguator",
			landing.FirstName.Default, landing.LastName.Default, len(candidates))
}
