package matching

import (
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// matchCase is one row of the MatchYahooID disambiguation matrix. A zero
// sweater means the landing has no jersey number; a zero birth, no birth
// date; an empty team, no current team.
type matchCase struct {
	name        string
	first, last string
	sweater     int
	birth       time.Time
	team        string
	pool        []store.YahooPlayer
	wantID      store.YahooPlayerID // zero when no player may match
	wantReason  MatchReason
}

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

var (
	hextallSrBirth = date(1913, time.July, 31)
	hextallJrBirth = date(1941, time.May, 23)
	smithBirth     = date(1990, time.January, 1)
)

// nameCases cover step 1: which Yahoo names count as the NHL player's name.
var nameCases = []matchCase{
	{name: "exact name", first: "Connor", last: "McDavid", team: "EDM",
		pool: []store.YahooPlayer{
			{YahooID: 100, FirstName: "Connor", LastName: "McDavid", JerseyNumber: 97, Team: "Edmonton"},
			{YahooID: 101, FirstName: "Connor", LastName: "Brown", JerseyNumber: 28, Team: "Ottawa"},
		},
		wantID: 100, wantReason: MatchReasonNameOnly},
	{name: "no name match", first: "Wayne", last: "Gretzky", team: "EDM",
		pool:       []store.YahooPlayer{{YahooID: 100, FirstName: "Connor", LastName: "McDavid", JerseyNumber: 97}},
		wantReason: MatchReasonNoMatch},
	{name: "case insensitive", first: "CONNOR", last: "MCDAVID", team: "EDM",
		pool:   []store.YahooPlayer{{YahooID: 100, FirstName: "connor", LastName: "mcdavid", JerseyNumber: 97}},
		wantID: 100, wantReason: MatchReasonNameOnly},
	{name: "nickname alias", first: "Rejean", last: "Lemelin", team: "BOS",
		pool:   []store.YahooPlayer{{YahooID: 332, FirstName: "Reggie", LastName: "Lemelin", JerseyNumber: 1, Team: "Boston"}},
		wantID: 332, wantReason: MatchReasonNameOnly},
	{name: "nickname alias reversed", first: "Mike", last: "Smith", team: "EDM",
		pool:   []store.YahooPlayer{{YahooID: 500, FirstName: "Michael", LastName: "Smith", JerseyNumber: 31, Team: "Edmonton"}},
		wantID: 500, wantReason: MatchReasonNameOnly},
	{name: "nickname alias Roland/Rollie", first: "Roland", last: "Melanson",
		pool:   []store.YahooPlayer{{YahooID: 375, FirstName: "Rollie", LastName: "Melanson"}},
		wantID: 375, wantReason: MatchReasonNameOnly},
	{name: "nickname needs the same last name", first: "Mike", last: "Smith",
		pool:       []store.YahooPlayer{{YahooID: 500, FirstName: "Michael", LastName: "Smyth"}},
		wantReason: MatchReasonNoMatch},
	{name: "accent stripped", first: "Daniel", last: "Brière", team: "PHI",
		pool:   []store.YahooPlayer{{YahooID: 1737, FirstName: "Daniel", LastName: "Briere", JerseyNumber: 48, Team: "Philadelphia"}},
		wantID: 1737, wantReason: MatchReasonNameOnly},
	{name: "umlaut stripped", first: "Juuso", last: "Välimäki", team: "CGY",
		pool:   []store.YahooPlayer{{YahooID: 7531, FirstName: "Juuso", LastName: "Valimaki", JerseyNumber: 4, Team: "Calgary"}},
		wantID: 7531, wantReason: MatchReasonNameOnly},
	{name: "HTML entity apostrophe", first: "Rod", last: "Brind'Amour", team: "CAR",
		pool:   []store.YahooPlayer{{YahooID: 45, FirstName: "Rod", LastName: "Brind&#x27;Amour", Team: "Carolina"}},
		wantID: 45, wantReason: MatchReasonNameOnly},
	{name: "trailing whitespace and accents", first: "Matěj ", last: "Blümel", sweater: 13, team: "BOS",
		pool:   []store.YahooPlayer{{YahooID: 8378, FirstName: "Matej", LastName: "Blumel", JerseyNumber: 13, Team: "Boston"}},
		wantID: 8378, wantReason: MatchReasonNameJersey},
	{name: "compound first name split differently", first: "Charles Alexis", last: "Legault", sweater: 62, team: "CAR",
		pool:   []store.YahooPlayer{{YahooID: 30710, FirstName: "Charles", LastName: "Alexis Legault", JerseyNumber: 62, Team: "Carolina"}},
		wantID: 30710, wantReason: MatchReasonFullName},
	{name: "compound first name with a jersey namesake", first: "Charles Alexis", last: "Legault", sweater: 62, team: "CAR",
		pool: []store.YahooPlayer{
			{YahooID: 30710, FirstName: "Charles", LastName: "Alexis Legault", JerseyNumber: 62, Team: "Carolina"},
			{YahooID: 99999, FirstName: "Someone", LastName: "Else", JerseyNumber: 62, Team: "Carolina"},
		},
		wantID: 30710, wantReason: MatchReasonFullName},
	{name: "compound first name is not a first-name match", first: "Charles Alexis", last: "Legault",
		pool:       []store.YahooPlayer{{YahooID: 12345, FirstName: "Charles", LastName: "Smith"}},
		wantReason: MatchReasonNoMatch},
	// Punctuation is part of the name for this matcher (see NormalizeName):
	// only HTML-escaped and identical punctuation match.
	{name: "same apostrophe", first: "Ryan", last: "O'Reilly", team: "NSH",
		pool:   []store.YahooPlayer{{YahooID: 4000, FirstName: "Ryan", LastName: "O'Reilly", Team: "Nashville"}},
		wantID: 4000, wantReason: MatchReasonNameOnly},
	{name: "typographic apostrophe differs", first: "Ryan", last: "O’Reilly", team: "NSH",
		pool:       []store.YahooPlayer{{YahooID: 4000, FirstName: "Ryan", LastName: "O'Reilly", Team: "Nashville"}},
		wantReason: MatchReasonNoMatch},
	{name: "dropped apostrophe differs", first: "Ryan", last: "O'Reilly", team: "NSH",
		pool:       []store.YahooPlayer{{YahooID: 4000, FirstName: "Ryan", LastName: "OReilly", Team: "Nashville"}},
		wantReason: MatchReasonNoMatch},
	{name: "hyphen differs from a space", first: "Jean-Gabriel", last: "Pageau", team: "NYI",
		pool:       []store.YahooPlayer{{YahooID: 4001, FirstName: "Jean Gabriel", LastName: "Pageau", Team: "NY Islanders"}},
		wantReason: MatchReasonNoMatch},
}

// jerseyCases cover step 2: the jersey number singles out a name match.
var jerseyCases = []matchCase{
	{name: "jersey singles out", first: "Connor", last: "McDavid", sweater: 97, team: "EDM",
		pool: []store.YahooPlayer{
			{YahooID: 100, FirstName: "Connor", LastName: "McDavid", JerseyNumber: 97, Team: "Edmonton"},
			{YahooID: 101, FirstName: "Connor", LastName: "Brown", JerseyNumber: 28, Team: "Ottawa"},
		},
		wantID: 100, wantReason: MatchReasonNameJersey},
	{name: "no jersey match falls back to the name", first: "Wayne", last: "Gretzky", sweater: 99,
		pool:   []store.YahooPlayer{{YahooID: 99, FirstName: "Wayne", LastName: "Gretzky"}},
		wantID: 99, wantReason: MatchReasonNameOnly},
}

// birthDateCases cover step 3: the birth date singles out namesakes.
var birthDateCases = []matchCase{
	{name: "birth date picks the father", first: "Bryan", last: "Hextall", birth: hextallSrBirth,
		pool:   hextallPool(),
		wantID: 22424, wantReason: MatchReasonNameBirthdate},
	{name: "birth date picks the son", first: "Bryan", last: "Hextall", birth: hextallJrBirth,
		pool:   hextallPool(),
		wantID: 22425, wantReason: MatchReasonNameBirthdate},
	{name: "birth date within a day", first: "Bryan", last: "Hextall", birth: hextallJrBirth.Add(birthDateTolerance),
		pool:   hextallPool(),
		wantID: 22425, wantReason: MatchReasonNameBirthdate},
	{name: "birth date beyond a day is ignored", first: "Bryan", last: "Hextall", birth: hextallJrBirth.Add(2 * birthDateTolerance),
		pool:       hextallPool(),
		wantReason: MatchReasonAmbiguous},
}

// teamCases cover step 4: the current team breaks the remaining ties.
var teamCases = []matchCase{
	{name: "team breaks a name tie", first: "Elias", last: "Pettersson", team: "VAN",
		pool: []store.YahooPlayer{
			{YahooID: 200, FirstName: "Elias", LastName: "Pettersson", JerseyNumber: 40, Team: "Vancouver"},
			{YahooID: 201, FirstName: "Elias", LastName: "Pettersson", JerseyNumber: 28, Team: "Carolina"},
		},
		wantID: 200, wantReason: MatchReasonTeamTiebreaker},
	{name: "team breaks a jersey tie", first: "John", last: "Smith", sweater: 10, team: "EDM",
		pool: []store.YahooPlayer{
			{YahooID: 300, FirstName: "John", LastName: "Smith", JerseyNumber: 10, Team: "Boston"},
			{YahooID: 301, FirstName: "John", LastName: "Smith", JerseyNumber: 10, Team: "Edmonton"},
		},
		wantID: 301, wantReason: MatchReasonTeamTiebreaker},
	{name: "team breaks a birth date tie", first: "John", last: "Smith", birth: smithBirth, team: "EDM",
		pool: []store.YahooPlayer{
			{YahooID: 300, FirstName: "John", LastName: "Smith", BirthDate: smithBirth, Team: "Boston"},
			{YahooID: 301, FirstName: "John", LastName: "Smith", BirthDate: smithBirth, Team: "Edmonton"},
		},
		wantID: 301, wantReason: MatchReasonTeamTiebreaker},
	{name: "relocated team keeps its Yahoo name", first: "Clayton", last: "Keller", team: "ARI",
		pool: []store.YahooPlayer{
			{YahooID: 400, FirstName: "Clayton", LastName: "Keller", Team: "Arizona"},
			{YahooID: 401, FirstName: "Clayton", LastName: "Keller", Team: "Utah"},
		},
		wantID: 400, wantReason: MatchReasonTeamTiebreaker},
}

// ambiguousCases cover step 5: several candidates survive every step.
var ambiguousCases = []matchCase{
	{name: "same team", first: "John", last: "Smith", team: "BOS",
		pool: []store.YahooPlayer{
			{YahooID: 300, FirstName: "John", LastName: "Smith", JerseyNumber: 10, Team: "Boston"},
			{YahooID: 301, FirstName: "John", LastName: "Smith", JerseyNumber: 20, Team: "Boston"},
		},
		wantReason: MatchReasonAmbiguous},
	{name: "no disambiguator", first: "John", last: "Smith",
		pool: []store.YahooPlayer{
			{YahooID: 300, FirstName: "John", LastName: "Smith"},
			{YahooID: 301, FirstName: "John", LastName: "Smith"},
		},
		wantReason: MatchReasonAmbiguous},
	{name: "unknown team", first: "John", last: "Smith", team: "XXX",
		pool: []store.YahooPlayer{
			{YahooID: 300, FirstName: "John", LastName: "Smith", Team: "Boston"},
			{YahooID: 301, FirstName: "John", LastName: "Smith", Team: "Edmonton"},
		},
		wantReason: MatchReasonAmbiguous},
	{name: "team matches neither", first: "John", last: "Smith", team: "TOR",
		pool: []store.YahooPlayer{
			{YahooID: 300, FirstName: "John", LastName: "Smith", Team: "Boston"},
			{YahooID: 301, FirstName: "John", LastName: "Smith", Team: "Edmonton"},
		},
		wantReason: MatchReasonAmbiguous},
}

func hextallPool() []store.YahooPlayer {
	return []store.YahooPlayer{
		{YahooID: 22424, FirstName: "Bryan", LastName: "Hextall", BirthDate: hextallSrBirth},
		{YahooID: 22425, FirstName: "Bryan", LastName: "Hextall", BirthDate: hextallJrBirth},
	}
}

func (c matchCase) landing() *nhl.PlayerLanding {
	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: c.first},
		LastName:  nhl.LocalizedString{Default: c.last},
	}
	if c.sweater != 0 {
		landing.SweaterNumber = &c.sweater
	}
	return landing
}

