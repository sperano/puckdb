package worker

import (
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
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

	pool := map[int]*cache.YahooPlayer{
		100: {YahooID: 100, FirstName: "Connor", LastName: "McDavid", JerseyNumber: 97, Team: "Edmonton"},
		101: {YahooID: 101, FirstName: "Connor", LastName: "Brown", JerseyNumber: 28, Team: "Ottawa"},
	}

	result, err := MatchYahooID(landing, "EDM", pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, 100, result.YahooID)
	assert.Equal(t, "name+jersey", result.Reason)
}

func TestMatchYahooID_NameOnlyMatch(t *testing.T) {
	t.Parallel()

	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "Connor"},
		LastName:  nhl.LocalizedString{Default: "McDavid"},
	}

	pool := map[int]*cache.YahooPlayer{
		100: {YahooID: 100, FirstName: "Connor", LastName: "McDavid", JerseyNumber: 97, Team: "Edmonton"},
		101: {YahooID: 101, FirstName: "Connor", LastName: "Brown", JerseyNumber: 28, Team: "Ottawa"},
	}

	result, err := MatchYahooID(landing, "EDM", pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, 100, result.YahooID)
	assert.Equal(t, "name-only", result.Reason)
}

func TestMatchYahooID_TeamTiebreaker(t *testing.T) {
	t.Parallel()

	// Two players with same name, different teams
	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "Elias"},
		LastName:  nhl.LocalizedString{Default: "Pettersson"},
	}

	pool := map[int]*cache.YahooPlayer{
		200: {YahooID: 200, FirstName: "Elias", LastName: "Pettersson", JerseyNumber: 40, Team: "Vancouver"},
		201: {YahooID: 201, FirstName: "Elias", LastName: "Pettersson", JerseyNumber: 28, Team: "Carolina"},
	}

	result, err := MatchYahooID(landing, "VAN", pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, 200, result.YahooID)
	assert.Equal(t, "team-tiebreaker", result.Reason)
}

func TestMatchYahooID_NoMatch(t *testing.T) {
	t.Parallel()

	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "Wayne"},
		LastName:  nhl.LocalizedString{Default: "Gretzky"},
	}

	pool := map[int]*cache.YahooPlayer{
		100: {YahooID: 100, FirstName: "Connor", LastName: "McDavid", JerseyNumber: 97},
	}

	result, err := MatchYahooID(landing, "EDM", pool)
	require.NoError(t, err)
	assert.False(t, result.Matched)
	assert.Equal(t, "no-match", result.Reason)
}

func TestMatchYahooID_Ambiguous(t *testing.T) {
	t.Parallel()

	// Two players with same name and team
	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "John"},
		LastName:  nhl.LocalizedString{Default: "Smith"},
	}

	pool := map[int]*cache.YahooPlayer{
		300: {YahooID: 300, FirstName: "John", LastName: "Smith", JerseyNumber: 10, Team: "Boston"},
		301: {YahooID: 301, FirstName: "John", LastName: "Smith", JerseyNumber: 20, Team: "Boston"},
	}

	result, err := MatchYahooID(landing, "BOS", pool)
	assert.Error(t, err)
	assert.False(t, result.Matched)
	assert.Equal(t, "ambiguous", result.Reason)
}

func TestMatchYahooID_CaseInsensitive(t *testing.T) {
	t.Parallel()

	landing := &nhl.PlayerLanding{
		FirstName: nhl.LocalizedString{Default: "CONNOR"},
		LastName:  nhl.LocalizedString{Default: "MCDAVID"},
	}

	pool := map[int]*cache.YahooPlayer{
		100: {YahooID: 100, FirstName: "connor", LastName: "mcdavid", JerseyNumber: 97},
	}

	result, err := MatchYahooID(landing, "EDM", pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, 100, result.YahooID)
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

	pool := map[int]*cache.YahooPlayer{
		99: {YahooID: 99, FirstName: "Wayne", LastName: "Gretzky", JerseyNumber: 0}, // Retired player, no jersey
	}

	result, err := MatchYahooID(landing, "", pool)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, 99, result.YahooID)
	assert.Equal(t, "name-only", result.Reason)
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
