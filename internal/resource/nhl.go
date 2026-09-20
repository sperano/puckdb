package resource

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/store"
)

const (
	baseURLAPIWebV1 = "https://api-web.nhle.com/v1"
	baseURLAPIStats = "https://api.nhle.com/stats/rest"
)

// DailySchedule represents the NHL daily schedule for a specific date.
// Note: This does not implement URLResource because the schedule is typically
// fetched via the NHL API client which handles the complex date formatting.
type DailySchedule struct {
	Date time.Time
}

func (d DailySchedule) Path() string {
	return fmt.Sprintf("%s/daily-schedule-%d-%02d-%02d.json",
		gamesDir(d.Date), d.Date.Year(), d.Date.Month(), d.Date.Day())
}

func (d DailySchedule) Type() core.FileType { return core.DailySchedule }

func (d DailySchedule) Parse(data []byte) (*nhl.DailySchedule, error) {
	var schedule nhl.DailySchedule
	if err := json.Unmarshal(data, &schedule); err != nil {
		return nil, fmt.Errorf("parse daily schedule for %s: %w", d.Date.Format("2006-01-02"), err)
	}
	return &schedule, nil
}

func (d DailySchedule) Format(obj *nhl.DailySchedule) ([]byte, error) {
	return json.Marshal(obj)
}

// Boxscore represents an NHL game boxscore.
type Boxscore struct {
	Date   time.Time
	GameID nhl.GameID
}

func (b Boxscore) Path() string {
	return fmt.Sprintf("%s/boxscore-%s.json", gamesDir(b.Date), b.GameID.String())
}

func (b Boxscore) URL() string {
	return fmt.Sprintf("%s/gamecenter/%s/boxscore", baseURLAPIWebV1, b.GameID.String())
}

func (b Boxscore) Type() core.FileType { return core.Boxscore }

func (b Boxscore) Parse(data []byte) (*nhl.Boxscore, error) {
	var boxscore nhl.Boxscore
	if err := json.Unmarshal(data, &boxscore); err != nil {
		return nil, fmt.Errorf("parse boxscore %s: %w", b.GameID, err)
	}
	return &boxscore, nil
}

func (b Boxscore) Format(obj *nhl.Boxscore) ([]byte, error) {
	return json.Marshal(obj)
}

// PlayByPlay represents NHL play-by-play data for a game.
type PlayByPlay struct {
	Date   time.Time
	GameID nhl.GameID
}

func (p PlayByPlay) Path() string {
	return fmt.Sprintf("%s/playbyplay-%s.json", gamesDir(p.Date), p.GameID.String())
}

func (p PlayByPlay) URL() string {
	return fmt.Sprintf("%s/gamecenter/%s/play-by-play", baseURLAPIWebV1, p.GameID.String())
}

func (p PlayByPlay) Type() core.FileType { return core.PlayByPlay }

func (p PlayByPlay) Parse(data []byte) (*nhl.PlayByPlay, error) {
	var pbp nhl.PlayByPlay
	if err := json.Unmarshal(data, &pbp); err != nil {
		return nil, fmt.Errorf("parse play-by-play %s: %w", p.GameID, err)
	}
	return &pbp, nil
}

func (p PlayByPlay) Format(obj *nhl.PlayByPlay) ([]byte, error) {
	return json.Marshal(obj)
}

// ShiftChart represents NHL shift chart data for a game.
type ShiftChart struct {
	Date   time.Time
	GameID nhl.GameID
}

func (s ShiftChart) Path() string {
	return fmt.Sprintf("%s/shiftchart-%s.json", gamesDir(s.Date), s.GameID.String())
}

func (s ShiftChart) URL() string {
	// Shift charts use the stats API with query parameter
	return fmt.Sprintf("%s/en/shiftcharts?cayenneExp=gameId=%s", baseURLAPIStats, s.GameID.String())
}

func (s ShiftChart) Type() core.FileType { return core.ShiftChart }

