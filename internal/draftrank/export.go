package draftrank

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// Format is an export format of a rankings page.
type Format string

const (
	FormatTable Format = "table"
	FormatCSV   Format = "csv"
	FormatJSON  Format = "json"
)

// Formats lists the supported formats.
var Formats = []Format{FormatTable, FormatCSV, FormatJSON}

const (
	// positionRankSeparator and listSeparator join multi-valued CSV cells.
	positionRankSeparator = ":"
	listSeparator         = "|"
	// exactFloat formats CSV numbers so they parse back to the same
	// float64 the JSON export and the API return.
	exactFloat    = 'g'
	exactDigits   = -1
	floatBitSize  = 64
	jsonIndent    = "  "
	jsonNoPrefix  = ""
	csvStatPrefix = "stat"
)

// Export writes a page in a format.
func Export(w io.Writer, page Page, format Format) error {
	switch format {
	case FormatCSV:
		return WriteCSV(w, page)
	case FormatJSON:
		return WriteJSON(w, page)
	case FormatTable:
		return WriteTable(w, page)
	}
	return fmt.Errorf("%w: unknown format %q", ErrInvalidQuery, format)
}

// WriteJSON writes the page exactly as the service returned it.
func WriteJSON(w io.Writer, page Page) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent(jsonNoPrefix, jsonIndent)
	return encoder.Encode(page)
}

// WriteCSV writes one line per row. Every line names the snapshot and
// scenario, so rows of different versions cannot be mixed unnoticed, and
// numbers keep full precision.
func WriteCSV(w io.Writer, page Page) error {
	out := csv.NewWriter(w)
	categories := csvCategories(page.League.Categories)
	if err := out.Write(csvHeader(categories)); err != nil {
		return err
	}
	for _, row := range page.Rows {
		if err := out.Write(csvRecord(page, row, categories)); err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}

type csvCategory struct {
	statID int
	label  string
}

// csvCategories names a column pair per scoring stat: the abbreviation, or
// stat<ID> when it is empty or shared by another stat.
func csvCategories(categories []Category) []csvCategory {
	counts := make(map[string]int, len(categories))
	for _, c := range categories {
		counts[strings.ToLower(c.Abbr)]++
	}
	out := make([]csvCategory, 0, len(categories))
	for _, c := range categories {
		label := strings.ToLower(c.Abbr)
		if label == "" || counts[label] > 1 {
			label = csvStatPrefix + strconv.Itoa(c.StatID)
		}
		out = append(out, csvCategory{statID: c.StatID, label: label})
	}
	return out
}

func csvHeader(categories []csvCategory) []string {
	header := []string{
		"snapshot_id", "snapshot_identity", "scenario", "overall_rank", "position_rank", "baseline_rank", "rank_change",
		"player_key", "yahoo_player_id", "name", "team", "positions", "position_ranks", "tier",
		"official_score", "adjusted_score", "replacement_value", "value", "adjusted_value", "uncertainty",
		"status", "injury_note", "news_reasons", "news_alerts",
	}
	for _, c := range categories {
		header = append(header, c.label+"_projected", c.label+"_value")
	}
	return header
}

func csvRecord(page Page, row Row, categories []csvCategory) []string {
	p := row.Placement
	snapshotID, identity := "", ""
	if page.Snapshot != nil {
		snapshotID, identity = page.Snapshot.ID.String(), page.Snapshot.Identity
	}
	reasons, alerts := newsCounts(row.Player)
	record := []string{
		snapshotID, identity, string(page.Scenario), strconv.Itoa(p.OverallRank), strconv.Itoa(row.PositionRank),
		strconv.Itoa(row.BaselineRank), strconv.Itoa(row.RankChange),
		row.PlayerKey, strconv.Itoa(row.YahooPlayerID), row.Name, row.Team,
		strings.Join(row.EligiblePositions, listSeparator), positionRanks(row), strconv.Itoa(p.Tier),
		exact(p.OfficialScore), exact(p.AdjustedScore), exact(p.ReplacementValue), exact(p.Value), exact(p.AdjustedValue), exact(p.Uncertainty),
		row.Status, row.InjuryNote, strconv.Itoa(reasons), strconv.Itoa(alerts),
	}
	for _, c := range categories {
		projected, value := "", ""
		if i := slices.IndexFunc(p.Contributions, func(x Contribution) bool { return x.StatID == c.statID }); i >= 0 {
			projected, value = exact(p.Contributions[i].Projected), exact(p.Contributions[i].Official)
		}
		record = append(record, projected, value)
	}
	return record
}

// positionRanks lists "POS:rank" in the player's eligibility order.
func positionRanks(row Row) string {
	parts := make([]string, 0, len(row.EligiblePositions))
	for _, position := range row.EligiblePositions {
		parts = append(parts, position+positionRankSeparator+strconv.Itoa(row.Placement.PositionRanks[position]))
	}
	return strings.Join(parts, listSeparator)
}

func newsCounts(player Player) (int, int) {
	if player.Adjustment == nil {
		return 0, 0
	}
	return len(player.Adjustment.Reasons), len(player.Adjustment.Alerts)
}

func exact(value float64) string {
	return strconv.FormatFloat(value, exactFloat, exactDigits, floatBitSize)
}
