package store

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// ////////////////////////////////////////////////////////////////////////////
// YAHOO FANTASY API XML TYPES
// This file contains all XML deserialization types for the Yahoo Fantasy API.
// ////////////////////////////////////////////////////////////////////////////

// ============================================================================
// ROOT TYPES (fantasy_content)
// ============================================================================

// FantasyContent is the root element returned by Yahoo Fantasy API responses.
type FantasyContent struct {
	XMLName xml.Name      `xml:"fantasy_content"`
	Game    FantasyGame   `xml:"game"`
	Games   []FantasyGame `xml:"games>game"`
	League  League        `xml:"league"`
	Team    Team          `xml:"team"`
}

// FantasyGame represents a Yahoo Fantasy game (sport + season combination).
type FantasyGame struct {
	XMLName            xml.Name `xml:"game"`
	ID                 int      `xml:"game_id"`
	Key                int      `xml:"game_key"`
	Name               string   `xml:"name"`
	Code               string   `xml:"code"`
	Type               string   `xml:"type"`
	URL                string   `xml:"url"`
	Season             int      `xml:"season"`
	IsRegistrationOver int      `xml:"is_registration_over"`
	IsGameOver         int      `xml:"is_game_over"`
	IsOffSeason        int      `xml:"is_offseason"`
}

// ============================================================================
// LEAGUE TYPES
// ============================================================================

// League represents a Yahoo Fantasy league.
type League struct {
	XMLName               xml.Name `xml:"league"`
	ID                    int      `xml:"league_id"`
	Key                   string   `xml:"league_key"`
	Name                  string   `xml:"name"`
	URL                   string   `xml:"url"`
	LogoURL               string   `xml:"logo_url"`
	DraftStatus           string   `xml:"draft_status"`
	NumTeams              int      `xml:"num_teams"`
	EditKey               string   `xml:"edit_key"`
	LeagueUpdateTimestamp int64    `xml:"league_update_timestamp"`
	ScoringType           string   `xml:"scoring_type"`
	LeagueType            string   `xml:"league_type"`
	IsProLeague           int      `xml:"is_pro_league"`
	IsCashLeague          int      `xml:"is_cash_league"`
	StartDate             string   `xml:"start_date"`
	EndDate               string   `xml:"end_date"`
	GameCode              string   `xml:"game_code"`
	Season                int      `xml:"season"`
	Settings              Settings
}

// Settings contains league configuration.
type Settings struct {
	XMLName            xml.Name `xml:"settings"`
	DraftType          string   `xml:"draft_type"`
	IsAuctionDraft     int      `xml:"is_auction_draft"`
	PersistentURL      string   `xml:"persistent_url"`
	UsesPlayoff        int      `xml:"uses_playoff"`
	WaiverType         string   `xml:"waiver_type"`
	WaiverRule         string   `xml:"waiver_rule"`
	DraftTime          int      `xml:"draft_time"`
	DraftPickTime      int      `xml:"draft_pick_time"`
	PostDraftPlayers   string   `xml:"post_draft_players"`
	MaxTeams           int      `xml:"max_teams"`
	WaiverTime         int      `xml:"waiver_time"`
	TradeEndDate       string   `xml:"trade_end_date"`
	TradeRatifyType    string   `xml:"trade_ratify_type"`
	TradeRejectTime    int      `xml:"trade_reject_time"`
	PlayerPool         string   `xml:"player_pool"`
	CantCutList        string   `xml:"cant_cut_list"`
	SendbirdChannelURL string   `xml:"sendbird_channel_url"`
	RosterPositions    RosterPositions
	StatCategories     RosterStatCategories
}

// RosterPositions contains the roster position configuration.
type RosterPositions struct {
	XMLName xml.Name         `xml:"roster_positions"`
	Slice   []RosterPosition `xml:"roster_position"`
}

// RosterPosition represents a position slot in a fantasy roster.
type RosterPosition struct {
	XMLName            xml.Name `xml:"roster_position"`
	Position           string   `xml:"position"`
	PositionType       string   `xml:"position_type"`
	Count              int      `xml:"count"`
	IsStartingPosition int      `xml:"is_starting_position"`
}

// RosterStatCategories contains the stat categories configuration.
type RosterStatCategories struct {
	XMLName xml.Name `xml:"stat_categories"`
	Stats   RosterStats
}

// RosterStats contains the list of stat categories.
type RosterStats struct {
	XMLName xml.Name     `xml:"stats"`
	Slice   []RosterStat `xml:"stat"`
}

