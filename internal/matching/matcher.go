// Package matching links an NHL player to the Yahoo ID of the same player
// by name, jersey number, birth date and team, reporting a MatchReason
// (NameJersey, NameBirthdate, TeamTiebreaker, Ambiguous, NoMatch, ...) for
// every outcome. It also holds the static NHL team table and Yahoo's
// team-name/abbreviation variants that both this matcher and the news
// player resolver read.
package matching

import (
	"fmt"
	"html"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/store"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// birthDateTolerance is how far apart NHL and Yahoo birth dates may be and
// still match: the sources disagree by a day around time zones.
const birthDateTolerance = 24 * time.Hour

// MatchReason represents the reason for a Yahoo ID match result.
type MatchReason int

const (
	// MatchReasonNoMatch indicates no matching Yahoo player was found.
	MatchReasonNoMatch MatchReason = iota
	// MatchReasonNameOnly indicates a match based on name alone.
	MatchReasonNameOnly
	// MatchReasonNameJersey indicates a match based on name and jersey number.
	MatchReasonNameJersey
	// MatchReasonNameBirthdate indicates a match based on name and birth date.
	MatchReasonNameBirthdate
	// MatchReasonFullName indicates a match based on full name comparison (for compound names).
	MatchReasonFullName
	// MatchReasonTeamTiebreaker indicates a match resolved by team when multiple candidates existed.
	MatchReasonTeamTiebreaker
	// MatchReasonAmbiguous indicates multiple matches with no disambiguating factor.
	MatchReasonAmbiguous
)

// String returns the string representation of the match reason.
func (r MatchReason) String() string {
	switch r {
	case MatchReasonNoMatch:
		return "no-match"
	case MatchReasonNameOnly:
		return "name-only"
	case MatchReasonNameJersey:
		return "name+jersey"
	case MatchReasonNameBirthdate:
		return "name+birthdate"
	case MatchReasonFullName:
		return "fullname"
	case MatchReasonTeamTiebreaker:
		return "team-tiebreaker"
	case MatchReasonAmbiguous:
		return "ambiguous"
	default:
		return "unknown"
	}
}

// YahooIDMatchResult contains the result of a Yahoo ID matching attempt. When
// Matched is true, ImageURL holds the parsed Yahoo player image URL (the
// `players_l/...png` variant) so callers can persist it without re-parsing the
// HTML or re-querying the pool. Empty when Matched is false or when the source
// page had no image.
type YahooIDMatchResult struct {
	YahooID  store.YahooPlayerID
	Matched  bool
	Reason   MatchReason
	ImageURL string
}

// NormalizeName prepares a name for matching by:
// 1. Trimming leading/trailing whitespace
// 2. Decoding HTML entities (&#x27; -> ')
// 3. Removing diacritics/accents (é -> e, ü -> u)
// 4. Converting to lowercase
//
// Punctuation and inner spaces are kept: MatchYahooID compares two name
// records, and "Sean O'Reilly" stays "sean o'reilly" ("Jean-Gabriel" does
// not match "Jean Gabriel"). Player news, which finds names inside prose,
// splits this result into words (news.Words: "sean o reilly"). Loosening
// the matcher the same way would change which Yahoo IDs players get.
func NormalizeName(name string) string {
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
	"rejean":      {"reggie"},
	"reggie":      {"rejean"},
	"michael":     {"mike", "mick", "mickey"},
	"mike":        {"michael", "mick", "mickey"},
	"william":     {"will", "bill", "billy", "willy"},
	"bill":        {"william", "billy"},
	"billy":       {"william", "bill"},
	"robert":      {"rob", "bob", "bobby", "robbie"},
	"bob":         {"robert", "bobby"},
	"bobby":       {"robert", "bob"},
	"rob":         {"robert", "robbie"},
	"richard":     {"rick", "ricky", "rich", "dick"},
	"rick":        {"richard", "ricky"},
	"james":       {"jim", "jimmy", "jamie"},
	"jim":         {"james", "jimmy"},
	"jimmy":       {"james", "jim"},
	"joseph":      {"joe", "joey"},
	"joe":         {"joseph", "joey"},
	"thomas":      {"tom", "tommy"},
	"tom":         {"thomas", "tommy"},
	"tommy":       {"thomas", "tom"},
	"anthony":     {"tony"},
	"tony":        {"anthony"},
	"alexander":   {"alex", "sasha"},
	"alex":        {"alexander", "sasha"},
	"daniel":      {"dan", "danny"},
	"dan":         {"daniel", "danny"},
	"danny":       {"daniel", "dan"},
	"matthew":     {"matt", "matty"},
	"matt":        {"matthew", "matty"},
	"nicholas":    {"nick", "nicky"},
	"nick":        {"nicholas", "nicky"},
	"christopher": {"chris"},
	"chris":       {"christopher"},
	"jonathan":    {"jon", "jonny", "john"},
	"jon":         {"jonathan", "jonny"},
	"john":        {"jonathan", "johnny", "jon"},
	"johnny":      {"john", "jonathan"},
	"edward":      {"ed", "eddie", "ted", "teddy"},
	"ed":          {"edward", "eddie"},
	"eddie":       {"edward", "ed"},
	"patrick":     {"pat", "patty", "paddy"},
	"pat":         {"patrick", "patty"},
	"timothy":     {"tim", "timmy"},
	"tim":         {"timothy", "timmy"},
	"kenneth":     {"ken", "kenny"},
	"ken":         {"kenneth", "kenny"},
	"stephen":     {"steve", "steven"},
	"steven":      {"steve", "stephen"},
	"steve":       {"stephen", "steven"},
	"david":       {"dave", "davey"},
	"dave":        {"david", "davey"},
	"joshua":      {"josh"},
	"josh":        {"joshua"},
	"andrew":      {"andy", "drew"},
	"andy":        {"andrew"},
	"drew":        {"andrew"},
	"benjamin":    {"ben", "benny"},
	"ben":         {"benjamin", "benny"},
	"samuel":      {"sam", "sammy"},
	"sam":         {"samuel", "sammy"},
	"peter":       {"pete"},
	"pete":        {"peter"},
	"phillip":     {"phil"},
	"phil":        {"phillip"},
	"douglas":     {"doug", "dougie"},
	"doug":        {"douglas", "dougie"},
	"raymond":     {"ray"},
	"ray":         {"raymond"},
	"lawrence":    {"larry"},
	"larry":       {"lawrence"},
	"gerald":      {"gerry", "jerry"},
	"gerry":       {"gerald", "jerry"},
	"jerry":       {"gerald", "gerry"},
	"eugene":      {"gene"},
	"gene":        {"eugene"},
	"francis":     {"frank", "frankie"},
	"frank":       {"francis", "frankie"},
	"frederick":   {"fred", "freddy", "freddie"},
	"fred":        {"frederick", "freddy"},
	"charles":     {"charlie", "chuck", "chas"},
	"charlie":     {"charles", "chuck"},
	"chuck":       {"charles", "charlie"},
	"bernard":     {"bernie"},
	"bernie":      {"bernard"},
	"vincent":     {"vinnie", "vince"},
	"vince":       {"vincent", "vinnie"},
	"vinnie":      {"vincent", "vince"},
	"zachary":     {"zach", "zack"},
	"zach":        {"zachary", "zack"},
	"zack":        {"zachary", "zach"},
	"jacob":       {"jake"},
	"jake":        {"jacob"},
	"nathaniel":   {"nate", "nathan"},
	"nathan":      {"nate", "nathaniel"},
	"nate":        {"nathan", "nathaniel"},
	"donald":      {"don", "donnie"},
	"don":         {"donald", "donnie"},
	"ronald":      {"ron", "ronnie"},
	"ron":         {"ronald", "ronnie"},
	"roland":      {"rollie"},
	"rollie":      {"roland"},
	"harold":      {"harry", "hal"},
	"harry":       {"harold"},
	"hal":         {"harold"},
	"arthur":      {"art", "artie"},
	"art":         {"arthur", "artie"},
	"leonard":     {"len", "lenny", "leo"},
	"len":         {"leonard", "lenny"},
	"lenny":       {"leonard", "len"},
	"walter":      {"walt", "wally"},
	"walt":        {"walter", "wally"},
	"wally":       {"walter", "walt"},
	"albert":      {"al", "bert"},
	"al":          {"albert"},
}

// MatchYahooID attempts to find a matching Yahoo player ID for an NHL player.
// The matching algorithm:
//  1. Find all name matches (case-insensitive, including nickname aliases and
//     full name comparison): candidatesByName
//  2. If the NHL player has a jersey number, filter by jersey: filterByJersey
//  3. If several remain and the NHL player has a birth date, filter by date
//     (within birthDateTolerance): filterByBirthDate
//  4. If several remain, use the current team as tiebreaker: filterByTeam
//  5. Return an error if more than one candidate is left
//
// A step that singles out one candidate decides the match and its reason. A
// step that keeps several narrows the candidates for the next one; a step
// that keeps none is ignored, so a disambiguator nobody shares never rules
// out every name match.
func MatchYahooID(
	landing *nhl.PlayerLanding,
	nhlTeamAbbrev string,
	nhlBirthDate time.Time,
	pool map[store.YahooPlayerID]*store.YahooPlayer,
) (YahooIDMatchResult, error) {
	candidates := candidatesByName(landing, pool)
	if len(candidates) == 0 {
		return YahooIDMatchResult{Matched: false, Reason: MatchReasonNoMatch}, nil
	}

	kept := filterByJersey(candidates, landing.SweaterNumber)
	if len(kept) == 1 {
		return matchedResult(landing, kept[0], MatchReasonNameJersey), nil
	}
	candidates = narrow(candidates, kept)

	if len(candidates) > 1 {
		kept = filterByBirthDate(candidates, nhlBirthDate)
		if len(kept) == 1 {
			return matchedResult(landing, kept[0], MatchReasonNameBirthdate), nil
		}
		candidates = narrow(candidates, kept)
	}

	if len(candidates) > 1 {
		kept = filterByTeam(candidates, nhlTeamAbbrev)
		if len(kept) == 1 {
			return matchedResult(landing, kept[0], MatchReasonTeamTiebreaker), nil
		}
		if len(kept) > 1 {
			return YahooIDMatchResult{Matched: false, Reason: MatchReasonAmbiguous},
				fmt.Errorf("multiple matches for %s %s with team %s: %d candidates",
					landing.FirstName.Default, landing.LastName.Default, nhlTeamAbbrev, len(kept))
		}
	}

	if len(candidates) == 1 {
		return matchedResult(landing, candidates[0], MatchReasonNameOnly), nil
	}
	return YahooIDMatchResult{Matched: false, Reason: MatchReasonAmbiguous},
		fmt.Errorf("multiple matches for %s %s: %d candidates, no disambiguator",
			landing.FirstName.Default, landing.LastName.Default, len(candidates))
}

// candidateMatch is a Yahoo player whose name matches the NHL player's.
type candidateMatch struct {
	player        *store.YahooPlayer
	fuzzyMatched  bool // true if matched via nickname alias, accent normalization, or full-name match
	fullNameMatch bool // true if matched via full name comparison (handles compound names)
}

// candidatesByName is step 1: the Yahoo players whose normalized full name
// equals the NHL player's, or whose first name is a nickname alias of the
// NHL first name with the same last name.
func candidatesByName(landing *nhl.PlayerLanding, pool map[store.YahooPlayerID]*store.YahooPlayer) []candidateMatch {
	nhlName := newPlayerName(landing.FirstName.Default, landing.LastName.Default)
	aliases := nicknameAliases[nhlName.first]
	var candidates []candidateMatch
	for _, yahoo := range pool {
		yahooName := newPlayerName(yahoo.FirstName, yahoo.LastName)

		// Check full name match first (handles compound first names like "Charles Alexis Legault")
		// NHL might have FirstName="Charles Alexis", LastName="Legault"
		// Yahoo might have FirstName="Charles", LastName="Alexis Legault"
		// But both produce the same full name: "charles alexis legault"
		if yahooName.full() == nhlName.full() {
			splitDiffers := yahooName.first != nhlName.first || yahooName.last != nhlName.last
			candidates = append(candidates, candidateMatch{
				player:        yahoo,
				fuzzyMatched:  splitDiffers || yahooName.normalized || nhlName.normalized,
				fullNameMatch: splitDiffers,
			})
			continue
		}

		// A nickname alias needs the same last name
		if yahooName.last == nhlName.last && slices.Contains(aliases, yahooName.first) {
			candidates = append(candidates, candidateMatch{player: yahoo, fuzzyMatched: true})
		}
	}
	return candidates
}

// playerName is a first and last name as NormalizeName returns them.
type playerName struct {
	first, last string
	// normalized is true when NormalizeName changed more than the case
	// (accent stripping, whitespace, HTML entities).
	normalized bool
}

// newPlayerName normalizes a first and last name.
func newPlayerName(first, last string) playerName {
	n := playerName{first: NormalizeName(first), last: NormalizeName(last)}
	n.normalized = n.first != strings.ToLower(first) || n.last != strings.ToLower(last)
	return n
}

func (n playerName) full() string { return n.first + " " + n.last }

// filterByJersey is step 2: the candidates wearing the NHL player's number,
// none when the NHL player has no number.
func filterByJersey(candidates []candidateMatch, sweater *int) []candidateMatch {
	if sweater == nil || *sweater <= 0 {
		return nil
	}
	return filterCandidates(candidates, func(p *store.YahooPlayer) bool {
		return p.JerseyNumber == *sweater
	})
}

// filterByBirthDate is step 3: the candidates born within
// birthDateTolerance of the NHL player, none when either date is unknown.
func filterByBirthDate(candidates []candidateMatch, nhlBirthDate time.Time) []candidateMatch {
	if nhlBirthDate.IsZero() {
		return nil
	}
	return filterCandidates(candidates, func(p *store.YahooPlayer) bool {
		if p.BirthDate.IsZero() {
			return false
		}
		diff := nhlBirthDate.Sub(p.BirthDate)
		return diff.Abs() <= birthDateTolerance
	})
}

// filterByTeam is step 4: the candidates whose Yahoo team is the NHL
// player's current team, none when the team is unknown or has no Yahoo name.
func filterByTeam(candidates []candidateMatch, nhlTeamAbbrev string) []candidateMatch {
	yahooTeam := yahooTeamName(nhlTeamAbbrev)
	if yahooTeam == "" {
		return nil
	}
	return filterCandidates(candidates, func(p *store.YahooPlayer) bool {
		return p.Team == yahooTeam
	})
}

// filterCandidates returns the candidates whose Yahoo player keep accepts.
func filterCandidates(candidates []candidateMatch, keep func(*store.YahooPlayer) bool) []candidateMatch {
	var kept []candidateMatch
	for _, c := range candidates {
		if keep(c.player) {
			kept = append(kept, c)
		}
	}
	return kept
}

// narrow returns the candidates a step kept, or all of them when it kept
// none.
func narrow(candidates, kept []candidateMatch) []candidateMatch {
	if len(kept) == 0 {
		return candidates
	}
	return kept
}

// matchedResult returns the match of c, logging fuzzy matches. A full-name
// match whose first/last split differs reports MatchReasonFullName instead
// of a name-based reason.
func matchedResult(landing *nhl.PlayerLanding, c candidateMatch, reason MatchReason) YahooIDMatchResult {
	if c.fullNameMatch && (reason == MatchReasonNameOnly || reason == MatchReasonNameJersey || reason == MatchReasonNameBirthdate) {
		reason = MatchReasonFullName
	}
	if c.fuzzyMatched {
		log.Warn().
			Int64("nhl_id", landing.PlayerID.Int64()).
			Str("nhl_name", landing.FirstName.Default+" "+landing.LastName.Default).
			Int("yahoo_id", int(c.player.YahooID)).
			Str("yahoo_name", c.player.FirstName+" "+c.player.LastName).
			Str("reason", reason.String()).
			Bool("fullname_match", c.fullNameMatch).
			Msg("Fuzzy matched player")
	}
	return YahooIDMatchResult{
		YahooID:  c.player.YahooID,
		Matched:  true,
		Reason:   reason,
		ImageURL: c.player.ImageURL,
	}
}
