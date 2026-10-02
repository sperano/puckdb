// Package yahooaccess checks whether the Yahoo Fantasy API serves a season's
// leagues to puckdb's app, by making the calls the importer makes: the
// season's game key lookup, then each configured league's settings.
//
// It exists for the period during which Yahoo answers 403 "This application
// is not authorized to perform this action" for a new season while the app's
// access request is pending (see config.LeagueMetadataSource), so a daily job
// can report the day that changes.
//
// When the game key lookup returns no key, the check falls back to the game
// key the importer cached in the data path, as the importer itself does, so
// the leagues are still probed.
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
	"github.com/sperano/puckdb/internal/store"
)

const (
	// maxResponseBytes bounds how much of a response is read; league
	// settings are a few kilobytes.
	maxResponseBytes = 1 << 20
	// maxDetailRunes bounds the response excerpt quoted in the report when
	// Yahoo's error body carries no description.
	maxDetailRunes = 300
	// gameKeyProbeName names the game key lookup, the first probe of a season.
	gameKeyProbeName = "game key"
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
	// informational marks a probe reported but left out of the outcome: a
	// game key lookup bypassed by a cached game key.
	informational bool
}

// SeasonCheck names one season to probe: its game key, then the settings of
// the given leagues. An empty LeagueIDs skips the league calls, so the game
// key alone decides the season.
type SeasonCheck struct {
	Season    int
	LeagueIDs []int
}

// SeasonResult is the outcome of one SeasonCheck.
type SeasonResult struct {
	Season         int
	Outcome        Outcome
	Probes         []Probe
	LeaguesServed  int
	LeaguesChecked int
	// GameKeyNote says where the league calls' game key came from when the
	// lookup returned none: the cached copy, or why none was usable. Empty
	// when the lookup returned the key or no league was to be probed.
	GameKeyNote string
}

// Report is the result of one check. The requested season comes first in
// Seasons; the season before it follows as a control, so the report shows
// whether the app still reaches a season Yahoo already serves.
type Report struct {
	CheckedAt time.Time
	// Error is set when the check could not start (no seasons config, no
	// usable token), in which case Seasons is empty.
	Error string
	// Seasons are the checked seasons, the requested one first.
	Seasons []SeasonResult
}

// Outcome is the requested season's verdict, which drives the exit code. It is
// Failed when the check could not start or recorded no season. The control
// season is diagnostic only: it is reported, but does not change the verdict.
func (r Report) Outcome() Outcome {
	primary, ok := r.Primary()
	if r.Error != "" || !ok {
		return Failed
	}
	return primary.Outcome
}

// Primary returns the requested season's result, the first checked season.
func (r Report) Primary() (SeasonResult, bool) {
	if len(r.Seasons) == 0 {
		return SeasonResult{}, false
	}
	return r.Seasons[0], true
}

// Checker probes Yahoo with an authenticated client.
type Checker struct {
	Client *http.Client
	// Timeout bounds each call; it must be positive.
	Timeout time.Duration
	// Storage reads the data path the importer caches Yahoo files in. When
	// the game key lookup returns no key, the league calls use the cached
	// game key instead. Nil disables the fallback.
	Storage store.Storage
}

// FailedReport is the report of a check that could not start.
func FailedReport(checkedAt time.Time, err error) Report {
	return Report{CheckedAt: checkedAt, Error: err.Error()}
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
	return season, leagueIDs(cfg), nil
}

// leagueIDs returns a season's configured league IDs.
func leagueIDs(cfg config.Season) []int {
	ids := make([]int, 0, len(cfg.Leagues))
	for _, league := range cfg.Leagues {
		ids = append(ids, league.LeagueID)
	}
	return ids
}

