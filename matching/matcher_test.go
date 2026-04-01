package matching

import (
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchYahooID_NameAndJerseyMatch(t *testing.T) {
	t.Parallel()

	sweater := 97
	landing := &nhl.PlayerLanding{
		FirstName:     nhl.LocalizedString{Default: "Connor"},
		LastName:      nhl.LocalizedString{Default: "McDavid"},
		SweaterNumber: &sweater,
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		100: {YahooID: 100, FirstName: "Connor", LastName: "McDavid", JerseyNumber: 97, Team: "Edmonton"},
		101: {YahooID: 101, FirstName: "Connor", LastName: "Brown", JerseyNumber: 28, Team: "Ottawa"},
	}

	result, err := MatchYahooID(landing, "EDM", time.Time{}, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(100), result.YahooID)
	assert.Equal(t, MatchReasonNameJersey, result.Reason)
}

func TestMatchYahooID_NameOnlyMatch(t *testing.T) {
	t.Parallel()

	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		100: {YahooID: 100, FirstName: "Connor", LastName: "McDavid", JerseyNumber: 97, Team: "Edmonton"},
		101: {YahooID: 101, FirstName: "Connor", LastName: "Brown", JerseyNumber: 28, Team: "Ottawa"},
	}

	result, err := MatchYahooID(landing, "EDM", time.Time{}, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(100), result.YahooID)
	assert.Equal(t, MatchReasonNameOnly, result.Reason)
}

func TestMatchYahooID_TeamTiebreaker(t *testing.T) {
	t.Parallel()

	// Two players with same name, different teams
	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "Elias"},
		LastName:  nhl.LocalizedString{Default: "Pettersson"},
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		200: {YahooID: 200, FirstName: "Elias", LastName: "Pettersson", JerseyNumber: 40, Team: "Vancouver"},
		201: {YahooID: 201, FirstName: "Elias", LastName: "Pettersson", JerseyNumber: 28, Team: "Carolina"},
	}

	result, err := MatchYahooID(landing, "VAN", time.Time{}, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(200), result.YahooID)
	assert.Equal(t, MatchReasonTeamTiebreaker, result.Reason)
}

func TestMatchYahooID_NoMatch(t *testing.T) {
	t.Parallel()

	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "Wayne"},
		LastName:  nhl.LocalizedString{Default: "Gretzky"},
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		100: {YahooID: 100, FirstName: "Connor", LastName: "McDavid", JerseyNumber: 97},
	}

	result, err := MatchYahooID(landing, "EDM", time.Time{}, pool)
	require.NoError(t, err)
	assert.False(t, result.Matched)
	assert.Equal(t, MatchReasonNoMatch, result.Reason)
}

func TestMatchYahooID_Ambiguous(t *testing.T) {
	t.Parallel()

	// Two players with same name and team
	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "John"},
		LastName:  nhl.LocalizedString{Default: "Smith"},
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		300: {YahooID: 300, FirstName: "John", LastName: "Smith", JerseyNumber: 10, Team: "Boston"},
		301: {YahooID: 301, FirstName: "John", LastName: "Smith", JerseyNumber: 20, Team: "Boston"},
	}

	result, err := MatchYahooID(landing, "BOS", time.Time{}, pool)
	assert.Error(t, err)
	assert.False(t, result.Matched)
	assert.Equal(t, MatchReasonAmbiguous, result.Reason)
}

func TestMatchYahooID_CaseInsensitive(t *testing.T) {
	t.Parallel()

	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "CONNOR"},
		LastName:  nhl.LocalizedString{Default: "MCDAVID"},
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		100: {YahooID: 100, FirstName: "connor", LastName: "mcdavid", JerseyNumber: 97},
	}

	result, err := MatchYahooID(landing, "EDM", time.Time{}, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(100), result.YahooID)
}

func TestMatchYahooID_JerseyZeroFallback(t *testing.T) {
	t.Parallel()

	// NHL player has jersey, Yahoo player has 0 (retired), should still match by name
	sweater := 99
	landing := &nhl.PlayerLanding{
		FirstName:     nhl.LocalizedString{Default: "Wayne"},
		LastName:      nhl.LocalizedString{Default: "Gretzky"},
		SweaterNumber: &sweater,
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		99: {YahooID: 99, FirstName: "Wayne", LastName: "Gretzky", JerseyNumber: 0}, // Retired player, no jersey
	}

	result, err := MatchYahooID(landing, "", time.Time{}, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(99), result.YahooID)
	assert.Equal(t, MatchReasonNameOnly, result.Reason)
}

