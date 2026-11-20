package matching

import (
	"testing"

	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
)

// looseNameCases cover step 1's fallback: names that match only with
// hyphens, dots and spaces read alike, nickname aliases or initials. The
// first six are NHL players whose Yahoo entry went unmatched (NHL and
// Yahoo IDs from production).
var looseNameCases = []matchCase{
	{name: "Max = Maxim", first: "Max", last: "Shabanov", sweater: 88, team: "NYI",
		pool:   []store.YahooPlayer{{YahooID: 33674, FirstName: "Maxim", LastName: "Shabanov", JerseyNumber: 88, Team: "NY Islanders"}},
		wantID: 33674, wantReason: MatchReasonNameJersey},
	{name: "Max = Maxence", first: "Max", last: "Guenette", sweater: 43,
		pool:   []store.YahooPlayer{{YahooID: 8464, FirstName: "Maxence", LastName: "Guenette", JerseyNumber: 43}},
		wantID: 8464, wantReason: MatchReasonNameJersey},
	{name: "P.O = Pierre-Olivier", first: "P.O", last: "Joseph", sweater: 73,
		pool:   []store.YahooPlayer{{YahooID: 7538, FirstName: "Pierre-Olivier", LastName: "Joseph", JerseyNumber: 73}},
		wantID: 7538, wantReason: MatchReasonNameJersey},
	{name: "Calvin = Cal", first: "Calvin", last: "Heeter", sweater: 31,
		pool:   []store.YahooPlayer{{YahooID: 5649, FirstName: "Cal", LastName: "Heeter", JerseyNumber: 31}},
		wantID: 5649, wantReason: MatchReasonNameJersey},
	{name: "space = hyphen in a first name", first: "Antti Jussi", last: "Niemi", sweater: 31,
		pool:   []store.YahooPlayer{{YahooID: 2413, FirstName: "Antti-Jussi", LastName: "Niemi", JerseyNumber: 31}},
		wantID: 2413, wantReason: MatchReasonNameJersey},
	{name: "hyphen = space in a last name", first: "Viking", last: "Gustafsson-Nyberg", sweater: 52,
		pool:   []store.YahooPlayer{{YahooID: 33807, FirstName: "Viking", LastName: "Gustafsson Nyberg", JerseyNumber: 52}},
		wantID: 33807, wantReason: MatchReasonNameJersey},

	{name: "Maxime = Max", first: "Maxime", last: "Comtois",
		pool:   []store.YahooPlayer{{YahooID: 7000, FirstName: "Max", LastName: "Comtois"}},
		wantID: 7000, wantReason: MatchReasonNameOnly},
	{name: "initials on the Yahoo side", first: "Pierre-Olivier", last: "Joseph",
		pool:   []store.YahooPlayer{{YahooID: 7538, FirstName: "P.O.", LastName: "Joseph"}},
		wantID: 7538, wantReason: MatchReasonNameOnly},
	{name: "alias with a hyphenated last name", first: "Max", last: "Gustafsson-Nyberg",
		pool:   []store.YahooPlayer{{YahooID: 7001, FirstName: "Maxim", LastName: "Gustafsson Nyberg"}},
		wantID: 7001, wantReason: MatchReasonNameOnly},
	{name: "hyphenated first name split differently", first: "Charles-Alexis", last: "Legault",
		pool:   []store.YahooPlayer{{YahooID: 30710, FirstName: "Charles", LastName: "Alexis Legault"}},
		wantID: 30710, wantReason: MatchReasonFullName},
	{name: "an exact name keeps a hyphen variant out", first: "Jean-Gabriel", last: "Pageau",
		pool: []store.YahooPlayer{
			{YahooID: 4001, FirstName: "Jean Gabriel", LastName: "Pageau"},
			{YahooID: 4002, FirstName: "Jean-Gabriel", LastName: "Pageau"},
		},
		wantID: 4002, wantReason: MatchReasonNameOnly},
	{name: "an alias keeps a loose match out", first: "Max", last: "Shabanov",
		pool: []store.YahooPlayer{
			{YahooID: 33674, FirstName: "Maxim", LastName: "Shabanov"},
			{YahooID: 7002, FirstName: "Max.", LastName: "Shabanov"},
		},
		wantID: 33674, wantReason: MatchReasonNameOnly},
	{name: "jersey singles out initials namesakes", first: "P.O", last: "Joseph", sweater: 73,
		pool: []store.YahooPlayer{
			{YahooID: 7538, FirstName: "Pierre-Olivier", LastName: "Joseph", JerseyNumber: 73},
			{YahooID: 7003, FirstName: "Paul-Olivier", LastName: "Joseph", JerseyNumber: 44},
		},
		wantID: 7538, wantReason: MatchReasonNameJersey},

	// Namesakes whose jersey numbers differ from the NHL player's stay
	// ambiguous: the loose match never guesses between them.
	{name: "initials namesakes, other jerseys", first: "P.O", last: "Joseph", sweater: 77,
		pool: []store.YahooPlayer{
			{YahooID: 7538, FirstName: "Pierre-Olivier", LastName: "Joseph", JerseyNumber: 73},
			{YahooID: 7003, FirstName: "Paul-Olivier", LastName: "Joseph", JerseyNumber: 44},
		},
		wantReason: MatchReasonAmbiguous},
	{name: "alias namesakes, other jerseys", first: "Max", last: "Guenette", sweater: 43,
		pool: []store.YahooPlayer{
			{YahooID: 8464, FirstName: "Maxence", LastName: "Guenette", JerseyNumber: 18},
			{YahooID: 7004, FirstName: "Maxime", LastName: "Guenette", JerseyNumber: 21},
		},
		wantReason: MatchReasonAmbiguous},
	{name: "hyphen namesakes, other jerseys", first: "Viking", last: "Gustafsson-Nyberg", sweater: 52,
		pool: []store.YahooPlayer{
			{YahooID: 33807, FirstName: "Viking", LastName: "Gustafsson Nyberg", JerseyNumber: 12},
			{YahooID: 7005, FirstName: "Viking", LastName: "Gustafsson.Nyberg", JerseyNumber: 9},
		},
		wantReason: MatchReasonAmbiguous},

	{name: "one initial is not a first name", first: "J.", last: "Smith",
		pool:       []store.YahooPlayer{{YahooID: 7006, FirstName: "John", LastName: "Smith"}},
		wantReason: MatchReasonNoMatch},
	{name: "initials need one word each", first: "P.O", last: "Joseph",
		pool:       []store.YahooPlayer{{YahooID: 7007, FirstName: "Pierre", LastName: "Joseph"}},
		wantReason: MatchReasonNoMatch},
	{name: "initials need the same letters", first: "P.O", last: "Joseph",
		pool:       []store.YahooPlayer{{YahooID: 7008, FirstName: "Pierre-Alexandre", LastName: "Joseph"}},
		wantReason: MatchReasonNoMatch},
	{name: "loose alias needs the same last name", first: "Max", last: "Guenette",
		pool:       []store.YahooPlayer{{YahooID: 7009, FirstName: "Maxence", LastName: "Guenett"}},
		wantReason: MatchReasonNoMatch},
}

