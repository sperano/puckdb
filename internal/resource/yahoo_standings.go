package resource

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"time"
	// The capture date is a calendar day in YahooFantasyDayZone; the embedded
	// database keeps that zone available in the scratch image.
	_ "time/tzdata"

	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/store"
)

// YahooFantasyDayZone is the time zone whose calendar date labels a
// standings capture. Every NHL game of a day ends before midnight Pacific,
// so a snapshot dated D never carries results of D+1's games, and a capture
// after the last game of D reflects all of D.
const YahooFantasyDayZone = "America/Los_Angeles"

var yahooFantasyDayLocation = mustLoadLocation(YahooFantasyDayZone)

// mustLoadLocation loads a zone the embedded database is known to hold.
func mustLoadLocation(name string) *time.Location {
	location, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return location
}

// StandingsCaptureDate is the calendar day of now in YahooFantasyDayZone,
// held at midnight UTC like every other daily resource date.
func StandingsCaptureDate(now time.Time) time.Time {
	year, month, day := now.In(yahooFantasyDayLocation).Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// LeagueStandings is a league's standings as Yahoo served them on Date: every
// team's rank and, in head-to-head leagues, its record.
//
// Yahoo's standings sub-resource (league/{league_key}/standings) takes no
// date or week filter and always answers with the current standings. The
// file for Date is therefore a capture made on Date, never a backfill: the
// Yahoo season fetch captures the standings once per day under the capture
// date (StandingsCaptureDate), later runs that day read the cached file, and
// no run writes a file for another day. A missing file for a past date means
// no snapshot was captured that day, and nothing fills it in later.
type LeagueStandings struct {
	Season   int
	LeagueID int
	// Date is the capture day, at midnight UTC (see StandingsCaptureDate).
	Date    time.Time
	GameKey int // Required for URL, not for path
}

func (l LeagueStandings) Path() string {
	return fmt.Sprintf("seasons/%d/yahoo/%d/standings/standings-%s.xml",
		l.Season, l.LeagueID, l.Date.Format(time.DateOnly))
}

func (l LeagueStandings) URL() string {
	return fmt.Sprintf("%s/league/%d.l.%d/standings", baseYahooAPIURL, l.GameKey, l.LeagueID)
}

func (l LeagueStandings) Type() core.FileType { return core.YahooLeagueStandings }

// Parse decodes a standings document and rejects one that is not this
// league's or whose standings are missing or malformed, so such a response
// never becomes a cached snapshot.
func (l LeagueStandings) Parse(data []byte) (*store.FantasyContent, error) {
	var content store.FantasyContent
	if err := xml.Unmarshal(data, &content); err != nil {
		return nil, l.parseError(err)
	}
	if err := validateYahooLeagueIdentity(content.League, l.Season, l.LeagueID, l.GameKey); err != nil {
		return nil, l.parseError(err)
	}
	if _, err := TeamStandingRecords(content.League); err != nil {
		return nil, l.parseError(err)
	}
	return &content, nil
}

func (l LeagueStandings) parseError(err error) error {
	return fmt.Errorf("parse standings %d/%d for %s: %w", l.Season, l.LeagueID, l.Date.Format(time.DateOnly), err)
}

// TeamStandingRecord is one team's row of a standings snapshot, converted
// from Yahoo's text values.
type TeamStandingRecord struct {
	TeamID  int
	TeamKey string
	Name    string
	// Rank is 1-based; 0 when Yahoo serves an empty rank, as it does before
	// the season's first game.
	Rank int
	// PlayoffSeed is 0 when Yahoo serves none.
	PlayoffSeed int
	// Outcome is nil in leagues without a head-to-head record (rotisserie).
	Outcome *OutcomeRecord
	// DivisionalOutcome is nil in leagues without divisions.
	DivisionalOutcome *OutcomeRecord
}

// OutcomeRecord is a head-to-head record.
type OutcomeRecord struct {
	Wins   int
	Losses int
	Ties   int
	// Percentage is nil when Yahoo serves none, as it does before the first
	// matchup is scored.
	Percentage *float64
}

// TeamStandingRecords converts a standings document's teams, in Yahoo's
// (rank) order. It fails on a document without standings, a team without an
// ID or team_standings, a duplicate team, a teams count that does not match
// the teams listed, or a rank, seed, outcome or percentage that is not a
// number.
func TeamStandingRecords(league store.League) ([]TeamStandingRecord, error) {
	teams := league.Standings.Teams
	if len(teams.Slice) == 0 {
		return nil, fmt.Errorf("response contains no standings")
	}
	if teams.Count > 0 && teams.Count != len(teams.Slice) {
		return nil, fmt.Errorf("standings list %d teams but count says %d", len(teams.Slice), teams.Count)
	}
	records := make([]TeamStandingRecord, 0, len(teams.Slice))
	seen := make(map[int]struct{}, len(teams.Slice))
	for i, team := range teams.Slice {
		record, err := teamStandingRecord(team)
		if err != nil {
			return nil, fmt.Errorf("standings entry %d: %w", i+1, err)
		}
		if _, dup := seen[record.TeamID]; dup {
			return nil, fmt.Errorf("standings entry %d: team %d listed twice", i+1, record.TeamID)
		}
		seen[record.TeamID] = struct{}{}
		records = append(records, record)
	}
	return records, nil
}

func teamStandingRecord(team store.Team) (TeamStandingRecord, error) {
	if team.ID <= 0 {
		return TeamStandingRecord{}, fmt.Errorf("team %q has no team_id", team.Key)
	}
	if team.TeamStandings == nil {
		return TeamStandingRecord{}, fmt.Errorf("team %d has no team_standings", team.ID)
	}
	standings := team.TeamStandings
	rank, err := optionalCount("rank", standings.Rank)
	if err != nil {
		return TeamStandingRecord{}, fmt.Errorf("team %d: %w", team.ID, err)
	}
	seed, err := optionalCount("playoff_seed", standings.PlayoffSeed)
	if err != nil {
		return TeamStandingRecord{}, fmt.Errorf("team %d: %w", team.ID, err)
	}
	outcome, err := outcomeRecord("outcome_totals", standings.OutcomeTotals)
	if err != nil {
		return TeamStandingRecord{}, fmt.Errorf("team %d: %w", team.ID, err)
	}
	divisional, err := outcomeRecord("divisional_outcome_totals", standings.DivisionalOutcomeTotals)
	if err != nil {
		return TeamStandingRecord{}, fmt.Errorf("team %d: %w", team.ID, err)
	}
	return TeamStandingRecord{
		TeamID: team.ID, TeamKey: team.Key, Name: team.Name,
		Rank: rank, PlayoffSeed: seed, Outcome: outcome, DivisionalOutcome: divisional,
	}, nil
}

// outcomeRecord converts a record element; a nil element gives a nil record.
func outcomeRecord(field string, totals *store.OutcomeTotals) (*OutcomeRecord, error) {
	if totals == nil {
		return nil, nil
	}
	wins, err := optionalCount(field+" wins", totals.Wins)
	if err != nil {
		return nil, err
	}
	losses, err := optionalCount(field+" losses", totals.Losses)
	if err != nil {
		return nil, err
	}
	ties, err := optionalCount(field+" ties", totals.Ties)
	if err != nil {
		return nil, err
	}
	record := &OutcomeRecord{Wins: wins, Losses: losses, Ties: ties}
	if totals.Percentage != "" {
		percentage, err := strconv.ParseFloat(totals.Percentage, 64)
		if err != nil {
			return nil, fmt.Errorf("%s percentage %q is not a number", field, totals.Percentage)
		}
		record.Percentage = &percentage
	}
	return record, nil
}

// optionalCount converts a non-negative integer Yahoo may leave empty (0).
func optionalCount(field, value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s %q is not a non-negative integer", field, value)
	}
	return n, nil
}
