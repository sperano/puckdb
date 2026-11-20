package matching

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/store"
)

// minInitials is the fewest initials a first name such as "P.O" must have
// to match a first name of as many words ("Pierre-Olivier"): a single
// initial ("J. Smith") is too weak to stand for a first name.
const minInitials = 2

// candidateMatch is a Yahoo player whose name matches the NHL player's.
type candidateMatch struct {
	player        *store.YahooPlayer
	fuzzyMatched  bool // true if matched via nickname alias, accent normalization, loose punctuation, initials or a full-name match with a different first/last split
	fullNameMatch bool // true if matched via full name comparison (handles compound names)
}

// candidatesByName is step 1: the Yahoo players whose normalized full name
// equals the NHL player's, or whose first name is a nickname alias of the
// NHL first name with the same last name. Only when no Yahoo player matches
// that way does the loose comparison (looseCandidates) run, so a player an
// exact name finds keeps exactly the candidates it had.
func candidatesByName(landing *nhl.PlayerLanding, pool map[store.YahooPlayerID]*store.YahooPlayer) []candidateMatch {
	nhlName := newPlayerName(landing.FirstName.Default, landing.LastName.Default)
	if candidates := exactCandidates(nhlName, pool); len(candidates) > 0 {
		return candidates
	}
	return looseCandidates(nhlName, pool)
}

// exactCandidates returns the Yahoo players whose normalized full name
// equals nhlName's, or whose first name is a nickname alias of its first
// name with the same last name.
func exactCandidates(nhlName playerName, pool map[store.YahooPlayerID]*store.YahooPlayer) []candidateMatch {
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

// looseCandidates returns the Yahoo players whose name matches nhlName's
// with hyphens, dots and spaces read as the same separator ("Antti Jussi"
// = "Antti-Jussi"): the same full name, or the same last name with a
// nickname alias ("Max" = "Maxim") or initials ("P.O" = "Pierre-Olivier")
// for a first name. Every such match is fuzzy.
func looseCandidates(nhlName playerName, pool map[store.YahooPlayerID]*store.YahooPlayer) []candidateMatch {
	nhlLoose := newLooseName(nhlName)
	var candidates []candidateMatch
	for _, yahoo := range pool {
		yahooLoose := newLooseName(newPlayerName(yahoo.FirstName, yahoo.LastName))
		if slices.Equal(yahooLoose.full(), nhlLoose.full()) {
			candidates = append(candidates, candidateMatch{
				player:        yahoo,
				fuzzyMatched:  true,
				fullNameMatch: !slices.Equal(yahooLoose.first, nhlLoose.first),
			})
			continue
		}
		if slices.Equal(yahooLoose.last, nhlLoose.last) && looseFirstNamesMatch(nhlLoose.first, yahooLoose.first) {
			candidates = append(candidates, candidateMatch{player: yahoo, fuzzyMatched: true})
		}
	}
	return candidates
}

// looseFirstNamesMatch reports whether two first names, as loose words,
// are nickname aliases of each other or one is the other's initials.
func looseFirstNamesMatch(nhlFirst, yahooFirst []string) bool {
	if slices.Contains(nicknameAliases[strings.Join(nhlFirst, " ")], strings.Join(yahooFirst, " ")) {
		return true
	}
	return areInitials(nhlFirst, yahooFirst) || areInitials(yahooFirst, nhlFirst)
}

// areInitials reports whether initials holds at least minInitials
// one-letter words, one per word of words, each the first letter of that
// word, and every word of words is longer than its initial: "p o" for
// "pierre olivier", not for "pierre" or "pierre o".
func areInitials(initials, words []string) bool {
	if len(initials) < minInitials || len(initials) != len(words) {
		return false
	}
	for i, initial := range initials {
		letter, size := utf8.DecodeRuneInString(initial)
		if size != len(initial) || !unicode.IsLetter(letter) {
			return false
		}
		first, firstSize := utf8.DecodeRuneInString(words[i])
		if first != letter || firstSize == len(words[i]) {
			return false
		}
	}
	return true
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

// looseName is a playerName split into words at hyphens, dots and
// whitespace, so "P.O" is ["p" "o"] and "Gustafsson-Nyberg" is
// ["gustafsson" "nyberg"]. NormalizeName itself keeps that punctuation
// (see its doc comment).
type looseName struct {
	first, last []string
}

func newLooseName(n playerName) looseName {
	return looseName{first: looseWords(n.first), last: looseWords(n.last)}
}

// full returns the first name's words followed by the last name's.
func (n looseName) full() []string { return slices.Concat(n.first, n.last) }

// looseWords splits s at hyphens, dots and whitespace, dropping empty
// words.
func looseWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '.' || unicode.IsSpace(r)
	})
}
