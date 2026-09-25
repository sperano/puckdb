package store

import (
	"encoding/xml"
	"strings"
)

// ============================================================================
// LEAGUE SETTINGS TYPES
// The <settings> element of the Yahoo league settings resource.
// ============================================================================

// Settings contains league configuration.
type Settings struct {
	XMLName            xml.Name      `xml:"settings"`
	DraftType          string        `xml:"draft_type"`
	IsAuctionDraft     int           `xml:"is_auction_draft"`
	PersistentURL      string        `xml:"persistent_url"`
	UsesPlayoff        int           `xml:"uses_playoff"`
	WaiverType         string        `xml:"waiver_type"`
	WaiverRule         string        `xml:"waiver_rule"`
	DraftTime          FlexTimestamp `xml:"draft_time"`
	DraftPickTime      int           `xml:"draft_pick_time"`
	PostDraftPlayers   string        `xml:"post_draft_players"`
	MaxTeams           int           `xml:"max_teams"`
	WaiverTime         int           `xml:"waiver_time"`
	TradeEndDate       string        `xml:"trade_end_date"`
	TradeRatifyType    string        `xml:"trade_ratify_type"`
	TradeRejectTime    int           `xml:"trade_reject_time"`
	PlayerPool         string        `xml:"player_pool"`
	CantCutList        string        `xml:"cant_cut_list"`
	SendbirdChannelURL string        `xml:"sendbird_channel_url"`
	RosterPositions    RosterPositions
	StatCategories     RosterStatCategories
	// StatModifiers holds the points weights of a points league.
	StatModifiers StatModifiers
	// Other keeps every settings element not modeled above (keeper, playoff,
	// weekly add or games-played rules...) so no Yahoo rule is silently lost.
	Other []RawSetting `xml:",any"`
}

// RawSetting is one settings element as Yahoo sent it.
type RawSetting struct {
	XMLName  xml.Name
	Value    string       `xml:",chardata"`
	Children []RawSetting `xml:",any"`
}

// OtherLeafSettings returns the unmodeled settings that carry a single value,
// keyed by element name, and the names of unmodeled nested elements.
func (s Settings) OtherLeafSettings() (leaves map[string]string, nested []string) {
	leaves = make(map[string]string)
	for _, raw := range s.Other {
		name := raw.XMLName.Local
		if len(raw.Children) > 0 {
			nested = append(nested, name)
			continue
		}
		leaves[name] = strings.TrimSpace(raw.Value)
	}
	return leaves, nested
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
	XMLName     xml.Name `xml:"stat"`
	StatID      int      `xml:"stat_id"`
	Enabled     int      `xml:"enabled"`
	Name        string   `xml:"name"`
	DisplayName string   `xml:"display_name"`
	Group       string   `xml:"group"`
	Abbr        string   `xml:"abbr"`
	// SortOrder is kept verbatim: "1" = higher is better, "0" = lower is
	// better, "" = Yahoo did not say (never read as lower is better).
	SortOrder         string             `xml:"sort_order"`
	PositionType      string             `xml:"position_type"`
	IsOnlyDisplayStat int                `xml:"is_only_display_stat"`
	PositionTypes     []StatPositionType `xml:"stat_position_types>stat_position_type"`
}

// StatPositionType says which player type (P skaters, G goalies) a stat
// applies to, and whether it is only displayed for that type.
type StatPositionType struct {
	PositionType      string `xml:"position_type"`
	IsOnlyDisplayStat int    `xml:"is_only_display_stat"`
}

// StatModifiers contains the per-stat points weights of a points league.
type StatModifiers struct {
	XMLName xml.Name       `xml:"stat_modifiers"`
	Stats   []StatModifier `xml:"stats>stat"`
}

// StatModifier is the points weight of one stat. Value is kept as text so a
// missing weight stays distinguishable from a zero weight.
type StatModifier struct {
	StatID  int         `xml:"stat_id"`
	Value   string      `xml:"value"`
	Bonuses []StatBonus `xml:"bonuses>bonus"`
}

// StatBonus is a threshold bonus attached to a points weight.
type StatBonus struct {
	Target string `xml:"target"`
	Points string `xml:"points"`
}