func (c matchCase) poolMap() map[store.YahooPlayerID]*store.YahooPlayer {
	pool := make(map[store.YahooPlayerID]*store.YahooPlayer, len(c.pool))
	for i := range c.pool {
		pool[c.pool[i].YahooID] = &c.pool[i]
	}
	return pool
}

func TestMatchYahooID(t *testing.T) {
	t.Parallel()

	groups := []struct {
		step  string
		cases []matchCase
	}{
		{"name", nameCases},
		{"jersey", jerseyCases},
		{"birth date", birthDateCases},
		{"team", teamCases},
		{"ambiguous", ambiguousCases},
	}
	for _, g := range groups {
		for _, tc := range g.cases {
			t.Run(g.step+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				result, err := MatchYahooID(tc.landing(), tc.team, tc.birth, tc.poolMap())
				if tc.wantReason == MatchReasonAmbiguous {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				assert.Equal(t, tc.wantReason, result.Reason)
				assert.Equal(t, tc.wantID != 0, result.Matched)
				assert.Equal(t, tc.wantID, result.YahooID)
			})
		}
	}
}

func TestMatchReason_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		reason MatchReason
		want   string
	}{
		{MatchReasonNoMatch, "no-match"},
		{MatchReasonNameOnly, "name-only"},
		{MatchReasonNameJersey, "name+jersey"},
		{MatchReasonNameBirthdate, "name+birthdate"},
		{MatchReasonFullName, "fullname"},
		{MatchReasonTeamTiebreaker, "team-tiebreaker"},
		{MatchReasonAmbiguous, "ambiguous"},
		{MatchReason(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.reason.String())
		})
	}
}

