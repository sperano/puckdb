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
// Punctuation and inner spaces are kept: "Sean O'Reilly" stays
// "sean o'reilly" and "Jean-Gabriel" stays "jean-gabriel". MatchYahooID
// compares two name records with this exact form first, and reads hyphens,
// dots and spaces as the same separator only when no name matches exactly
// or through a nickname alias (candidatesByName); apostrophes always count.
// Player news, which finds names inside prose, splits this result into
// words (news.Words: "sean o reilly").
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

// MatchYahooID attempts to find a matching Yahoo player ID for an NHL player.
// The matching algorithm:
//  1. Find all name matches (case-insensitive, including nickname aliases and
//     full name comparison; when none, the same with hyphens, dots and
//     spaces read alike, and initials for a first name): candidatesByName
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

// BirthDatesNeeded returns, sorted, the Yahoo IDs of the candidates
// MatchYahooID would compare by birth date (step 3) that have none: the name
// matches the jersey step left tied, when the NHL player's birth date is
// known. The pool's game player lists carry no birth date, so a caller
// fetches these players' dates, sets them on the pool entries and then calls
// MatchYahooID. Nil when the birth date step would not run.
func BirthDatesNeeded(
	landing *nhl.PlayerLanding,
	nhlBirthDate time.Time,
	pool map[store.YahooPlayerID]*store.YahooPlayer,
) []store.YahooPlayerID {
	if nhlBirthDate.IsZero() {
		return nil
	}
	candidates := candidatesByName(landing, pool)
	kept := filterByJersey(candidates, landing.SweaterNumber)
	if len(kept) == 1 {
		return nil
	}
	candidates = narrow(candidates, kept)
	if len(candidates) < 2 {
		return nil
	}
	var ids []store.YahooPlayerID
	for _, c := range candidates {
		if c.player.BirthDate.IsZero() {
			ids = append(ids, c.player.YahooID)
		}
	}
	slices.Sort(ids)
	return ids
}

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
