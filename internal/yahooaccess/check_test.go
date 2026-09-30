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
	testSeason         = 2026
	testPreviousSeason = 2025
	testTimeout        = 5 * time.Second
	gameKeyURL         = "https://fantasysports.yahooapis.com/fantasy/v2/games;game_codes=nhl;seasons=2026"
	previousGameKeyURL = "https://fantasysports.yahooapis.com/fantasy/v2/games;game_codes=nhl;seasons=2025"
	leagueOneURL       = "https://fantasysports.yahooapis.com/fantasy/v2/league/465.l.111/settings"
	leagueTwoURL       = "https://fantasysports.yahooapis.com/fantasy/v2/league/465.l.222/settings"
	previousLeagueURL  = "https://fantasysports.yahooapis.com/fantasy/v2/league/464.l.9/settings"
	gameKeyXML         = `<fantasy_content><games><game><game_key>465</game_key><code>nhl</code><season>2026</season></game></games></fantasy_content>`
	previousGameKeyXML = `<fantasy_content><games><game><game_key>464</game_key><code>nhl</code><season>2025</season></game></games></fantasy_content>`
	leagueXML          = `<fantasy_content><league><league_key>465.l.111</league_key></league></fantasy_content>`
	notAuthorized      = "This application is not authorized to perform this action"
	yahooErrorXML      = `<?xml version="1.0" encoding="UTF-8"?>
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

func runCheck(t *testing.T, checks []SeasonCheck, responses map[string]stubResponse) (Report, []string) {
	t.Helper()
	transport := &stubTransport{t: t, responses: responses}
	checker := Checker{Client: &http.Client{Transport: transport}, Timeout: testTimeout}
	return checker.Check(context.Background(), checks, testCheckedAt), transport.requested
}

func runPrimaryCheck(t *testing.T, responses map[string]stubResponse) (Report, []string) {
	t.Helper()
	return runCheck(t, []SeasonCheck{{Season: testSeason, LeagueIDs: testLeagues}}, responses)
}

func TestCheck_GameKeyForbiddenIsNotAuthorizedAndSkipsLeagues(t *testing.T) {
	report, requested := runPrimaryCheck(t, map[string]stubResponse{
		gameKeyURL: {status: http.StatusForbidden, body: yahooErrorXML},
	})

	require.Equal(t, NotAuthorized, report.Outcome())
	require.Equal(t, []string{gameKeyURL}, requested)
	primary, ok := report.Primary()
	require.True(t, ok)
	require.Equal(t, NotAuthorized, primary.Outcome)
	require.Equal(t, 0, primary.LeaguesServed)
	require.Equal(t, 2, primary.LeaguesChecked)
	require.Len(t, primary.Probes, 3)
	require.Equal(t, notAuthorized, primary.Probes[0].Detail)
	require.True(t, primary.Probes[1].Skipped)
	require.True(t, primary.Probes[2].Skipped)
}

func TestCheck_AllServedIsAuthorized(t *testing.T) {
	report, requested := runPrimaryCheck(t, map[string]stubResponse{
		gameKeyURL:   {status: http.StatusOK, body: gameKeyXML},
		leagueOneURL: {status: http.StatusOK, body: leagueXML},
		leagueTwoURL: {status: http.StatusOK, body: leagueXML},
	})

	require.Equal(t, Authorized, report.Outcome())
	require.Equal(t, []string{gameKeyURL, leagueOneURL, leagueTwoURL}, requested)
	primary, _ := report.Primary()
	require.Equal(t, Authorized, primary.Outcome)
	require.Equal(t, 2, primary.LeaguesServed)
}

func TestCheck_OneLeagueForbiddenIsNotAuthorized(t *testing.T) {
	report, _ := runPrimaryCheck(t, map[string]stubResponse{
		gameKeyURL:   {status: http.StatusOK, body: gameKeyXML},
		leagueOneURL: {status: http.StatusOK, body: leagueXML},
		leagueTwoURL: {status: http.StatusForbidden, body: yahooErrorXML},
	})

	require.Equal(t, NotAuthorized, report.Outcome())
	primary, _ := report.Primary()
	require.Equal(t, 1, primary.LeaguesServed)
}

// The previous season is probed as a control: when the requested season is
// refused but the one before it is served, the app still reaches Yahoo and the
// problem is the new season alone.
func TestCheck_PreviousSeasonIsProbedAsControl(t *testing.T) {
	report, requested := runCheck(t, []SeasonCheck{
		{Season: testSeason, LeagueIDs: testLeagues},
		{Season: testPreviousSeason, LeagueIDs: []int{9}},
	}, map[string]stubResponse{
		gameKeyURL:         {status: http.StatusForbidden, body: yahooErrorXML},
		previousGameKeyURL: {status: http.StatusOK, body: previousGameKeyXML},
		previousLeagueURL:  {status: http.StatusOK, body: leagueXML},
	})

	require.Equal(t, NotAuthorized, report.Outcome())
	require.Equal(t, []string{gameKeyURL, previousGameKeyURL, previousLeagueURL}, requested)
	require.Len(t, report.Seasons, 2)
	require.Equal(t, testSeason, report.Seasons[0].Season)
	require.Equal(t, NotAuthorized, report.Seasons[0].Outcome)
	require.Equal(t, testPreviousSeason, report.Seasons[1].Season)
	require.Equal(t, Authorized, report.Seasons[1].Outcome)
	require.Equal(t, 1, report.Seasons[1].LeaguesServed)
}

// A season with no configured leagues is still probed through its game key.
func TestCheck_PreviousSeasonGameKeyOnly(t *testing.T) {
	report, requested := runCheck(t, []SeasonCheck{
		{Season: testSeason, LeagueIDs: testLeagues},
		{Season: testPreviousSeason},
	}, map[string]stubResponse{
		gameKeyURL:         {status: http.StatusOK, body: gameKeyXML},
		leagueOneURL:       {status: http.StatusOK, body: leagueXML},
		leagueTwoURL:       {status: http.StatusOK, body: leagueXML},
		previousGameKeyURL: {status: http.StatusOK, body: previousGameKeyXML},
	})

	require.Equal(t, Authorized, report.Outcome())
	require.Equal(t, []string{gameKeyURL, leagueOneURL, leagueTwoURL, previousGameKeyURL}, requested)
	require.Len(t, report.Seasons, 2)
	require.Equal(t, 0, report.Seasons[1].LeaguesChecked)
	require.Equal(t, Authorized, report.Seasons[1].Outcome)
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
			report, _ := runPrimaryCheck(t, responses)
			require.Equal(t, Failed, report.Outcome())
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

func TestResolveChecks(t *testing.T) {
	seasons := config.YahooSeasonsMap{
		2025: {Leagues: []config.League{{LeagueID: 9}, {LeagueID: 10}}},
		2026: {Leagues: []config.League{{LeagueID: 111}, {LeagueID: 222}}},
	}

	checks, err := ResolveChecks(seasons, 0)
	require.NoError(t, err)
	require.Equal(t, []SeasonCheck{
		{Season: 2026, LeagueIDs: []int{111, 222}},
		{Season: 2025, LeagueIDs: []int{9, 10}},
	}, checks)

	// An explicit season keeps the previous one as its control.
	checks, err = ResolveChecks(seasons, 2025)
	require.NoError(t, err)
	require.Equal(t, []SeasonCheck{
		{Season: 2025, LeagueIDs: []int{9, 10}},
		{Season: 2024},
	}, checks)

	// A requested season that is not configured is refused.
	_, err = ResolveChecks(seasons, 2024)
	require.Error(t, err)
}

func TestErrorDetail(t *testing.T) {
	require.Equal(t, notAuthorized, errorDetail([]byte(yahooErrorXML)))
	require.Equal(t, "plain text body", errorDetail([]byte("plain\n  text body")))

	long := errorDetail([]byte(strings.Repeat("x", maxDetailRunes*2)))
	require.Equal(t, maxDetailRunes+len("..."), len([]rune(long)))
}
