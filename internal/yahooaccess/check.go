// Package yahooaccess checks whether the Yahoo Fantasy API serves a season's
// leagues to puckdb's app, by making the calls the importer makes: the
// season's game key lookup, then each configured league's settings.
//
// It exists for the period during which Yahoo answers 403 "This application
// is not authorized to perform this action" for a new season while the app's
// access request is pending (see config.LeagueMetadataSource), so a daily job
// can report the day that changes.
package yahooaccess

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/resource"
)

const (
	// maxResponseBytes bounds how much of a response is read; league
	// settings are a few kilobytes.
	maxResponseBytes = 1 << 20
	// maxDetailRunes bounds the response excerpt quoted in the report when
	// Yahoo's error body carries no description.
	maxDetailRunes = 300
)

// Outcome is the verdict of one check.
type Outcome string

const (
	// Authorized means every call returned 200.
	Authorized Outcome = "AUTHORIZED"
	// NotAuthorized means every failed call was a 403.
	NotAuthorized Outcome = "NOT AUTHORIZED"
	// Failed means the check could not decide: no usable token, a network
	// error, an unexpected status or an unreadable response.
	Failed Outcome = "ERROR"
)

// Probe is the result of one Yahoo API call.
type Probe struct {
	Name string
	URL  string
	// StatusCode is 0 when no response was received, or when the call was
	// skipped because the game key is unknown.
	StatusCode int
	// Detail is Yahoo's error description, a response excerpt, or the
	// transport or parse error.
	Detail  string
	Skipped bool
	// failed marks a probe that makes the whole check undecided.
	failed bool
}

// Report is the result of one check.
type Report struct {
	Season    int
	CheckedAt time.Time
	Outcome   Outcome
	// Error is set when the check could not start (no season, no token).
	Error          string
	Probes         []Probe
	LeaguesServed  int
	LeaguesChecked int
}

// Checker probes Yahoo with an authenticated client.
type Checker struct {
	Client *http.Client
	// Timeout bounds each call; it must be positive.
	Timeout time.Duration
}

// FailedReport is the report of a check that could not start.
func FailedReport(season int, checkedAt time.Time, err error) Report {
	return Report{Season: season, CheckedAt: checkedAt, Outcome: Failed, Error: err.Error()}
}

// ResolveSeason returns the season to check and its league IDs: season
// itself, or the latest configured season when season is 0.
func ResolveSeason(seasons config.YahooSeasonsMap, season int) (int, []int, error) {
	if season == 0 {
		if len(seasons) == 0 {
			return 0, nil, errors.New("the yahoo seasons config lists no season")
		}
		season = slices.Max(slices.Collect(maps.Keys(seasons)))
	}
	cfg, ok := seasons[season]
	if !ok || len(cfg.Leagues) == 0 {
		return season, nil, fmt.Errorf("the yahoo seasons config lists no league for season %d", season)
	}
	leagueIDs := make([]int, 0, len(cfg.Leagues))
	for _, league := range cfg.Leagues {
		leagueIDs = append(leagueIDs, league.LeagueID)
	}
	return season, leagueIDs, nil
}

// Check resolves the season's game key, then fetches every league's settings
// (skipped when the game key is unknown), and classifies the results.
func (c Checker) Check(ctx context.Context, season int, leagueIDs []int, checkedAt time.Time) Report {
	report := Report{Season: season, CheckedAt: checkedAt, LeaguesChecked: len(leagueIDs)}
	gameKeyResource := resource.GameKey{Season: season}
	gameKeyProbe, body := c.probe(ctx, "game key", gameKeyResource.URL())
	gameKey := 0
	if gameKeyProbe.StatusCode == http.StatusOK {
		key, err := gameKeyResource.ParseKey(body)
		if err != nil {
			gameKeyProbe.Detail, gameKeyProbe.failed = err.Error(), true
		}
		gameKey = key
	}
	report.Probes = append(report.Probes, gameKeyProbe)

	for _, leagueID := range leagueIDs {
		name := fmt.Sprintf("league %d settings", leagueID)
		if gameKey == 0 {
			report.Probes = append(report.Probes, Probe{Name: name, Skipped: true, Detail: "not checked: game key unknown"})
			continue
		}
		probe, _ := c.probe(ctx, name, resource.League{Season: season, LeagueID: leagueID, GameKey: gameKey}.URL())
		if probe.StatusCode == http.StatusOK {
			report.LeaguesServed++
		}
		report.Probes = append(report.Probes, probe)
	}
	report.Outcome = classify(report.Probes)
	return report
}

// classify returns Failed when any probe failed, else NotAuthorized when any
// probe got a 403 (or was skipped because of one), else Authorized.
func classify(probes []Probe) Outcome {
	outcome := Authorized
	for _, p := range probes {
		switch {
		case p.failed:
			return Failed
		case p.Skipped:
			// A skip follows a game key 403 or failure, both classified.
		case p.StatusCode == http.StatusForbidden:
			outcome = NotAuthorized
		}
	}
	return outcome
}

// probe GETs url and returns the probe with the body of a 200 response.
func (c Checker) probe(ctx context.Context, name, url string) (Probe, []byte) {
	probe := Probe{Name: name, URL: url}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		probe.Detail, probe.failed = err.Error(), true
		return probe, nil
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		probe.Detail, probe.failed = err.Error(), true
		return probe, nil
	}
	defer resp.Body.Close()
	probe.StatusCode = resp.StatusCode
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		probe.Detail, probe.failed = fmt.Sprintf("read response: %v", err), true
		return probe, nil
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return probe, body
	case http.StatusForbidden:
		probe.Detail = errorDetail(body)
	default:
		probe.Detail, probe.failed = errorDetail(body), true
	}
	return probe, nil
}

// yahooError is the body of a Yahoo Fantasy API error response.
type yahooError struct {
	Description string `xml:"description"`
}

// errorDetail returns Yahoo's error description, or a bounded excerpt of a
// body that is not a Yahoo error document.
func errorDetail(body []byte) string {
	var e yahooError
	if err := xml.Unmarshal(body, &e); err == nil && strings.TrimSpace(e.Description) != "" {
		return strings.TrimSpace(e.Description)
	}
	excerpt := []rune(strings.Join(strings.Fields(string(body)), " "))
	if len(excerpt) > maxDetailRunes {
		return string(excerpt[:maxDetailRunes]) + "..."
	}
	return string(excerpt)
}
