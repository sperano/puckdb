package yahooaccess

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/require"
)

// otherGameKeyXML is a cached key that differs from the lookup's, to show
// which one the league calls use.
const otherGameKeyXML = `<fantasy_content><games><game><game_key>999</game_key><code>nhl</code><season>2026</season></game></games></fantasy_content>`

// cachedStorage is a data path holding the season's cached game key file.
func cachedStorage(t *testing.T, season int, body string) store.Storage {
	t.Helper()
	storage := store.NewMemStorage()
	require.NoError(t, storage.Write(context.Background(), resource.GameKey{Season: season}.Path(), []byte(body)))
	return storage
}

// unreadableStorage fails every read, like a data path whose mount is broken.
type unreadableStorage struct {
	store.Storage
}

func (unreadableStorage) Read(context.Context, string) ([]byte, error) {
	return nil, errors.New("input/output error")
}

func runCachedCheck(t *testing.T, storage store.Storage, checks []SeasonCheck, responses map[string]stubResponse) (Report, []string) {
	t.Helper()
	transport := &stubTransport{t: t, responses: responses}
	checker := Checker{Client: &http.Client{Transport: transport}, Timeout: testTimeout, Storage: storage}
	return checker.Check(context.Background(), checks, testCheckedAt), transport.requested
}

func primaryChecks() []SeasonCheck {
	return []SeasonCheck{{Season: testSeason, LeagueIDs: testLeagues}}
}

// A refused lookup does not stop the league calls when the game key is
// cached: the importer reads the cached key and never makes the lookup, so
// the leagues decide.
func TestCheck_CachedGameKeyProbesLeaguesAfterRefusedLookup(t *testing.T) {
	report, requested := runCachedCheck(t, cachedStorage(t, testSeason, gameKeyXML), primaryChecks(), map[string]stubResponse{
		gameKeyURL:   {status: http.StatusForbidden, body: yahooErrorXML},
		leagueOneURL: {status: http.StatusOK, body: leagueXML},
		leagueTwoURL: {status: http.StatusOK, body: leagueXML},
	})

	require.Equal(t, []string{gameKeyURL, leagueOneURL, leagueTwoURL}, requested)
	primary, _ := report.Primary()
	require.Equal(t, Authorized, primary.Outcome)
	require.Equal(t, 2, primary.LeaguesServed)
	require.Equal(t, http.StatusForbidden, primary.Probes[0].StatusCode)
	require.Equal(t, "465, read from game-keys/gamekey-2026.xml in the data path; the league calls use it", primary.GameKeyNote)
	require.Contains(t, report.Text(), "- cached game key: 465, read from game-keys/gamekey-2026.xml")
}

func TestCheck_CachedGameKeyLeaguesForbiddenIsNotAuthorized(t *testing.T) {
	report, _ := runCachedCheck(t, cachedStorage(t, testSeason, gameKeyXML), primaryChecks(), map[string]stubResponse{
		gameKeyURL:   {status: http.StatusForbidden, body: yahooErrorXML},
		leagueOneURL: {status: http.StatusForbidden, body: yahooErrorXML},
		leagueTwoURL: {status: http.StatusOK, body: leagueXML},
	})

	primary, _ := report.Primary()
	require.Equal(t, NotAuthorized, primary.Outcome)
	require.Equal(t, 1, primary.LeaguesServed)
}

// Any lookup failure falls back to the cached key, not only a 403.
func TestCheck_CachedGameKeyAfterFailedLookup(t *testing.T) {
	tests := map[string]stubResponse{
		"server error":        {status: http.StatusServiceUnavailable, body: "try later"},
		"unreadable game key": {status: http.StatusOK, body: "<fantasy_content/>"},
	}
	for name, lookup := range tests {
		t.Run(name, func(t *testing.T) {
			report, requested := runCachedCheck(t, cachedStorage(t, testSeason, gameKeyXML), primaryChecks(), map[string]stubResponse{
				gameKeyURL:   lookup,
				leagueOneURL: {status: http.StatusOK, body: leagueXML},
				leagueTwoURL: {status: http.StatusOK, body: leagueXML},
			})

			require.Equal(t, []string{gameKeyURL, leagueOneURL, leagueTwoURL}, requested)
			require.Equal(t, Authorized, report.Outcome())
		})
	}
}

// Without a usable cached key the leagues are skipped as before, and the note
// says why.
func TestCheck_NoUsableCachedGameKey(t *testing.T) {
	tests := map[string]struct {
		storage  store.Storage
		wantNote string
	}{
		"no data path": {
			wantNote: "none: no data path configured",
		},
		"not cached": {
			storage:  store.NewMemStorage(),
			wantNote: "none: game-keys/gamekey-2026.xml is not in the data path",
		},
		"another season's key": {
			storage: cachedStorage(t, testSeason, previousGameKeyXML),
			wantNote: "none: game-keys/gamekey-2026.xml: parse game key for season 2026: " +
				`yahoo game key 464 is for code "nhl" season 2025`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			report, requested := runCachedCheck(t, tt.storage, primaryChecks(), map[string]stubResponse{
				gameKeyURL: {status: http.StatusForbidden, body: yahooErrorXML},
			})

			require.Equal(t, []string{gameKeyURL}, requested)
			primary, _ := report.Primary()
			require.Equal(t, NotAuthorized, primary.Outcome)
			require.Equal(t, tt.wantNote, primary.GameKeyNote)
			require.True(t, primary.Probes[1].Skipped)
		})
	}
}

// A served lookup wins over the cached copy.
func TestCheck_LookupKeyWinsOverCachedKey(t *testing.T) {
	report, requested := runCachedCheck(t, cachedStorage(t, testSeason, otherGameKeyXML), primaryChecks(), map[string]stubResponse{
		gameKeyURL:   {status: http.StatusOK, body: gameKeyXML},
		leagueOneURL: {status: http.StatusOK, body: leagueXML},
		leagueTwoURL: {status: http.StatusOK, body: leagueXML},
	})

	require.Equal(t, []string{gameKeyURL, leagueOneURL, leagueTwoURL}, requested)
	primary, _ := report.Primary()
	require.Equal(t, Authorized, primary.Outcome)
	require.Empty(t, primary.GameKeyNote)
}

// A season without configured leagues is decided by its lookup alone, so the
// cached key is not read.
func TestCheck_GameKeyOnlySeasonIgnoresCachedKey(t *testing.T) {
	report, _ := runCachedCheck(t, cachedStorage(t, testSeason, gameKeyXML), []SeasonCheck{{Season: testSeason}}, map[string]stubResponse{
		gameKeyURL: {status: http.StatusForbidden, body: yahooErrorXML},
	})

	primary, _ := report.Primary()
	require.Equal(t, NotAuthorized, primary.Outcome)
	require.Empty(t, primary.GameKeyNote)
}

// A data path that cannot be read is a setup problem, not Yahoo's answer: the
// season is undecided instead of NOT AUTHORIZED.
func TestCheck_UnreadableDataPathIsError(t *testing.T) {
	report, requested := runCachedCheck(t, unreadableStorage{Storage: store.NewMemStorage()}, primaryChecks(), map[string]stubResponse{
		gameKeyURL: {status: http.StatusForbidden, body: yahooErrorXML},
	})

	require.Equal(t, []string{gameKeyURL}, requested)
	primary, _ := report.Primary()
	require.Equal(t, Failed, primary.Outcome)
	require.Equal(t, "unknown: read game-keys/gamekey-2026.xml: input/output error", primary.GameKeyNote)
	require.True(t, primary.Probes[1].Skipped)
}
