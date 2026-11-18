package mcpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const staleLeagueKey = "500.l.1001"

// fakeLeagueLister counts its calls so a test can show no query ran.
type fakeLeagueLister struct {
	leagues []sqlcdb.YahooLeague
	err     error
	calls   int
}

func (f *fakeLeagueLister) GetAllYahooLeagues(context.Context) ([]sqlcdb.YahooLeague, error) {
	f.calls++
	return f.leagues, f.err
}

func newFakeLeagueLister() *fakeLeagueLister {
	return &fakeLeagueLister{leagues: []sqlcdb.YahooLeague{
		{ID: allowedLeagueID, LeagueKey: allowedLeagueKey},
		{ID: otherLeagueID, LeagueKey: otherLeagueKey},
	}}
}

func yahooOptions(keys ...string) Options {
	return Options{Toolsets: []Toolset{ToolsetYahoo}, YahooLeagues: keys}
}

func TestCheckLeagueKeysAcceptsImportedKeys(t *testing.T) {
	t.Parallel()
	lister := newFakeLeagueLister()
	require.NoError(t, CheckLeagueKeys(t.Context(), lister, yahooOptions(allowedLeagueKey, otherLeagueKey)))
	assert.Equal(t, 1, lister.calls)
}

func TestCheckLeagueKeysRejectsMissingKeys(t *testing.T) {
	t.Parallel()
	const secondStale = "500.l.2002"
	err := CheckLeagueKeys(t.Context(), newFakeLeagueLister(), yahooOptions(allowedLeagueKey, staleLeagueKey, secondStale))
	require.Error(t, err)
	assert.Contains(t, err.Error(), staleLeagueKey+leagueKeyListSeparator+secondStale)
	assert.NotContains(t, err.Error(), allowedLeagueKey)
}

func TestCheckLeagueKeysSkipsWithoutQuery(t *testing.T) {
	t.Parallel()
	cases := map[string]Options{
		"no allowlist":  yahooOptions(),
		"no yahoo tool": {Toolsets: []Toolset{ToolsetNHL}, YahooLeagues: []string{staleLeagueKey}},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			lister := newFakeLeagueLister()
			require.NoError(t, CheckLeagueKeys(t.Context(), lister, opts))
			assert.Zero(t, lister.calls)
		})
	}
}

func TestCheckLeagueKeysPropagatesQueryError(t *testing.T) {
	t.Parallel()
	queryErr := errors.New("connection refused")
	lister := &fakeLeagueLister{err: queryErr}
	err := CheckLeagueKeys(t.Context(), lister, yahooOptions(allowedLeagueKey))
	require.ErrorIs(t, err, queryErr)
}