func (s ShiftChart) Parse(data []byte) (*nhl.ShiftChart, error) {
	var shifts nhl.ShiftChart
	if err := json.Unmarshal(data, &shifts); err != nil {
		return nil, fmt.Errorf("parse shift chart %s: %w", s.GameID, err)
	}
	return &shifts, nil
}

func (s ShiftChart) Format(obj *nhl.ShiftChart) ([]byte, error) {
	return json.Marshal(obj)
}

// GameStory represents an NHL game story/recap.
type GameStory struct {
	Date   time.Time
	GameID nhl.GameID
}

func (g GameStory) Path() string {
	return fmt.Sprintf("%s/gamestory-%s.json", gamesDir(g.Date), g.GameID.String())
}

func (g GameStory) URL() string {
	return fmt.Sprintf("%s/wsc/game-story/%s", baseURLAPIWebV1, g.GameID.String())
}

func (g GameStory) Type() core.FileType { return core.GameStory }

func (g GameStory) Parse(data []byte) (*nhl.GameStory, error) {
	var story nhl.GameStory
	if err := json.Unmarshal(data, &story); err != nil {
		return nil, fmt.Errorf("parse game story %s: %w", g.GameID, err)
	}
	return &story, nil
}

func (g GameStory) Format(obj *nhl.GameStory) ([]byte, error) {
	return json.Marshal(obj)
}

// SeasonSeries represents NHL season series matchup data for a game.
// Contains officials, head coaches, scratches, and head-to-head series records.
type SeasonSeries struct {
	Date   time.Time
	GameID nhl.GameID
}

func (s SeasonSeries) Path() string {
	return fmt.Sprintf("%s/seasonseries-%s.json", gamesDir(s.Date), s.GameID.String())
}

func (s SeasonSeries) Type() core.FileType { return core.SeasonSeries }

func (s SeasonSeries) Parse(data []byte) (*nhl.SeasonSeriesMatchup, error) {
	var matchup nhl.SeasonSeriesMatchup
	if err := json.Unmarshal(data, &matchup); err != nil {
		return nil, fmt.Errorf("parse season series %s: %w", s.GameID, err)
	}
	return &matchup, nil
}

func (s SeasonSeries) Format(obj *nhl.SeasonSeriesMatchup) ([]byte, error) {
	return json.Marshal(obj)
}

// PlayerLanding represents an NHL player's landing page data.
type PlayerLanding struct {
	PlayerID nhl.PlayerID
}

func (p PlayerLanding) Path() string {
	return fmt.Sprintf("players/player-%s-landing.json", p.PlayerID.String())
}

func (p PlayerLanding) URL() string {
	return fmt.Sprintf("%s/player/%s/landing", baseURLAPIWebV1, p.PlayerID.String())
}

func (p PlayerLanding) Type() core.FileType { return core.PlayerLanding }

func (p PlayerLanding) Parse(data []byte) (*nhl.PlayerLanding, error) {
	var landing nhl.PlayerLanding
	if err := json.Unmarshal(data, &landing); err != nil {
		return nil, fmt.Errorf("parse player landing %d: %w", p.PlayerID, err)
	}
	return &landing, nil
}

func (p PlayerLanding) Format(obj *nhl.PlayerLanding) ([]byte, error) {
	return json.Marshal(obj)
}

// MissingPlayerLanding is a marker for player IDs that returned 404.
// This does not implement URLResource since it's just a cache marker.
type MissingPlayerLanding struct {
	PlayerID nhl.PlayerID
}

func (m MissingPlayerLanding) Path() string {
	return fmt.Sprintf("players-missing/player-%s.json", m.PlayerID.String())
}

func (m MissingPlayerLanding) Type() core.FileType { return core.PlayerLanding }

func (m MissingPlayerLanding) Parse(data []byte) (*store.MissingPlayerLandingData, error) {
	var missing store.MissingPlayerLandingData
	if err := json.Unmarshal(data, &missing); err != nil {
		return nil, fmt.Errorf("parse missing player landing %d: %w", m.PlayerID, err)
	}
	return &missing, nil
}

