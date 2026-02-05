package worker

import (
	"fmt"
	"html"
	"strings"
	"time"
	"unicode"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// YahooIDMatchResult contains the result of a Yahoo ID matching attempt.
type YahooIDMatchResult struct {
	YahooID int
	Matched bool
	Reason  string // "name+jersey", "name-only", "team-tiebreaker", "no-match", "ambiguous"
}

// normalizeName prepares a name for matching by:
// 1. Trimming leading/trailing whitespace
// 2. Decoding HTML entities (&#x27; -> ')
// 3. Removing diacritics/accents (é -> e, ü -> u)
// 4. Converting to lowercase
func normalizeName(name string) string {
	name = strings.TrimSpace(name)

	// Decode HTML entities (e.g., &#x27; -> ')
	name = html.UnescapeString(name)

	// Remove diacritics using unicode normalization
	// NFD decomposes characters (é -> e + combining accent)
	// Then we remove the combining marks
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, _ := transform.String(t, name)

	return strings.ToLower(result)
}

// nicknameAliases maps first names to their common nicknames/variations.
// Used for fuzzy matching when exact name match fails.
// Keys and values should be lowercase.
var nicknameAliases = map[string][]string{
	// Common hockey player nickname variations
	"rejean":    {"reggie"},
	"reggie":    {"rejean"},
	"michael":   {"mike", "mick", "mickey"},
	"mike":      {"michael", "mick", "mickey"},
	"william":   {"will", "bill", "billy", "willy"},
	"bill":      {"william", "billy"},
	"billy":     {"william", "bill"},
	"robert":    {"rob", "bob", "bobby", "robbie"},
	"bob":       {"robert", "bobby"},
	"bobby":     {"robert", "bob"},
	"rob":       {"robert", "robbie"},
	"richard":   {"rick", "ricky", "rich", "dick"},
	"rick":      {"richard", "ricky"},
	"james":     {"jim", "jimmy", "jamie"},
	"jim":       {"james", "jimmy"},
	"jimmy":     {"james", "jim"},
	"joseph":    {"joe", "joey"},
	"joe":       {"joseph", "joey"},
	"thomas":    {"tom", "tommy"},
	"tom":       {"thomas", "tommy"},
	"tommy":     {"thomas", "tom"},
	"anthony":   {"tony"},
	"tony":      {"anthony"},
	"alexander": {"alex", "sasha"},
	"alex":      {"alexander", "sasha"},
	"daniel":    {"dan", "danny"},
	"dan":       {"daniel", "danny"},
	"danny":     {"daniel", "dan"},
	"matthew":   {"matt", "matty"},
	"matt":      {"matthew", "matty"},
	"nicholas":  {"nick", "nicky"},
	"nick":      {"nicholas", "nicky"},
	"christopher": {"chris"},
	"chris":     {"christopher"},
	"jonathan":  {"jon", "jonny", "john"},
	"jon":       {"jonathan", "jonny"},
	"john":      {"jonathan", "johnny", "jon"},
	"johnny":    {"john", "jonathan"},
	"edward":    {"ed", "eddie", "ted", "teddy"},
	"ed":        {"edward", "eddie"},
	"eddie":     {"edward", "ed"},
	"patrick":   {"pat", "patty", "paddy"},
	"pat":       {"patrick", "patty"},
	"timothy":   {"tim", "timmy"},
	"tim":       {"timothy", "timmy"},
	"kenneth":   {"ken", "kenny"},
	"ken":       {"kenneth", "kenny"},
	"stephen":   {"steve", "steven"},
	"steven":    {"steve", "stephen"},
	"steve":     {"stephen", "steven"},
	"david":     {"dave", "davey"},
	"dave":      {"david", "davey"},
	"joshua":    {"josh"},
	"josh":      {"joshua"},
	"andrew":    {"andy", "drew"},
	"andy":      {"andrew"},
	"drew":      {"andrew"},
	"benjamin":  {"ben", "benny"},
	"ben":       {"benjamin", "benny"},
	"samuel":    {"sam", "sammy"},
	"sam":       {"samuel", "sammy"},
	"peter":     {"pete"},
	"pete":      {"peter"},
	"phillip":   {"phil"},
	"phil":      {"phillip"},
	"douglas":   {"doug", "dougie"},
	"doug":      {"douglas", "dougie"},
	"raymond":   {"ray"},
	"ray":       {"raymond"},
	"lawrence":  {"larry"},
	"larry":     {"lawrence"},
	"gerald":    {"gerry", "jerry"},
	"gerry":     {"gerald", "jerry"},
	"jerry":     {"gerald", "gerry"},
	"eugene":    {"gene"},
	"gene":      {"eugene"},
	"francis":   {"frank", "frankie"},
	"frank":     {"francis", "frankie"},
	"frederick": {"fred", "freddy", "freddie"},
	"fred":      {"frederick", "freddy"},
	"charles":   {"charlie", "chuck", "chas"},
	"charlie":   {"charles", "chuck"},
	"chuck":     {"charles", "charlie"},
	"bernard":   {"bernie"},
	"bernie":    {"bernard"},
	"vincent":   {"vinnie", "vince"},
	"vince":     {"vincent", "vinnie"},
	"vinnie":    {"vincent", "vince"},
	"zachary":   {"zach", "zack"},
	"zach":      {"zachary", "zack"},
	"zack":      {"zachary", "zach"},
	"jacob":     {"jake"},
	"jake":      {"jacob"},
	"nathaniel": {"nate", "nathan"},
	"nathan":    {"nate", "nathaniel"},
	"nate":      {"nathan", "nathaniel"},
	"donald":    {"don", "donnie"},
	"don":       {"donald", "donnie"},
	"ronald":    {"ron", "ronnie"},
	"ron":       {"ronald", "ronnie"},
	"roland":    {"rollie"},
	"rollie":    {"roland"},
	"harold":    {"harry", "hal"},
	"harry":     {"harold"},
	"hal":       {"harold"},
	"arthur":    {"art", "artie"},
	"art":       {"arthur", "artie"},
	"leonard":   {"len", "lenny", "leo"},
	"len":       {"leonard", "lenny"},
	"lenny":     {"leonard", "len"},
	"walter":    {"walt", "wally"},
	"walt":      {"walter", "wally"},
	"wally":     {"walter", "walt"},
	"albert":    {"al", "bert"},
	"al":        {"albert"},
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
// 1. Find all name matches (case-insensitive, including nickname aliases)
// 2. If NHL player has a jersey number, filter by jersey
// 3. If both have birth dates, filter by date match (within 1 day)
// 4. If multiple matches, use team as tiebreaker
// 5. Return error if >1 match after all disambiguation
func MatchYahooID(
	landing *nhl.PlayerLanding,
	nhlTeamAbbrev string,
	nhlBirthDate time.Time,
	pool map[int]*cache.YahooPlayer,
) (YahooIDMatchResult, error) {
	// Normalize names: decode HTML entities, strip accents, lowercase
	firstName := normalizeName(landing.FirstName.Default)
	lastName := normalizeName(landing.LastName.Default)

	// Build list of first names to match (original + aliases)
	firstNamesToMatch := []string{firstName}
	if aliases, ok := nicknameAliases[firstName]; ok {
		firstNamesToMatch = append(firstNamesToMatch, aliases...)
	}

	// Step 1: Find all name matches (exact or via nickname)
	type candidateMatch struct {
		player       *cache.YahooPlayer
		fuzzyMatched bool // true if matched via nickname alias or accent normalization
	}
	var candidates []candidateMatch
	for _, yahoo := range pool {
		// Normalize Yahoo names too (they may have HTML entities)
		yahooFirst := normalizeName(yahoo.FirstName)
		yahooLast := normalizeName(yahoo.LastName)

		if yahooLast != lastName {
			continue
		}

		// Check exact match first (after normalization)
		if yahooFirst == firstName {
			// Mark as fuzzy if normalization changed either name
			fuzzy := yahooFirst != strings.ToLower(yahoo.FirstName) ||
				firstName != strings.ToLower(landing.FirstName.Default) ||
				yahooLast != strings.ToLower(yahoo.LastName) ||
				lastName != strings.ToLower(landing.LastName.Default)
			candidates = append(candidates, candidateMatch{player: yahoo, fuzzyMatched: fuzzy})
			continue
		}

		// Check nickname aliases
		for _, fn := range firstNamesToMatch[1:] { // Skip first (exact) name
			if yahooFirst == fn {
				candidates = append(candidates, candidateMatch{player: yahoo, fuzzyMatched: true})
				break
			}
		}
	}

	if len(candidates) == 0 {
		return YahooIDMatchResult{Matched: false, Reason: "no-match"}, nil
	}

	// Helper to log and return a fuzzy match result
	logFuzzyMatch := func(match candidateMatch, reason string) YahooIDMatchResult {
		if match.fuzzyMatched {
			log.Warn().
				Int64("nhl_id", landing.PlayerID.AsInt64()).
				Str("nhl_name", landing.FirstName.Default+" "+landing.LastName.Default).
				Int("yahoo_id", match.player.YahooID).
				Str("yahoo_name", match.player.FirstName+" "+match.player.LastName).
				Str("reason", reason).
				Msg("Fuzzy matched player via nickname alias")
		}
		return YahooIDMatchResult{
			YahooID: match.player.YahooID,
			Matched: true,
			Reason:  reason,
		}
	}

	// Step 2: Filter by jersey number if NHL player has one
	hasJersey := landing.SweaterNumber != nil && *landing.SweaterNumber > 0
	if hasJersey {
		var jerseyMatches []candidateMatch
		for _, c := range candidates {
			if c.player.JerseyNumber == *landing.SweaterNumber {
				jerseyMatches = append(jerseyMatches, c)
			}
		}
		if len(jerseyMatches) == 1 {
			return logFuzzyMatch(jerseyMatches[0], "name+jersey"), nil
		}
		if len(jerseyMatches) > 1 {
			// Multiple jersey matches, continue with these for team tiebreaker
			candidates = jerseyMatches
		}
		// If no jersey matches, continue with all name matches
	}

	// Step 3: Filter by birth date if both NHL and Yahoo players have one
	if !nhlBirthDate.IsZero() && len(candidates) > 1 {
		var birthMatches []candidateMatch
		for _, c := range candidates {
			if !c.player.BirthDate.IsZero() {
				diff := nhlBirthDate.Sub(c.player.BirthDate)
				if diff < 0 {
					diff = -diff
				}
				if diff <= 24*time.Hour {
					birthMatches = append(birthMatches, c)
				}
			}
		}
		if len(birthMatches) == 1 {
			return logFuzzyMatch(birthMatches[0], "name+birthdate"), nil
		}
		if len(birthMatches) > 1 {
			// Multiple birth date matches, continue with these for team tiebreaker
			candidates = birthMatches
		}
		// If no birth date matches, continue with all candidates
	}

	// Step 4: Team tiebreaker if multiple candidates
	if len(candidates) > 1 && nhlTeamAbbrev != "" {
		yahooTeamName := nhlAbbrevToYahooTeam[nhlTeamAbbrev]
		if yahooTeamName != "" {
			var teamMatches []candidateMatch
			for _, c := range candidates {
				if c.player.Team == yahooTeamName {
					teamMatches = append(teamMatches, c)
				}
			}
			if len(teamMatches) == 1 {
				return logFuzzyMatch(teamMatches[0], "team-tiebreaker"), nil
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

	// Step 5: Single candidate = match (name-only)
	if len(candidates) == 1 {
		return logFuzzyMatch(candidates[0], "name-only"), nil
	}

	// Multiple candidates, no single match found
	return YahooIDMatchResult{Matched: false, Reason: "ambiguous"},
		fmt.Errorf("multiple matches for %s %s: %d candidates, no disambiguator",
			landing.FirstName.Default, landing.LastName.Default, len(candidates))
}
