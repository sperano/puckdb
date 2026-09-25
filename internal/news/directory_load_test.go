package news

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sperano/puckdb/internal/sqlcdb"
)

// fakeDirectoryQueries is a configurable stand-in for DirectoryQueries.
type fakeDirectoryQueries struct {
	players         []sqlcdb.ListNewsResolverPlayersRow
	playersErr      error
	yahooPlayers    []sqlcdb.ListNewsYahooPlayersRow
	yahooPlayersErr error
	fetches         []sqlcdb.ListNewsYahooPoolFetchesRow
	fetchesErr      error
	teams           []sqlcdb.ListNewsTeamsRow
	teamsErr        error
}

func (f *fakeDirectoryQueries) ListNewsResolverPlayers(context.Context) ([]sqlcdb.ListNewsResolverPlayersRow, error) {
	return f.players, f.playersErr
}

func (f *fakeDirectoryQueries) ListNewsYahooPlayers(context.Context, int32) ([]sqlcdb.ListNewsYahooPlayersRow, error) {
	return f.yahooPlayers, f.yahooPlayersErr
}

func (f *fakeDirectoryQueries) ListNewsYahooPoolFetches(context.Context, int32) ([]sqlcdb.ListNewsYahooPoolFetchesRow, error) {
	return f.fetches, f.fetchesErr
}

func (f *fakeDirectoryQueries) ListNewsTeams(context.Context) ([]sqlcdb.ListNewsTeamsRow, error) {
	return f.teams, f.teamsErr
}

const (
	mcnabbYahooTestID = 4321
	retiredTimerID    = 8400001
)

func directoryQueriesFixture() *fakeDirectoryQueries {
	return &fakeDirectoryQueries{
		players: []sqlcdb.ListNewsResolverPlayersRow{
			{ID: mcnabbID, YahooID: pgtype.Int8{Int64: mcnabbYahooTestID, Valid: true},
				FirstName: "Brayden", LastName: "McNabb", IsActive: true, TeamAbbrev: "VGK"},
			{ID: retiredTimerID, FirstName: "Old", LastName: "Timer", IsActive: false, TeamAbbrev: "TOR"},
		},
		yahooPlayers: []sqlcdb.ListNewsYahooPlayersRow{
			{PlayerID: mckennaYahooID, FullName: "Gavin McKenna", EditorialTeamAbbr: "TOR"},
		},
		teams: []sqlcdb.ListNewsTeamsRow{
			{Abbrev: "VGK", FullName: "Vegas Golden Knights", CommonName: "Golden Knights"},
			{Abbrev: "TOR", FullName: "Toronto Maple Leafs", CommonName: "Maple Leafs"},
		},
	}
}

func TestLoadDirectoryMapsYahooIDFromPlayers(t *testing.T) {
	dir, err := LoadDirectory(context.Background(), directoryQueriesFixture(), testSeason)
	require.NoError(t, err)
	mentions := dir.Resolve(Story{Title: "Brayden McNabb: Suspended"})
	m := mentionByText(t, mentions, "brayden mcnabb")
	assert.Equal(t, ResolutionResolved, m.Resolution)
	assert.Equal(t, mcnabbYahooTestID, m.Player.YahooPlayerID)
}

func TestLoadDirectoryInactivePlayersAreNotNameMatched(t *testing.T) {
	dir, err := LoadDirectory(context.Background(), directoryQueriesFixture(), testSeason)
	require.NoError(t, err)
	mentions := dir.Resolve(Story{Title: "Old Timer: Retires"})
	m := mentionByText(t, mentions, "old timer")
	assert.NotEqual(t, ResolutionResolved, m.Resolution, "an inactive player is not indexed by name")
}

func TestLoadDirectoryMatchesYahooOnlyPlayers(t *testing.T) {
	dir, err := LoadDirectory(context.Background(), directoryQueriesFixture(), testSeason)
	require.NoError(t, err)
	mentions := dir.Resolve(Story{Title: "Gavin McKenna: Signs deal"})
	m := mentionByText(t, mentions, "gavin mckenna")
	assert.Equal(t, ResolutionResolved, m.Resolution)
	assert.Equal(t, mckennaYahooID, m.Player.YahooPlayerID)
	assert.Zero(t, m.Player.NHLPlayerID, "no NHL record exists for this rookie yet")
}

func TestLoadDirectoryWrapsQueryErrors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name    string
		mutate  func(*fakeDirectoryQueries)
		wantMsg string
	}{
		{"players", func(f *fakeDirectoryQueries) { f.playersErr = boom }, "list players:"},
		{"yahoo players", func(f *fakeDirectoryQueries) { f.yahooPlayersErr = boom }, "list Yahoo pool players of season"},
		{"teams", func(f *fakeDirectoryQueries) { f.teamsErr = boom }, "list teams:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := directoryQueriesFixture()
			tt.mutate(f)
			_, err := LoadDirectory(context.Background(), f, testSeason)
			require.Error(t, err)
			assert.ErrorIs(t, err, boom)
			assert.Contains(t, err.Error(), tt.wantMsg)
		})
	}
}

func TestLoadYahooStatusesAsOfIsOldestLeagueFetch(t *testing.T) {
	older := coverageNow.Add(-3 * time.Hour)
	newer := coverageNow.Add(-1 * time.Hour)
	f := &fakeDirectoryQueries{
		fetches: []sqlcdb.ListNewsYahooPoolFetchesRow{
			{LeagueKey: "league-a", FetchedAt: pgtype.Timestamptz{Time: newer, Valid: true}},
			{LeagueKey: "league-b", FetchedAt: pgtype.Timestamptz{Time: older, Valid: true}},
		},
		yahooPlayers: []sqlcdb.ListNewsYahooPlayersRow{
			{PlayerID: lyonYahooID, FullName: "Alex Lyon", Status: "DTD", StatusFull: "Day-to-Day",
				FetchedAt: pgtype.Timestamptz{Time: newer, Valid: true}},
		},
	}
	statuses, asOf, err := LoadYahooStatuses(context.Background(), f, testSeason)
	require.NoError(t, err)
	assert.True(t, asOf.Equal(older), "as-of is the oldest of the leagues' latest fetches")
	require.Len(t, statuses, 1)
	assert.Equal(t, lyonYahooID, statuses[0].YahooPlayerID)
	assert.Equal(t, "DTD", statuses[0].Status)
}

func TestLoadYahooStatusesNoPoolsIsWrappedError(t *testing.T) {
	_, _, err := LoadYahooStatuses(context.Background(), &fakeDirectoryQueries{}, testSeason)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoYahooPools))
}

func TestLoadYahooStatusesWrapsQueryErrors(t *testing.T) {
	boom := errors.New("boom")
	f := &fakeDirectoryQueries{fetchesErr: boom}
	_, _, err := LoadYahooStatuses(context.Background(), f, testSeason)
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "list Yahoo pool fetches of season")

	f = &fakeDirectoryQueries{
		fetches:         []sqlcdb.ListNewsYahooPoolFetchesRow{{LeagueKey: "a", FetchedAt: pgtype.Timestamptz{Time: coverageNow, Valid: true}}},
		yahooPlayersErr: boom,
	}
	_, _, err = LoadYahooStatuses(context.Background(), f, testSeason)
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "list Yahoo pool players of season")
}