// TestCandidatesByNameMarksLooseMatchesFuzzy checks that only an exact
// name is a non-fuzzy candidate: every loose match is logged as fuzzy.
func TestCandidatesByNameMarksLooseMatchesFuzzy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		first, last string
		yahoo       store.YahooPlayer
		wantFuzzy   bool
	}{
		{"exact", "Viking", "Gustafsson-Nyberg", store.YahooPlayer{YahooID: 1, FirstName: "Viking", LastName: "Gustafsson-Nyberg"}, false},
		{"hyphen as space", "Viking", "Gustafsson-Nyberg", store.YahooPlayer{YahooID: 1, FirstName: "Viking", LastName: "Gustafsson Nyberg"}, true},
		{"initials", "P.O", "Joseph", store.YahooPlayer{YahooID: 1, FirstName: "Pierre-Olivier", LastName: "Joseph"}, true},
		{"alias", "Calvin", "Heeter", store.YahooPlayer{YahooID: 1, FirstName: "Cal", LastName: "Heeter"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := matchCase{first: tc.first, last: tc.last, pool: []store.YahooPlayer{tc.yahoo}}
			candidates := candidatesByName(c.landing(), c.poolMap())
			if assert.Len(t, candidates, 1) {
				assert.Equal(t, tc.wantFuzzy, candidates[0].fuzzyMatched)
				assert.False(t, candidates[0].fullNameMatch)
			}
		})
	}
}

func TestAreInitials(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		initials, words []string
		want            bool
	}{
		{"two initials", []string{"p", "o"}, []string{"pierre", "olivier"}, true},
		{"accented initial", []string{"é", "o"}, []string{"émile", "olivier"}, true},
		{"one initial", []string{"p"}, []string{"pierre"}, false},
		{"fewer words", []string{"p", "o"}, []string{"pierre"}, false},
		{"word as initial", []string{"pi", "o"}, []string{"pierre", "olivier"}, false},
		{"digit as initial", []string{"1", "o"}, []string{"1a", "olivier"}, false},
		{"word no longer than its initial", []string{"p", "o"}, []string{"pierre", "o"}, false},
		{"other letter", []string{"p", "a"}, []string{"pierre", "olivier"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, areInitials(tc.initials, tc.words))
		})
	}
}