func TestMatchYahooID_PropagatesImageURL(t *testing.T) {
	t.Parallel()

	sweater := 97
	landing := &nhl.PlayerLanding{
		FirstName:     nhl.LocalizedString{Default: "Connor"},
		LastName:      nhl.LocalizedString{Default: "McDavid"},
		SweaterNumber: &sweater,
	}

	const wantURL = "https://s.yimg.com/xe/i/us/sp/v/nhl_cutout/players_l/10172025/100.png"
	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		100: {YahooID: 100, FirstName: "Connor", LastName: "McDavid", JerseyNumber: 97, Team: "Edmonton", ImageURL: wantURL},
	}

	result, err := MatchYahooID(landing, "EDM", time.Time{}, pool)
	require.NoError(t, err)
	require.True(t, result.Matched)
	assert.Equal(t, wantURL, result.ImageURL)
}

func TestMatchYahooID_NoMatchHasEmptyImageURL(t *testing.T) {
	t.Parallel()

	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "Nobody"},
		LastName:  nhl.LocalizedString{Default: "Here"},
	}
	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		100: {YahooID: 100, FirstName: "Connor", LastName: "McDavid", ImageURL: "https://example.com/100.png"},
	}

	result, err := MatchYahooID(landing, "EDM", time.Time{}, pool)
	require.NoError(t, err)
	require.False(t, result.Matched)
	assert.Empty(t, result.ImageURL)
}
