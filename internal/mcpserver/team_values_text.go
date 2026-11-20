package mcpserver

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"

	"github.com/sperano/puckdb/internal/draft"
)

const (
	// Section titles of get_yahoo_team_player_values.
	teamValuesCategoriesSection = "categories"
	teamValuesUnmatchedSection  = "unmatched"
	// maxTeamValuesOutputBytes bounds the result: a team's rows over a
	// season (its roster plus the players it dropped) stay far below it.
	maxTeamValuesOutputBytes = 64 * 1024
	// ratioValueDecimals prints a ratio category's value (2.714, 0.912).
	ratioValueDecimals = 3
	// noTeamValuesProjection is the header's projection without a draft
	// ranking snapshot.
	noTeamValuesProjection = "none"
	// Column suffixes of a category's numerator, denominator, z-score and
	// projected pace.
	numeratorSuffix   = "_num"
	denominatorSuffix = "_den"
	zScoreSuffix      = "_z"
	paceSuffix        = "_pace"
)

// teamValuesSections titles each position type's player section.
var teamValuesSections = map[string]string{skaterPositionType: "skaters", goaliePositionType: "goalies"}

// teamValuesPlayerColumns precede the category columns of a player section.
var teamValuesPlayerColumns = []string{
	"rank", "player_key", "nhl_player_id", "name", "on_roster", "days", "active_days", "games", "value",
}

// teamValuesCategoryRow describes one category's columns.
type teamValuesCategoryRow struct {
	Column       string          `json:"column"`
	StatID       int32           `json:"stat_id"`
	Name         string          `json:"name"`
	PositionType string          `json:"position_type"`
	Direction    draft.Direction `json:"direction"`
	Numerator    string          `json:"numerator"`
	Denominator  string          `json:"denominator"`
	InValue      bool            `json:"in_value"`
}

// teamValuesUnmatchedRow is a team player with no NHL match.
type teamValuesUnmatchedRow struct {
	PlayerKey     string `json:"player_key"`
	YahooPlayerID int32  `json:"yahoo_player_id"`
	Name          string `json:"name"`
	PositionType  string `json:"position_type"`
	OnRoster      bool   `json:"on_roster"`
	Days          int    `json:"days"`
	ActiveDays    int    `json:"active_days"`
}

// text renders the header, the categories, one section per position type
// and the unmatched players.
func (v teamValues) text() (string, error) {
	comparison := comparisonPlayers(v.rows, v.args.to)
	matched, unmatched := v.teamPlayers()
	categories, err := csvSection(teamValuesCategoriesSection, v.categoryRows())
	if err != nil {
		return "", err
	}
	parts := []string{v.header(comparison).String() + "\n", categories}
	for _, positionType := range valuePositionTypes {
		section, err := v.playersText(positionType, valuePlayers(matched[positionType], v.categoriesOf(positionType),
			comparison[positionType], v.projected, v.args.to))
		if err != nil {
			return "", err
		}
		parts = append(parts, section)
	}
	unmatchedText, err := csvSection(teamValuesUnmatchedSection, v.unmatchedRows(unmatched))
	if err != nil {
		return "", err
	}
	return strings.Join(append(parts, unmatchedText), ""), nil
}

func (v teamValues) header(comparison map[string][]windowPlayer) metadataHeader {
	var h metadataHeader
	h.add("league_id", v.league.ID)
	h.add("league_key", v.league.LeagueKey)
	h.add("season", v.league.Season)
	h.add("team_id", v.team.ID)
	h.add("team", v.team.Name)
	h.add(teamValuesFromArg, formatDate(v.args.from))
	h.add(teamValuesToArg, formatDate(v.args.to))
	if v.requestedTo.Valid {
		h.add("requested_to", formatDate(v.requestedTo))
	}
	h.add("latest_roster_date", formatDate(v.latest))
	h.add(activeOnlyArg, v.args.activeOnly)
	h.add("compared_skaters", len(comparison[skaterPositionType]))
	h.add("compared_goalies", len(comparison[goaliePositionType]))
	if v.projection == nil {
		h.add("projection", noTeamValuesProjection)
	} else {
		h.add("projection", v.projection.SnapshotID)
		h.add("projection_as_of", timeCell(v.projection.AsOf))
	}
	if len(v.unsupported) > 0 {
		h.add("unsupported_categories", strings.Join(v.unsupported, cellListSeparator))
	}
	return h
}