func TestMatchYahooID_NicknameMatch(t *testing.T) {
	t.Parallel()

	// NHL player "Rejean Lemelin" should match Yahoo player "Reggie Lemelin"
	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "Rejean"},
		LastName:  nhl.LocalizedString{Default: "Lemelin"},
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		332: {YahooID: 332, FirstName: "Reggie", LastName: "Lemelin", JerseyNumber: 1, Team: "Boston"},
	}

	result, err := MatchYahooID(landing, "BOS", time.Time{}, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(332), result.YahooID)
}

func TestMatchYahooID_NicknameMatchReverse(t *testing.T) {
	t.Parallel()

	// Test the reverse: NHL "Mike" should match Yahoo "Michael"
	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "Mike"},
		LastName:  nhl.LocalizedString{Default: "Smith"},
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		500: {YahooID: 500, FirstName: "Michael", LastName: "Smith", JerseyNumber: 31, Team: "Edmonton"},
	}

	result, err := MatchYahooID(landing, "EDM", time.Time{}, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(500), result.YahooID)
}

func TestMatchYahooID_AccentNormalization(t *testing.T) {
	t.Parallel()

	// NHL "Daniel Brière" should match Yahoo "Daniel Briere" (no accent)
	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "Daniel"},
		LastName:  nhl.LocalizedString{Default: "Brière"},
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		1737: {YahooID: 1737, FirstName: "Daniel", LastName: "Briere", JerseyNumber: 48, Team: "Philadelphia"},
	}

	result, err := MatchYahooID(landing, "PHI", time.Time{}, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(1737), result.YahooID)
}

func TestMatchYahooID_AccentNormalizationUmlaut(t *testing.T) {
	t.Parallel()

	// NHL "Juuso Välimäki" should match Yahoo "Juuso Valimaki"
	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "Juuso"},
		LastName:  nhl.LocalizedString{Default: "Välimäki"},
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		7531: {YahooID: 7531, FirstName: "Juuso", LastName: "Valimaki", JerseyNumber: 4, Team: "Calgary"},
	}

	result, err := MatchYahooID(landing, "CGY", time.Time{}, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(7531), result.YahooID)
}

func TestMatchYahooID_HTMLEntityApostrophe(t *testing.T) {
	t.Parallel()

	// Yahoo "Rod Brind&#x27;Amour" (HTML entity) should match NHL "Rod Brind'Amour"
	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "Rod"},
		LastName:  nhl.LocalizedString{Default: "Brind'Amour"},
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		45: {YahooID: 45, FirstName: "Rod", LastName: "Brind&#x27;Amour", JerseyNumber: 0, Team: "Carolina"},
	}

	result, err := MatchYahooID(landing, "CAR", time.Time{}, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(45), result.YahooID)
}

func TestMatchYahooID_RolandRollie(t *testing.T) {
	t.Parallel()

	// NHL "Roland Melanson" should match Yahoo "Rollie Melanson"
	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "Roland"},
		LastName:  nhl.LocalizedString{Default: "Melanson"},
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		375: {YahooID: 375, FirstName: "Rollie", LastName: "Melanson", JerseyNumber: 0, Team: ""},
	}

	result, err := MatchYahooID(landing, "", time.Time{}, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(375), result.YahooID)
}

func TestMatchYahooID_TrailingWhitespace(t *testing.T) {
	t.Parallel()

	// NHL API has trailing space in "Matěj " - should still match Yahoo "Matej Blumel"
	sweater := 13
	landing := &nhl.PlayerLanding{
		FirstName:     nhl.LocalizedString{Default: "Matěj "},
		LastName:      nhl.LocalizedString{Default: "Blümel"},
		SweaterNumber: &sweater,
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		8378: {YahooID: 8378, FirstName: "Matej", LastName: "Blumel", JerseyNumber: 13, Team: "Boston"},
	}

	result, err := MatchYahooID(landing, "BOS", time.Time{}, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(8378), result.YahooID)
	assert.Equal(t, MatchReasonNameJersey, result.Reason)
}

