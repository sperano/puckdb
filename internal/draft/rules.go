// Package draft holds the league-rule, player-pool and roster models the
// draft helper builds on. Rules are normalized from the Yahoo league settings
// resource and versioned in yahoo_league_rule_snapshots; nothing here falls
// back to a default league (such as the Crapettes categories the pool
// simulator hard-codes) when Yahoo did not provide a rule.
package draft

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sperano/puckdb/internal/store"
)

// Source says where a rules snapshot's settings came from.
type Source string

const (
	// SourceYahooAPI rules were read from the league's own Yahoo settings.
	SourceYahooAPI Source = "yahoo_api"
	// SourceTemporaryStandIn rules were copied from an earlier season's
	// league while Yahoo refuses the real one (config.LeagueMetadataSource).
	SourceTemporaryStandIn Source = "temporary_stand_in"
)

// Direction says which way a category ranks.
type Direction string

const (
	HigherIsBetter Direction = "higher"
	LowerIsBetter  Direction = "lower"
	// DirectionUnknown means Yahoo did not say; it is never guessed.
	DirectionUnknown Direction = ""
)

// Yahoo sort_order values.
const (
	yahooSortOrderHigher = "1"
	yahooSortOrderLower  = "0"
)

// leagueKeySeparator splits "<game_key>.l.<league_id>".
const leagueKeySeparator = ".l."

// Settings that are URLs or chat handles, not rules; they are left out of
// the rules so they cannot create new rule versions.
var nonRuleSettings = map[string]bool{
	"persistent_url":       true,
	"sendbird_channel_url": true,
}