func (v teamValues) categoriesOf(positionType string) []valueCategory {
	var out []valueCategory
	for _, c := range v.categories {
		if c.positionType == positionType {
			out = append(out, c)
		}
	}
	return out
}

func (v teamValues) categoryRows() []teamValuesCategoryRow {
	rows := make([]teamValuesCategoryRow, 0, len(v.categories))
	for _, c := range v.categories {
		row := teamValuesCategoryRow{
			Column: c.abbr, StatID: c.statID, Name: c.name, PositionType: c.positionType, Direction: c.direction,
			InValue: c.direction != draft.DirectionUnknown,
		}
		if c.ratio != nil {
			row.Numerator, row.Denominator = string(c.ratio.numerator), string(c.ratio.denominator)
		}
		rows = append(rows, row)
	}
	return rows
}

// playersText writes one position type's rows. Its category columns depend
// on the league, so it cannot use csvSection's struct rows.
func (v teamValues) playersText(positionType string, players []valuedPlayer) (string, error) {
	title := teamValuesSections[positionType]
	if len(players) == 0 {
		return commentPrefix + title + "\n" + noResultsText + "\n", nil
	}
	categories := v.categoriesOf(positionType)
	var buf bytes.Buffer
	buf.WriteString(commentPrefix + title + "\n")
	w := csv.NewWriter(&buf)
	if err := w.Write(v.playerColumns(categories)); err != nil {
		return "", fmt.Errorf("%s: write CSV header: %w", title, err)
	}
	for i, p := range players {
		if err := w.Write(v.playerRecord(p, categories)); err != nil {
			return "", fmt.Errorf("%s: write CSV row %d: %w", title, i, err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", fmt.Errorf("%s: flush CSV: %w", title, err)
	}
	return buf.String(), nil
}

func (v teamValues) playerColumns(categories []valueCategory) []string {
	columns := append([]string{}, teamValuesPlayerColumns...)
	for _, c := range categories {
		columns = append(columns, c.abbr)
		if c.ratio != nil {
			columns = append(columns, c.abbr+numeratorSuffix, c.abbr+denominatorSuffix)
		}
		columns = append(columns, c.abbr+zScoreSuffix)
		if v.projection != nil {
			columns = append(columns, c.abbr+paceSuffix)
		}
	}
	return columns
}

func (v teamValues) playerRecord(p valuedPlayer, categories []valueCategory) []string {
	record := []string{
		positiveInt(p.rank), p.playerKey, strconv.FormatInt(p.nhlPlayerID.Int64, 10), p.name, strconv.FormatBool(p.onRoster),
		strconv.Itoa(p.days), strconv.Itoa(p.activeDays), compactFloat(p.games()), compactFloat(p.value),
	}
	for _, c := range categories {
		record = append(record, categoryValueCell(c, p.totals))
		if c.ratio != nil {
			record = append(record, compactFloat(p.totals[c.ratio.numerator]), compactFloat(p.totals[c.ratio.denominator]))
		}
		record = append(record, optionalFloatCell(p.z, c.statID))
		if v.projection != nil {
			record = append(record, optionalFloatCell(p.pace, c.statID))
		}
	}
	return record
}

// categoryValueCell prints a counting total exactly and a ratio at
// ratioValueDecimals, or "" for a ratio with no denominator.
func categoryValueCell(c valueCategory, t statTotals) string {
	value, ok := c.value(t)
	switch {
	case !ok:
		return ""
	case c.ratio != nil:
		return strconv.FormatFloat(value, 'f', ratioValueDecimals, draftDetailFloatBitSize)
	default:
		return compactFloat(value)
	}
}

func optionalFloatCell(values map[int32]float64, statID int32) string {
	value, ok := values[statID]
	if !ok {
		return ""
	}
	return compactFloat(value)
}

func (v teamValues) unmatchedRows(players []windowPlayer) []teamValuesUnmatchedRow {
	rows := make([]teamValuesUnmatchedRow, 0, len(players))
	for _, p := range players {
		rows = append(rows, teamValuesUnmatchedRow{
			PlayerKey: p.playerKey, YahooPlayerID: p.yahooPlayerID, Name: p.name, PositionType: p.positionType,
			OnRoster: p.lastDate.Time.Equal(v.args.to.Time), Days: p.days, ActiveDays: p.activeDays,
		})
	}
	return rows
}