func TestMatchYahooID_BirthDateTiebreaker(t *testing.T) {
	t.Parallel()

	// Two players with same name - use birth date to disambiguate
	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "Bryan"},
		LastName:  nhl.LocalizedString{Default: "Hextall"},
	}

	// Bryan Hextall Sr. (1913) and Bryan Hextall Jr. (1941)
	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		22424: {
			YahooID:   22424,
			FirstName: "Bryan",
			LastName:  "Hextall",
			BirthDate: time.Date(1913, time.July, 31, 0, 0, 0, 0, time.UTC),
		},
		22425: {
			YahooID:   22425,
			FirstName: "Bryan",
			LastName:  "Hextall",
			BirthDate: time.Date(1941, time.May, 23, 0, 0, 0, 0, time.UTC),
		},
	}

	// Match Bryan Sr. by birth date
	nhlBirthDate := time.Date(1913, time.July, 31, 0, 0, 0, 0, time.UTC)
	result, err := MatchYahooID(landing, "", nhlBirthDate, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(22424), result.YahooID)
	assert.Equal(t, MatchReasonNameBirthdate, result.Reason)

	// Match Bryan Jr. by birth date
	nhlBirthDate = time.Date(1941, time.May, 23, 0, 0, 0, 0, time.UTC)
	result, err = MatchYahooID(landing, "", nhlBirthDate, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(22425), result.YahooID)
	assert.Equal(t, MatchReasonNameBirthdate, result.Reason)
}

func TestMatchYahooID_CompoundFirstName(t *testing.T) {
	t.Parallel()

	// Charles Alexis Legault: NHL splits as FirstName="Charles Alexis", LastName="Legault"
	// but Yahoo splits as FirstName="Charles", LastName="Alexis Legault"
	// Full name is the same: "Charles Alexis Legault"
	sweater := 62
	landing := &nhl.PlayerLanding{
		FirstName:     nhl.LocalizedString{Default: "Charles Alexis"},
		LastName:      nhl.LocalizedString{Default: "Legault"},
		SweaterNumber: &sweater,
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		30710: {
			YahooID:      30710,
			FirstName:    "Charles",
			LastName:     "Alexis Legault", // Yahoo split the name differently
			JerseyNumber: 62,
			Team:         "Carolina",
		},
	}

	result, err := MatchYahooID(landing, "CAR", time.Time{}, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(30710), result.YahooID)
	assert.Equal(t, MatchReasonFullName, result.Reason) // Should use fullname reason since split differs
}

func TestMatchYahooID_CompoundFirstName_WithJersey(t *testing.T) {
	t.Parallel()

	// Same scenario but verifying jersey match also works
	sweater := 62
	landing := &nhl.PlayerLanding{
		FirstName:     nhl.LocalizedString{Default: "Charles Alexis"},
		LastName:      nhl.LocalizedString{Default: "Legault"},
		SweaterNumber: &sweater,
	}

	// Add another player with same jersey to ensure full name matching is used
	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		30710: {
			YahooID:      30710,
			FirstName:    "Charles",
			LastName:     "Alexis Legault",
			JerseyNumber: 62,
			Team:         "Carolina",
		},
		99999: {
			YahooID:      99999,
			FirstName:    "Someone",
			LastName:     "Else",
			JerseyNumber: 62,
			Team:         "Carolina",
		},
	}

	result, err := MatchYahooID(landing, "CAR", time.Time{}, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(30710), result.YahooID)
}

func TestMatchYahooID_CompoundFirstName_NoMatch(t *testing.T) {
	t.Parallel()

	// Ensure compound first name doesn't match a different person
	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "Charles Alexis"},
		LastName:  nhl.LocalizedString{Default: "Legault"},
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		12345: {
			YahooID:   12345,
			FirstName: "Charles",
			LastName:  "Smith", // Different person entirely
		},
	}

	result, err := MatchYahooID(landing, "", time.Time{}, pool)
	require.NoError(t, err)
	assert.False(t, result.Matched)
	assert.Equal(t, MatchReasonNoMatch, result.Reason)
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