func (m MissingPlayerLanding) Format(obj *store.MissingPlayerLandingData) ([]byte, error) {
	return json.Marshal(obj)
}

// Franchises represents the list of all NHL franchises (singleton resource).
type Franchises struct{}

func (f Franchises) Path() string {
	return "nhl/franchises.json"
}

func (f Franchises) URL() string {
	return fmt.Sprintf("%s/en/franchise", baseURLAPIStats)
}

func (f Franchises) Type() core.FileType { return core.Franchises }

func (f Franchises) Parse(data []byte) (nhl.FranchisesResponse, error) {
	var response nhl.FranchisesResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nhl.FranchisesResponse{}, fmt.Errorf("parse franchises: %w", err)
	}
	return response, nil
}

func (f Franchises) Format(obj nhl.FranchisesResponse) ([]byte, error) {
	return json.Marshal(obj)
}

// SeasonsManifest represents the list of available NHL seasons (singleton resource).
type SeasonsManifest struct{}

func (s SeasonsManifest) Path() string {
	return "nhl/seasons-manifest.json"
}

func (s SeasonsManifest) URL() string {
	return fmt.Sprintf("%s/standings-season", baseURLAPIWebV1)
}

func (s SeasonsManifest) Type() core.FileType { return core.SeasonsManifest }

func (s SeasonsManifest) Parse(data []byte) (nhl.SeasonsResponse, error) {
	var response nhl.SeasonsResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nhl.SeasonsResponse{}, fmt.Errorf("parse seasons manifest: %w", err)
	}
	return response, nil
}

func (s SeasonsManifest) Format(obj nhl.SeasonsResponse) ([]byte, error) {
	return json.Marshal(obj)
}

// SeasonStandings represents NHL standings for a specific season.
// Note: This does not implement URLResource because standings require
// date-based queries handled by the API client.
type SeasonStandings struct {
	Season nhl.Season // Full season identifier (e.g., 20242025), not the start season
}

func (s SeasonStandings) Path() string {
	return fmt.Sprintf("nhl/standings/standings-%d.json", s.Season.ID())
}

func (s SeasonStandings) Type() core.FileType { return core.SeasonStandings }

func (s SeasonStandings) Parse(data []byte) ([]nhl.Standing, error) {
	var standings []nhl.Standing
	if err := json.Unmarshal(data, &standings); err != nil {
		return nil, fmt.Errorf("parse season standings %d: %w", s.Season.ID(), err)
	}
	return standings, nil
}

func (s SeasonStandings) Format(obj []nhl.Standing) ([]byte, error) {
	return json.Marshal(obj)
}

// PlayerGameLog represents a player's game-by-game statistics for a season.
type PlayerGameLog struct {
	PlayerID nhl.PlayerID
	Season   nhl.Season
	GameType int // 2 = regular season, 3 = playoffs // TODO enum
}

func (p PlayerGameLog) Path() string {
	return fmt.Sprintf("/seasons/%d/player-gamelogs/player-%s-%d.json", p.Season.StartYear(), p.PlayerID.String(), p.GameType)
}

func (p PlayerGameLog) URL() string {
	// Season needs to be formatted as "20242025" for the API
	return fmt.Sprintf("%s/player/%s/game-log/%d/%d", baseURLAPIWebV1, p.PlayerID.String(), p.Season.StartYear(), p.GameType)
}

func (p PlayerGameLog) Type() core.FileType { return core.PlayerGameLog }

func (p PlayerGameLog) Parse(data []byte) (*nhl.PlayerGameLog, error) {
	var gameLog nhl.PlayerGameLog
	if err := json.Unmarshal(data, &gameLog); err != nil {
		return nil, fmt.Errorf("parse player game log %d season %d: %w", p.PlayerID, p.Season.StartYear(), err)
	}
	return &gameLog, nil
}