// RosterStat represents a single stat category configuration.
type RosterStat struct {
	XMLName xml.Name `xml:"stat"`
	StatID  int      `xml:"stat_id"`
	Enabled int      `xml:"enabled"`
	Name    string   `xml:"name"`
	Group   string   `xml:"group"`
	Abbr    string   `xml:"abbr"`
}

// ============================================================================
// TEAM TYPES
// ============================================================================

// Team represents a Yahoo Fantasy team.
type Team struct {
	XMLName               xml.Name `xml:"team"`
	Key                   string   `xml:"team_key"`
	ID                    int      `xml:"team_id"`
	Name                  string   `xml:"name"`
	IsOwnedByCurrentLogin bool     `xml:"is_owned_by_current_login"`
	URL                   string   `xml:"url"`
	TeamLogos             TeamLogos
	WaiverPriority        int    `xml:"waiver_priority"`
	NumberOfMoves         int    `xml:"number_of_moves"`
	NumberOfTrades        int    `xml:"number_of_trades"`
	LeagueScoringType     string `xml:"league_scoring_type"`
	DraftPosition         int    `xml:"draft_position"`
	HasDraftGrade         bool   `xml:"has_draft_grade"`
	Managers              Managers
	Roster                Roster
	TeamStats             TeamStats
}

// TeamLogos contains team logo information.
type TeamLogos struct {
	XMLName xml.Name   `xml:"team_logos"`
	Slice   []TeamLogo `xml:"team_logo"`
}

// TeamLogo represents a single team logo.
type TeamLogo struct {
	XMLName xml.Name `xml:"team_logo"`
	Size    string   `xml:"size"`
	URL     string   `xml:"url"`
}

// Managers contains the team managers.
type Managers struct {
	XMLName xml.Name  `xml:"managers"`
	Slice   []Manager `xml:"manager"`
}

// Manager represents a team manager.
type Manager struct {
	XMLName        xml.Name `xml:"manager"`
	ID             int      `xml:"manager_id"`
	Nickname       string   `xml:"nickname"`
	GUID           string   `xml:"guid"`
	IsCurrentLogin bool     `xml:"is_current_login"`
	EMail          string   `xml:"email"`
	ImageURL       string   `xml:"image_url"`
	FeloScore      int      `xml:"felo_score"`
	FeloTier       string   `xml:"felo_tier"`
}

// Roster contains the team roster.
type Roster struct {
	XMLName      xml.Name `xml:"roster"`
	CoverageType string   `xml:"coverage_type"`
	Date         string   `xml:"date"`
	IsEditable   bool     `xml:"is_editable"`
	Players      Players
}

// TeamStats contains the team's stats.
type TeamStats struct {
	XMLName      xml.Name `xml:"team_stats"`
	CoverageType string   `xml:"coverage_type"`
	Date         string   `xml:"date"`
	Stats        Stats    `xml:"stats"`
}

// Stats contains a list of stat values.
type Stats struct {
	XMLName xml.Name `xml:"stats"`
	Slice   []Stat   `xml:"stat"`
}

// Stat represents a single stat value.
type Stat struct {
	XMLName xml.Name `xml:"stat"`
	StatID  string   `xml:"stat_id"`
	Value   string   `xml:"value"`
}

// ============================================================================
// PLAYER TYPES
// ============================================================================

// Players contains a list of players.
type Players struct {
	XMLName xml.Name `xml:"players"`
	Slice   []Player `xml:"player"`
	Count   int      `xml:"count,attr"`
}

// Player represents a Yahoo Fantasy player.
type Player struct {
	XMLName                  xml.Name `xml:"player"`
	Key                      string   `xml:"player_key"`
	ID                       int      `xml:"player_id"`
	Name                     PlayerName
	EditorialPlayerKey       string    `xml:"editorial_player_key"`
	EditorialTeamKey         nhlTeamID `xml:"editorial_team_key"`
	EditorialTeamFullName    string    `xml:"editorial_team_full_name"`
	EditorialTeamAbbr        string    `xml:"editorial_team_abbr"`
	UniformNumber            int       `xml:"uniform_number"`
	DisplayPosition          string    `xml:"display_position"`
	Headshot                 PlayerHeadshot
	ImageURL                 string   `xml:"image_url"`
	IsUndroppable            int      `xml:"is_undroppable"`
	PositionType             string   `xml:"position_type"`
	PrimaryPosition          string   `xml:"primary_position"`
	EligiblePositions        []string `xml:"eligible_positions>position"`
	HasPlayerNotes           int      `xml:"has_player_notes"`
	PlayerNotesLastTimestamp int      `xml:"player_notes_last_timestamp"`
	SelectedPosition         PlayerSelectedPosition
	IsEditable               int `xml:"is_editable"`
}