func TestLookupTeamID(t *testing.T) {
	t.Parallel()

	assert.Equal(t, int64(8), LookupTeamID("MTL"))
	assert.Equal(t, int64(22), LookupTeamID("EDM"))
	assert.Equal(t, int64(0), LookupTeamID("INVALID"))
}

func TestLookupTeamByID(t *testing.T) {
	t.Parallel()

	info := LookupTeamByID(8)
	require.NotNil(t, info)
	assert.Equal(t, "MTL", info.Abbrev)
	assert.Equal(t, "Montréal Canadiens", info.FullName)

	assert.Nil(t, LookupTeamByID(99999))
}

func TestLookupTeamAbbrev(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "MTL", LookupTeamAbbrev(8))
	assert.Equal(t, "EDM", LookupTeamAbbrev(22))
	assert.Equal(t, "", LookupTeamAbbrev(99999))
}

func TestMatchYahooID_AmbiguousNoDisambiguator(t *testing.T) {
	t.Parallel()

	// Two players with same name, no jersey, no birthdate, no team → ambiguous
	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "John"},
		LastName:  nhl.LocalizedString{Default: "Smith"},
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		300: {YahooID: 300, FirstName: "John", LastName: "Smith"},
		301: {YahooID: 301, FirstName: "John", LastName: "Smith"},
	}

	result, err := MatchYahooID(landing, "", time.Time{}, pool)
	assert.Error(t, err)
	assert.False(t, result.Matched)
	assert.Equal(t, MatchReasonAmbiguous, result.Reason)
}

func TestMatchYahooID_MultipleJerseyMatchesThenTeamTiebreaker(t *testing.T) {
	t.Parallel()

	// Two players with same name AND same jersey → needs team tiebreaker
	sweater := 10
	landing := &nhl.PlayerLanding{
		FirstName:     nhl.LocalizedString{Default: "John"},
		LastName:      nhl.LocalizedString{Default: "Smith"},
		SweaterNumber: &sweater,
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		300: {YahooID: 300, FirstName: "John", LastName: "Smith", JerseyNumber: 10, Team: "Boston"},
		301: {YahooID: 301, FirstName: "John", LastName: "Smith", JerseyNumber: 10, Team: "Edmonton"},
	}

	result, err := MatchYahooID(landing, "EDM", time.Time{}, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(301), result.YahooID)
	assert.Equal(t, MatchReasonTeamTiebreaker, result.Reason)
}

func TestMatchYahooID_MultipleBirthDateMatchesThenTeamTiebreaker(t *testing.T) {
	t.Parallel()

	// Two players with same name AND same birthdate → needs team tiebreaker
	birthDate := time.Date(1990, time.January, 1, 0, 0, 0, 0, time.UTC)
	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "John"},
		LastName:  nhl.LocalizedString{Default: "Smith"},
	}

	pool := map[store.YahooPlayerID]*store.YahooPlayer{
		300: {YahooID: 300, FirstName: "John", LastName: "Smith", BirthDate: birthDate, Team: "Boston"},
		301: {YahooID: 301, FirstName: "John", LastName: "Smith", BirthDate: birthDate, Team: "Edmonton"},
	}

	result, err := MatchYahooID(landing, "EDM", birthDate, pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, store.YahooPlayerID(301), result.YahooID)
	assert.Equal(t, MatchReasonTeamTiebreaker, result.Reason)
}

func TestNhlAbbrevToYahooTeam(t *testing.T) {
	t.Parallel()

	// Verify all 32+ teams are mapped
	tests := []struct {
		abbrev   string
		wantTeam string
	}{
		{"BOS", "Boston"},
		{"NYR", "NY Rangers"},
		{"NYI", "NY Islanders"},
		{"TBL", "Tampa Bay"},
		{"STL", "St. Louis"},
		{"VGK", "Vegas"},
		{"UTA", "Utah"},
	}

	for _, tt := range tests {
		t.Run(tt.abbrev, func(t *testing.T) {
			assert.Equal(t, tt.wantTeam, nhlAbbrevToYahooTeam[tt.abbrev])
		})
	}

	// Verify we have entries for all expected teams
	assert.GreaterOrEqual(t, len(nhlAbbrevToYahooTeam), 32)
}