// StatCategory is one stat of the league's scoring configuration.
type StatCategory struct {
	StatID      int    `json:"statId"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Abbr        string `json:"abbr"`
	Group       string `json:"group,omitempty"`
	// PositionTypes lists the player types (P skaters, G goalies) the stat
	// scores for; display-only position types are left out.
	PositionTypes []string  `json:"positionTypes,omitempty"`
	Direction     Direction `json:"direction,omitempty"`
	// Weight is the points value of one unit in a points league; nil when
	// Yahoo sent no stat modifier for the stat.
	Weight  *float64 `json:"weight,omitempty"`
	Bonuses []Bonus  `json:"bonuses,omitempty"`
	Enabled bool     `json:"enabled"`
	// DisplayOnly stats are shown by Yahoo but never scored.
	DisplayOnly bool `json:"displayOnly,omitempty"`
}

// Bonus is a threshold bonus on a points weight, kept verbatim.
type Bonus struct {
	Target string `json:"target"`
	Points string `json:"points"`
}

// Scores reports whether the category takes part in scoring.
func (c StatCategory) Scores() bool {
	return c.Enabled && !c.DisplayOnly
}

// Label is the category's short name for reports.
func (c StatCategory) Label() string {
	if c.Abbr != "" {
		return c.Abbr
	}
	return c.Name
}

// RosterSlot is one roster position and how many of it a team has.
type RosterSlot struct {
	Position     string `json:"position"`
	PositionType string `json:"positionType,omitempty"`
	Count        int    `json:"count"`
	Starting     bool   `json:"starting"`
}

// DraftRules describes the league's draft.
type DraftRules struct {
	Type            string     `json:"type"`
	Auction         bool       `json:"auction"`
	Status          string     `json:"status"`
	Time            *time.Time `json:"time,omitempty"`
	PickTimeSeconds int        `json:"pickTimeSeconds,omitempty"`
}

// Rules is one league's normalized rules. It holds only what Yahoo reported:
// every settings element the model does not name is kept in Settings (or,
// when nested, listed in UnmodeledSettings) so it stays visible.
type Rules struct {
	LeagueKey   string         `json:"leagueKey"`
	GameKey     int            `json:"gameKey,omitempty"`
	LeagueID    int            `json:"leagueId"`
	Season      int            `json:"season"`
	Name        string         `json:"name"`
	ScoringType string         `json:"scoringType"`
	NumTeams    int            `json:"numTeams"`
	Categories  []StatCategory `json:"categories"`
	RosterSlots []RosterSlot   `json:"rosterSlots"`
	Draft       DraftRules     `json:"draft"`
	// Settings holds every other single-valued settings element verbatim.
	Settings map[string]string `json:"settings,omitempty"`
	// UnmodeledSettings names nested settings elements the model ignores.
	UnmodeledSettings []string `json:"unmodeledSettings,omitempty"`
	// Issues records values Yahoo sent that could not be read.
	Issues []string `json:"issues,omitempty"`
}

// FromLeague normalizes a parsed Yahoo league settings resource.
func FromLeague(league store.League) Rules {
	settings, nested := leagueSettings(league.Settings)
	rules := Rules{
		LeagueKey:         league.Key,
		GameKey:           GameKeyOf(league.Key),
		LeagueID:          league.ID,
		Season:            league.Season,
		Name:              league.Name,
		ScoringType:       league.ScoringType,
		NumTeams:          league.NumTeams,
		RosterSlots:       rosterSlots(league.Settings.RosterPositions.Slice),
		Draft:             draftRules(league),
		Settings:          settings,
		UnmodeledSettings: nested,
	}
	rules.Categories, rules.Issues = statCategories(league.Settings)
	return rules
}

// GameKeyOf returns the Yahoo game key of a "<game_key>.l.<league_id>"
// league key, or 0 when the key has no numeric game key (a stand-in).
func GameKeyOf(leagueKey string) int {
	prefix, _, found := strings.Cut(leagueKey, leagueKeySeparator)
	if !found {
		return 0
	}
	gameKey, err := strconv.Atoi(prefix)
	if err != nil {
		return 0
	}
	return gameKey
}

// Hash returns the rules' canonical JSON and its SHA-256, which identifies
// the rules version.
func (r Rules) Hash() (string, []byte, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return "", nil, fmt.Errorf("encode rules for league %s: %w", r.LeagueKey, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), data, nil
}

func statCategories(settings store.Settings) ([]StatCategory, []string) {
	weights, bonuses, issues := statModifiers(settings.StatModifiers)
	stats := settings.StatCategories.Stats.Slice
	categories := make([]StatCategory, 0, len(stats))
	for _, stat := range stats {
		category := StatCategory{
			StatID: stat.StatID, Name: stat.Name, DisplayName: stat.DisplayName, Abbr: stat.Abbr,
			Group: stat.Group, Direction: direction(stat.SortOrder), Enabled: stat.Enabled == 1,
			Weight: weights[stat.StatID], Bonuses: bonuses[stat.StatID],
		}
		category.PositionTypes, category.DisplayOnly = scoringPositionTypes(stat)
		if category.Direction == DirectionUnknown && stat.SortOrder != "" {
			issues = append(issues, fmt.Sprintf("stat %d (%s) has unknown sort_order %q", stat.StatID, stat.Abbr, stat.SortOrder))
		}
		categories = append(categories, category)
	}
	slices.SortFunc(categories, func(a, b StatCategory) int { return a.StatID - b.StatID })
	return categories, issues
}

func statModifiers(modifiers store.StatModifiers) (map[int]*float64, map[int][]Bonus, []string) {
	weights := make(map[int]*float64)
	bonuses := make(map[int][]Bonus)
	var issues []string
	for _, modifier := range modifiers.Stats {
		weight, err := strconv.ParseFloat(strings.TrimSpace(modifier.Value), 64)
		if err != nil {
			issues = append(issues, fmt.Sprintf("stat %d has unreadable points weight %q", modifier.StatID, modifier.Value))
			continue
		}
		weights[modifier.StatID] = &weight
		for _, bonus := range modifier.Bonuses {
			bonuses[modifier.StatID] = append(bonuses[modifier.StatID], Bonus{Target: bonus.Target, Points: bonus.Points})
		}
	}
	return weights, bonuses, issues
}

func direction(sortOrder string) Direction {
	switch strings.TrimSpace(sortOrder) {
	case yahooSortOrderHigher:
		return HigherIsBetter
	case yahooSortOrderLower:
		return LowerIsBetter
	default:
		return DirectionUnknown
	}
}

// scoringPositionTypes returns the position types a stat scores for and
// whether it is display-only everywhere: Yahoo marks display-only stats
// either on the stat itself or per position type.
func scoringPositionTypes(stat store.RosterStat) ([]string, bool) {
	if stat.IsOnlyDisplayStat == 1 {
		return nil, true
	}
	if len(stat.PositionTypes) == 0 {
		if stat.PositionType == "" {
			return nil, false
		}
		return []string{stat.PositionType}, false
	}
	var scoring []string
	for _, positionType := range stat.PositionTypes {
		if positionType.IsOnlyDisplayStat != 1 {
			scoring = append(scoring, positionType.PositionType)
		}
	}
	return scoring, len(scoring) == 0
}

func rosterSlots(positions []store.RosterPosition) []RosterSlot {
	slots := make([]RosterSlot, 0, len(positions))
	for _, position := range positions {
		slots = append(slots, RosterSlot{
			Position: position.Position, PositionType: position.PositionType,
			Count: position.Count, Starting: position.IsStartingPosition == 1,
		})
	}
	return slots
}

func draftRules(league store.League) DraftRules {
	rules := DraftRules{
		Type:            league.Settings.DraftType,
		Auction:         league.Settings.IsAuctionDraft == 1,
		Status:          league.DraftStatus,
		PickTimeSeconds: league.Settings.DraftPickTime,
	}
	if league.Settings.DraftTime > 0 {
		draftTime := time.Unix(int64(league.Settings.DraftTime), 0).UTC()
		rules.Time = &draftTime
	}
	return rules
}

// leagueSettings flattens the settings the model does not structure into a
// map: the scalar fields store.Settings names plus every other leaf element.
// Named fields Yahoo left empty (or zero, which the XML decoder cannot tell
// apart from absent) are omitted rather than reported as a rule.
func leagueSettings(settings store.Settings) (map[string]string, []string) {
	values, nested := settings.OtherLeafSettings()
	named := map[string]string{
		"uses_playoff":       strconv.Itoa(settings.UsesPlayoff),
		"waiver_type":        settings.WaiverType,
		"waiver_rule":        settings.WaiverRule,
		"waiver_time":        positiveInt(settings.WaiverTime),
		"post_draft_players": settings.PostDraftPlayers,
		"max_teams":          positiveInt(settings.MaxTeams),
		"trade_end_date":     settings.TradeEndDate,
		"trade_ratify_type":  settings.TradeRatifyType,
		"trade_reject_time":  positiveInt(settings.TradeRejectTime),
		"player_pool":        settings.PlayerPool,
		"cant_cut_list":      settings.CantCutList,
	}
	for key, value := range named {
		if value != "" {
			values[key] = value
		}
	}
	for key := range nonRuleSettings {
		delete(values, key)
	}
	slices.Sort(nested)
	return values, nested
}

func positiveInt(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}