// DailyStandings represents NHL standings for a specific date.
// Fetched via LeagueStandingsForDate and cached alongside daily schedule data.
type DailyStandings struct {
	Date time.Time
}

func (d DailyStandings) Path() string {
	return fmt.Sprintf("%s/standings-%d-%02d-%02d.json",
		gamesDir(d.Date), d.Date.Year(), d.Date.Month(), d.Date.Day())
}

func (d DailyStandings) Type() core.FileType { return core.DailyStandings }

func (d DailyStandings) Parse(data []byte) ([]nhl.Standing, error) {
	var standings []nhl.Standing
	if err := json.Unmarshal(data, &standings); err != nil {
		return nil, fmt.Errorf("parse daily standings for %s: %w", d.Date.Format("2006-01-02"), err)
	}
	return standings, nil
}

func (d DailyStandings) Format(obj []nhl.Standing) ([]byte, error) {
	return json.Marshal(obj)
}

// SeasonRoster represents the full roster for a team in a specific season.
// Fetched via RosterSeason and includes players who never appeared in a game.
type SeasonRoster struct {
	Season     int
	TeamAbbrev string
}

func (r SeasonRoster) Path() string {
	return fmt.Sprintf("seasons/%d/rosters/roster-%s.json", r.Season, r.TeamAbbrev)
}

func (r SeasonRoster) Type() core.FileType { return core.SeasonRoster }

func (r SeasonRoster) Parse(data []byte) (*nhl.Roster, error) {
	var roster nhl.Roster
	if err := json.Unmarshal(data, &roster); err != nil {
		return nil, fmt.Errorf("parse season roster %s/%d: %w", r.TeamAbbrev, r.Season, err)
	}
	return &roster, nil
}

func (r SeasonRoster) Format(obj *nhl.Roster) ([]byte, error) {
	return json.Marshal(obj)
}

// ClubStatsResource represents pre-aggregated team stats per season from the NHL API.
// Includes fields not available from individual boxscores (e.g., shorthanded goals, shooting %).
type ClubStatsResource struct {
	Season     int
	TeamAbbrev string
	GameType   int
}

func (c ClubStatsResource) Path() string {
	return fmt.Sprintf("seasons/%d/clubstats/clubstats-%s-%d.json", c.Season, c.TeamAbbrev, c.GameType)
}

func (c ClubStatsResource) Type() core.FileType { return core.ClubStatsResource }

func (c ClubStatsResource) Parse(data []byte) (*nhl.ClubStats, error) {
	var stats nhl.ClubStats
	if err := json.Unmarshal(data, &stats); err != nil {
		return nil, fmt.Errorf("parse club stats %s/%d/%d: %w", c.TeamAbbrev, c.Season, c.GameType, err)
	}
	return &stats, nil
}

func (c ClubStatsResource) Format(obj *nhl.ClubStats) ([]byte, error) {
	return json.Marshal(obj)
}

// ClubScheduleSeason represents a team's full season schedule from the NHL API.
// Includes preseason, regular season, and playoff games.
type ClubScheduleSeason struct {
	Season     int
	TeamAbbrev string
}

func (c ClubScheduleSeason) Path() string {
	return fmt.Sprintf("seasons/%d/club-schedule/club-schedule-%s.json", c.Season, c.TeamAbbrev)
}

func (c ClubScheduleSeason) Type() core.FileType { return core.ClubScheduleSeasonResource }

func (c ClubScheduleSeason) Parse(data []byte) (*nhl.TeamScheduleResponse, error) {
	var schedule nhl.TeamScheduleResponse
	if err := json.Unmarshal(data, &schedule); err != nil {
		return nil, fmt.Errorf("parse club schedule season %s/%d: %w", c.TeamAbbrev, c.Season, err)
	}
	return &schedule, nil
}

func (c ClubScheduleSeason) Format(obj *nhl.TeamScheduleResponse) ([]byte, error) {
	return json.Marshal(obj)
}
