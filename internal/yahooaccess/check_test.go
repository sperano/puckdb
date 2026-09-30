package yahooaccess

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/stretchr/testify/require"
)

const (
	testSeason    = 2026
	testTimeout   = 5 * time.Second
	gameKeyURL    = "https://fantasysports.yahooapis.com/fantasy/v2/games;game_codes=nhl;seasons=2026"
	leagueOneURL  = "https://fantasysports.yahooapis.com/fantasy/v2/league/465.l.111/settings"
	leagueTwoURL  = "https://fantasysports.yahooapis.com/fantasy/v2/league/465.l.222/settings"
	gameKeyXML    = `<fantasy_content><games><game><game_key>465</game_key><code>nhl</code><season>2026</season></game></games></fantasy_content>`
	leagueXML     = `<fantasy_content><league><league_key>465.l.111</league_key></league></fantasy_content>`
	notAuthorized = "This application is not authorized to perform this action"
	yahooErrorXML = `<?xml version="1.0" encoding="UTF-8"?>
<error xml:lang="en-us" xmlns="http://www.yahooapis.com/v1/base.rng"><description>` + notAuthorized + `</description><detail/></error>`
)

var (
	testCheckedAt = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	testLeagues   = []int{111, 222}
)

type stubResponse struct {
	status int
	body   string
	err    error
}

// stubTransport answers each URL with a canned response; unknown URLs fail
// the test.
type stubTransport struct {
	t         *testing.T
	responses map[string]stubResponse
	requested []string
}

func (s *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	url := req.URL.String()
	s.requested = append(s.requested, url)
	resp, ok := s.responses[url]
	if !ok {
		s.t.Errorf("unexpected request %s", url)
		return nil, errors.New("unexpected request")
	}
	if resp.err != nil {
		return nil, resp.err
	}
	return &http.Response{
		StatusCode: resp.status,
		Body:       io.NopCloser(strings.NewReader(resp.body)),
		Header:     http.Header{},
		Request:    req,
	}, nil
}

func runCheck(t *testing.T, responses map[string]stubResponse) (Report, []string) {
	t.Helper()
	transport := &stubTransport{t: t, responses: responses}
	checker := Checker{Client: &http.Client{Transport: transport}, Timeout: testTimeout}
	return checker.Check(context.Background(), testSeason, testLeagues, testCheckedAt), transport.requested
}

func TestCheck_GameKeyForbiddenIsNotAuthorizedAndSkipsLeagues(t *testing.T) {
	report, requested := runCheck(t, map[string]stubResponse{
		gameKeyURL: {status: http.StatusForbidden, body: yahooErrorXML},
	})

	require.Equal(t, NotAuthorized, report.Outcome)
	require.Equal(t, []string{gameKeyURL}, requested)
	require.Equal(t, 0, report.LeaguesServed)
	require.Equal(t, 2, report.LeaguesChecked)
	require.Len(t, report.Probes, 3)
	require.Equal(t, notAuthorized, report.Probes[0].Detail)
	require.True(t, report.Probes[1].Skipped)
	require.True(t, report.Probes[2].Skipped)
}

func TestCheck_AllServedIsAuthorized(t *testing.T) {
	report, requested := runCheck(t, map[string]stubResponse{
		gameKeyURL:   {status: http.StatusOK, body: gameKeyXML},
		leagueOneURL: {status: http.StatusOK, body: leagueXML},
		leagueTwoURL: {status: http.StatusOK, body: leagueXML},
	})

	require.Equal(t, Authorized, report.Outcome)
	require.Equal(t, []string{gameKeyURL, leagueOneURL, leagueTwoURL}, requested)
	require.Equal(t, 2, report.LeaguesServed)
}

func TestCheck_OneLeagueForbiddenIsNotAuthorized(t *testing.T) {
	report, _ := runCheck(t, map[string]stubResponse{
		gameKeyURL:   {status: http.StatusOK, body: gameKeyXML},
		leagueOneURL: {status: http.StatusOK, body: leagueXML},
		leagueTwoURL: {status: http.StatusForbidden, body: yahooErrorXML},
	})

	require.Equal(t, NotAuthorized, report.Outcome)
	require.Equal(t, 1, report.LeaguesServed)
}

func TestCheck_UndecidedResponsesAreErrors(t *testing.T) {
	tests := map[string]map[string]stubResponse{
		"server error": {
			gameKeyURL:   {status: http.StatusOK, body: gameKeyXML},
			leagueOneURL: {status: http.StatusForbidden, body: yahooErrorXML},
			leagueTwoURL: {status: http.StatusServiceUnavailable, body: "try later"},
		},
		"unauthorized token": {
			gameKeyURL: {status: http.StatusUnauthorized, body: yahooErrorXML},
		},
		"transport error": {
			gameKeyURL: {err: errors.New("connection refused")},
		},
		"unreadable game key": {
			gameKeyURL: {status: http.StatusOK, body: "<fantasy_content/>"},
		},
	}
	for name, responses := range tests {
		t.Run(name, func(t *testing.T) {
			report, _ := runCheck(t, responses)
			require.Equal(t, Failed, report.Outcome)
		})
	}
}

func TestResolveSeason(t *testing.T) {
	seasons := config.YahooSeasonsMap{
		2025: {Leagues: []config.League{{LeagueID: 9}}},
		2026: {Leagues: []config.League{{LeagueID: 111}, {LeagueID: 222}}},
		2027: {},
	}

	season, leagues, err := ResolveSeason(seasons, 2025)
	require.NoError(t, err)
	require.Equal(t, 2025, season)
	require.Equal(t, []int{9}, leagues)

	// The latest season has no league: reported, not silently skipped.
	season, _, err = ResolveSeason(seasons, 0)
	require.Error(t, err)
	require.Equal(t, 2027, season)

	delete(seasons, 2027)
	season, leagues, err = ResolveSeason(seasons, 0)
	require.NoError(t, err)
	require.Equal(t, testSeason, season)
	require.Equal(t, testLeagues, leagues)

	_, _, err = ResolveSeason(config.YahooSeasonsMap{}, 0)
	require.Error(t, err)
}

func TestErrorDetail(t *testing.T) {
	require.Equal(t, notAuthorized, errorDetail([]byte(yahooErrorXML)))
	require.Equal(t, "plain text body", errorDetail([]byte("plain\n  text body")))

	long := errorDetail([]byte(strings.Repeat("x", maxDetailRunes*2)))
	require.Equal(t, maxDetailRunes+len("..."), len([]rune(long)))
}