// ResolveChecks returns the seasons to check, the requested one first: the
// requested season (or the latest configured season when requested is 0), then
// the season before it as a control. The control's league IDs come from the
// seasons config when it lists the season, so the game key is always probed
// and the leagues too when they are known.
func ResolveChecks(seasons config.YahooSeasonsMap, requested int) ([]SeasonCheck, error) {
	primary, leagueIDs, err := ResolveSeason(seasons, requested)
	if err != nil {
		return nil, err
	}
	checks := []SeasonCheck{{Season: primary, LeagueIDs: leagueIDs}}
	if previous := primary - 1; previous > 0 {
		checks = append(checks, SeasonCheck{Season: previous, LeagueIDs: configuredLeagueIDs(seasons, previous)})
	}
	return checks, nil
}

// configuredLeagueIDs returns a season's configured league IDs, or nil when
// the season is not in the config.
func configuredLeagueIDs(seasons config.YahooSeasonsMap, season int) []int {
	cfg, ok := seasons[season]
	if !ok {
		return nil
	}
	return leagueIDs(cfg)
}

// Check probes every named season's game key, then its league settings
// (skipped when the game key is unknown), and classifies each season's
// results.
func (c Checker) Check(ctx context.Context, checks []SeasonCheck, checkedAt time.Time) Report {
	report := Report{CheckedAt: checkedAt, Seasons: make([]SeasonResult, 0, len(checks))}
	for _, check := range checks {
		report.Seasons = append(report.Seasons, c.checkSeason(ctx, check))
	}
	return report
}

// checkSeason resolves one season's game key, then fetches its league settings.
func (c Checker) checkSeason(ctx context.Context, check SeasonCheck) SeasonResult {
	result := SeasonResult{Season: check.Season, LeaguesChecked: len(check.LeagueIDs)}
	gameKeyProbe, gameKey, note := c.resolveGameKey(ctx, check)
	result.Probes = append(result.Probes, gameKeyProbe)
	result.GameKeyNote = note
	for _, leagueID := range check.LeagueIDs {
		name := fmt.Sprintf("league %d settings", leagueID)
		if gameKey == 0 {
			result.Probes = append(result.Probes, Probe{Name: name, Skipped: true, Detail: "not checked: game key unknown"})
			continue
		}
		probe, _ := c.probe(ctx, name, resource.League{Season: check.Season, LeagueID: leagueID, GameKey: gameKey}.URL())
		if probe.StatusCode == http.StatusOK {
			result.LeaguesServed++
		}
		result.Probes = append(result.Probes, probe)
	}
	result.Outcome = classify(result.Probes)
	return result
}

// resolveGameKey probes the season's game key lookup and returns the probe,
// the game key (0 when unknown) and the SeasonResult.GameKeyNote. When the
// lookup returns no key and leagues are to be probed, it falls back to the
// cached game key; the lookup is then informational, as it is to the
// importer, and the league calls decide the outcome. A data path that cannot
// be read fails the probe: the check cannot tell what the importer would do.
func (c Checker) resolveGameKey(ctx context.Context, check SeasonCheck) (Probe, int, string) {
	gameKeyResource := resource.GameKey{Season: check.Season}
	probe, body := c.probe(ctx, gameKeyProbeName, gameKeyResource.URL())
	if probe.StatusCode == http.StatusOK {
		key, err := gameKeyResource.ParseKey(body)
		if err == nil {
			return probe, key, ""
		}
		probe.Detail, probe.failed = err.Error(), true
	}
	if len(check.LeagueIDs) == 0 {
		return probe, 0, ""
	}
	key, note, err := c.cachedGameKey(ctx, check.Season)
	if err != nil {
		probe.failed = true
		return probe, 0, note
	}
	probe.informational = key != 0
	return probe, key, note
}

// classify returns Failed when any probe failed, else NotAuthorized when any
// probe got a 403 (or was skipped because of one), else Authorized.
// Informational probes do not count.
func classify(probes []Probe) Outcome {
	outcome := Authorized
	for _, p := range probes {
		switch {
		case p.informational:
			// A game key lookup bypassed by the cached key.
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