// PlayerName represents a player's name information.
type PlayerName struct {
	XMLName    xml.Name `xml:"name"`
	Full       string   `xml:"full"`
	First      string   `xml:"first"`
	Last       string   `xml:"last"`
	ASCIIFirst string   `xml:"ascii_first"`
	ASCIILast  string   `xml:"ascii_last"`
}

// Validate checks that the player name fields are consistent.
func (n *PlayerName) Validate() error {
	full := fmt.Sprintf("%s %s", n.First, n.Last)
	if full != n.Full {
		return &ErrInvalidFullName{n.Full, n.First, n.Last}
	}
	if n.First != n.ASCIIFirst {
		return &ErrASCIIFirstName{n.First, n.ASCIIFirst}
	}
	if n.Last != n.ASCIILast {
		return &ErrASCIILastName{n.Last, n.ASCIILast}
	}
	return nil
}

// PlayerHeadshot contains player headshot image info.
type PlayerHeadshot struct {
	XMLName xml.Name `xml:"headshot"`
	URL     string   `xml:"url"`
	Size    string   `xml:"size"`
}

// PlayerSelectedPosition represents the player's selected roster position.
type PlayerSelectedPosition struct {
	XMLName      xml.Name `xml:"selected_position"`
	CoverageType string   `xml:"coverage_type"`
	Date         string   `xml:"date"`
	Position     string   `xml:"position"`
	IsFlex       int      `xml:"is_flex"`
}

// ============================================================================
// VALIDATION ERRORS
// ============================================================================

// ErrInvalidFullName is returned when the full name doesn't match first + last.
type ErrInvalidFullName struct {
	Full  string
	First string
	Last  string
}

func (e *ErrInvalidFullName) Error() string {
	return fmt.Sprintf("\"%s\" != \"%s\" + \" \" + \"%s\"", e.Full, e.First, e.Last)
}

// ErrASCIIFirstName is returned when the first name doesn't match its ASCII version.
type ErrASCIIFirstName struct {
	Name  string
	ASCII string
}

func (e *ErrASCIIFirstName) Error() string {
	return fmt.Sprintf("error ASCII First Name \"%s\" != \"%s\"", e.Name, e.ASCII)
}

// ErrASCIILastName is returned when the last name doesn't match its ASCII version.
type ErrASCIILastName struct {
	Name  string
	ASCII string
}

func (e *ErrASCIILastName) Error() string {
	return fmt.Sprintf("error ASCII Last Name \"%s\" != \"%s\"", e.Name, e.ASCII)
}

// ============================================================================
// INTERNAL ID TYPES
// ============================================================================

// playerID represents a Yahoo Fantasy player ID string (format: "game_key.player.id").
type playerID string

func (p playerID) ID() (uint, error) {
	tokens := strings.Split(string(p), ".")
	if len(tokens) != 3 {
		return 0, fmt.Errorf("invalid playerID: %s", p)
	}
	id, err := strconv.Atoi(tokens[2])
	if err != nil {
		return 0, fmt.Errorf("invalid playerID: %s", p)
	}
	return uint(id), nil
}

// positionID represents a Yahoo Fantasy position ID string (format: "game_key.position.id").
type positionID string

func (p positionID) ID() (uint, error) {
	tokens := strings.Split(string(p), ".")
	if len(tokens) != 3 {
		return 0, fmt.Errorf("invalid positionID: %s", p)
	}
	id, err := strconv.Atoi(tokens[2])
	if err != nil {
		return 0, fmt.Errorf("invalid positionID: %s", p)
	}
	return uint(id), err
}

// nhlTeamID represents a Yahoo Fantasy NHL team ID string (format: "game_key.team.id").
type nhlTeamID string

func (t nhlTeamID) ID() (uint, error) {
	tokens := strings.Split(string(t), ".")
	if len(tokens) != 3 {
		return 0, fmt.Errorf("invalid nhlTeamID: %s", t)
	}
	id, err := strconv.Atoi(tokens[2])
	if err != nil {
		return 0, fmt.Errorf("invalid nhlTeamID: %s", t)
	}
	return uint(id), err
}
