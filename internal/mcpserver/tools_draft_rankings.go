package mcpserver

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sperano/puckdb/internal/draftrank"
)

const (
	draftRankingsToolName = "get_draft_rankings"
	draftScenarioArg      = "scenario"
	draftPositionsArg     = "positions"
	draftSearchArg        = "search"
	draftSortArg          = "sort"
	draftDirectionArg     = "direction"

	// maxDraftRankingsLimit is the most rows one call returns, and the
	// default: a whole draftable pool (838 players in a 12-team league)
	// fits in one call.
	maxDraftRankingsLimit = 1500
	// maxDraftRankingsOutputBytes refuses a response too large for an
	// agent's context; the caller pages with offset, limit and snapshot_id.
	maxDraftRankingsOutputBytes = 256 * 1024
	// draftRankingsSignificantDigits rounds every number of the players
	// section of magnitude 1 or more, keeping every integer digit (245, 31,
	// 7.1, 2.7); draftRankingsFractionDigits rounds a smaller one, so a
	// save percentage or an uncertainty keeps its resolution (0.912).
	// get_draft_player_detail has the exact values.
	draftRankingsSignificantDigits = 2
	draftRankingsFractionDigits    = 3
	// draftRankingsMaxDecimals caps the decimals of a value close to zero;
	// a nonzero value that would round to 0 at that cap is printed with
	// draftRankingsTinyDigits significant digits in exponent form (1e-05),
	// so it never reads as an exact zero.
	draftRankingsMaxDecimals = 4
	draftRankingsTinyDigits  = 1

	draftCategoriesSection  = "categories"
	draftRosterSlotsSection = "roster_slots"
	draftPlayersSection     = "players"
)

var (
	draftOffsetParam = intArg{name: "offset", min: 0, max: math.MaxInt32}
	draftLimitParam  = intArg{name: "limit", min: minimumPositiveInteger, max: maxDraftRankingsLimit}

	draftScenarioValues  = stringValues(draftrank.Scenarios)
	draftSortValues      = stringValues(draftSortFields)
	draftDirectionValues = []string{string(draftrank.SortAscending), string(draftrank.SortDescending)}
	draftSortFields      = []draftrank.SortField{
		draftrank.SortOverallRank, draftrank.SortPositionRank, draftrank.SortName, draftrank.SortTeam,
		draftrank.SortScore, draftrank.SortValue, draftrank.SortAdjustedValue, draftrank.SortUncertainty,
		draftrank.SortTier, draftrank.SortBaselineRank, draftrank.SortRankChange,
	}
	// draftPlayerColumns precede one projected-total column per category.
	draftPlayerColumns = []string{
		"rank", "player_key", "name", "team", "positions", "pos_rank", "tier", "value", "uncertainty",
		"rank_change", "status", "injury_note",
	}
)

// draftRankingsSource is the read path get_draft_rankings and
// get_draft_player_detail share with the GraphQL draftRankings query and
// `puckdb draft rankings`; *draftrank.Service satisfies it.
type draftRankingsSource interface {
	leagueKeyResolver
	Rankings(context.Context, draftrank.LeagueRef, uuid.UUID, draftrank.Query) (draftrank.Page, error)
}

func draftRankingsTool(source draftRankingsSource, guard leagueGuard) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(draftRankingsToolName,
			mcp.WithDescription(fmt.Sprintf("Get a league's stored draft ranking as one compact table: by default the whole pool "+
				"(up to %d players) in one call. Prints a '# key=value' header once (league, rules format, provisional, rules_source, "+
				"status, snapshot id and as-of, latest_snapshot_id, scenario served, total, offset, rows, next_offset when more rows remain), "+
				"then CSV sections: '# categories' (each projected column's stat), '# roster_slots', '# issues' (stale snapshot, "+
				"stand-in or provisional rules, failing news sources, scenario fallback, ...), and '# players': one row per player with "+
				"overall rank, player key, name, team, positions, best position rank among the filtered (else eligible) positions, tier, "+
				"value (the adjusted value the ranking is ordered by, sort=adjusted_value), uncertainty, rank change vs. the baseline (positive: news moved the player up), "+
				"Yahoo status and injury note, and the projected season total per category (blank when the category does not apply). "+
				"Player numbers are rounded to %d significant digits (%d below 1) with every integer digit kept, a nonzero value too small for that in exponent form (1e-05); category weights are exact. get_draft_player_detail gives a few players' exact values, contributions "+
				"and explanations. Ranks always come from the whole pool: filters never renumber them. "+
				"To page, pass the snapshot_id of the first call so a refresh in between cannot mix two snapshots.",
				maxDraftRankingsLimit, draftRankingsSignificantDigits, draftRankingsFractionDigits)),
			mcp.WithString(leagueArg, mcp.Required(), mcp.Description("Yahoo league key (465.l.1001) or numeric ID (1001); an ID is resolved within season")),
			mcp.WithNumber(seasonYearParam.name, mcp.Required(), mcp.Description("Season as a start year (2025) or a season ID (20252026)")),
			mcp.WithString(draftSnapshotIDArg, mcp.Description("Immutable ranking snapshot UUID from a previous call's header; omit for the latest snapshot")),
			mcp.WithString(draftScenarioArg, mcp.Enum(draftScenarioValues...),
				mcp.Description("Ranking scenario; default: base when the snapshot has news scenarios, else baseline")),
			mcp.WithArray(draftPositionsArg, mcp.WithStringEnumItems(draftrank.ViewPositions),
				mcp.Description("Only players eligible at any of these positions; omit for every player")),
			mcp.WithString(draftSearchArg, mcp.Description("Words that must all appear in the name, team or player key (accents ignored)")),
			mcp.WithString(draftSortArg, mcp.Enum(draftSortValues...), mcp.Description("Sort field; default overall_rank")),
			mcp.WithString(draftDirectionArg, mcp.Enum(draftDirectionValues...),
				mcp.Description("Sort direction; default: the field's natural one (ranks ascending, values descending)")),
			mcp.WithNumber(draftOffsetParam.name, mcp.Description("Rows to skip (default 0)")),
			mcp.WithNumber(draftLimitParam.name, mcp.Description(fmt.Sprintf("Max rows (default and max %d)", maxDraftRankingsLimit))),
		),
		Handler: guard.scopedKey(source, func(ctx context.Context, req mcp.CallToolRequest, ref draftrank.LeagueRef) (*mcp.CallToolResult, error) {
			snapshotID, errResult := optionalDraftSnapshotID(req)
			if errResult != nil {
				return errResult, nil
			}
			query, errResult := draftRankingsQuery(req)
			if errResult != nil {
				return errResult, nil
			}
			page, err := source.Rankings(ctx, ref, snapshotID, query)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return draftRankingsResult(page)
		}),
	}
}

// draftRankingsQuery reads the view arguments. Their values (scenario,
// positions, sort, direction) are validated by the service, the same way
// for every consumer, before any read.
func draftRankingsQuery(req mcp.CallToolRequest) (draftrank.Query, *mcp.CallToolResult) {
	var q draftrank.Query
	for _, arg := range []struct {
		name string
		dest func(string)
	}{
		{draftScenarioArg, func(v string) { q.Scenario = draftrank.Scenario(strings.ToLower(v)) }},
		{draftSearchArg, func(v string) { q.Search = v }},
		{draftSortArg, func(v string) { q.Sort = draftrank.SortField(strings.ToLower(v)) }},
		{draftDirectionArg, func(v string) { q.Direction = draftrank.SortDirection(strings.ToLower(v)) }},
	} {
		value, errResult := optionalString(req, arg.name)
		if errResult != nil {
			return q, errResult
		}
		arg.dest(strings.TrimSpace(value))
	}
	positions, errResult := optionalStrings(req, draftPositionsArg)
	if errResult != nil {
		return q, errResult
	}
	q.Positions = positions
	q.Offset, q.Limit, errResult = draftRankingsPaging(req)
	return q, errResult
}

// draftRankingsPaging reads offset and limit; limit defaults to the max.
func draftRankingsPaging(req mcp.CallToolRequest) (offset, limit int, errResult *mcp.CallToolResult) {
	rawOffset, _, errResult := optionalInt[int64](req, draftOffsetParam)
	if errResult != nil {
		return 0, 0, errResult
	}
	rawLimit, given, errResult := optionalInt[int64](req, draftLimitParam)
	if errResult != nil {
		return 0, 0, errResult
	}
	if !given {
		rawLimit = maxDraftRankingsLimit
	}
	return int(rawOffset), int(rawLimit), nil
}

// optionalStrings reads an optional array of strings; absent or null is nil.
func optionalStrings(req mcp.CallToolRequest, name string) ([]string, *mcp.CallToolResult) {
	raw, present := argument(req, name)
	if !present {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, mcp.NewToolResultError(fmt.Sprintf("invalid %s %s: want an array of strings", name, formatRaw(raw)))
	}
	values := make([]string, len(items))
	for i, item := range items {
		value, isString := item.(string)
		if !isString {
			return nil, mcp.NewToolResultError(fmt.Sprintf("invalid %s[%d] %s: want a string", name, i, formatRaw(item)))
		}
		values[i] = value
	}
	return values, nil
}

// draftRankingsResult renders the whole page before checking its size: the
// rows are already capped at maxDraftRankingsLimit, which bounds that work.
// Raising the limit would call for an estimate before rendering.
func draftRankingsResult(page draftrank.Page) (*mcp.CallToolResult, error) {
	text, err := draftRankingsText(page)
	if err != nil {
		return nil, err
	}
	if len(text) > maxDraftRankingsOutputBytes {
		return mcp.NewToolResultError(fmt.Sprintf(
			"draft rankings result is %d bytes; maximum is %d: lower limit and page with offset and snapshot_id",
			len(text), maxDraftRankingsOutputBytes)), nil
	}
	return mcp.NewToolResultText(text), nil
}

// draftCategoryRow names one projected-total column of the players section.
type draftCategoryRow struct {
	Column        string `json:"column"`
	StatID        int    `json:"stat_id"`
	Abbr          string `json:"abbr"`
	Name          string `json:"name"`
	PositionTypes string `json:"position_types"`
	Direction     string `json:"direction"`
	Weight        string `json:"weight"`
}

type draftRosterSlotRow struct {
	Position     string `json:"position"`
	PositionType string `json:"position_type"`
	Count        int    `json:"count"`
	Starting     bool   `json:"starting"`
}

// draftRankingsText renders the header, then the small sections, then the
// players, so the metadata an agent needs comes before the long table.
func draftRankingsText(page draftrank.Page) (string, error) {
	labels := draftrank.CategoryLabels(page.League.Categories)
	categories := make([]draftCategoryRow, len(page.League.Categories))
	for i, c := range page.League.Categories {
		categories[i] = draftCategoryRow{
			Column: labels[i], StatID: c.StatID, Abbr: c.Abbr, Name: c.Name, PositionTypes: joinCell(c.PositionTypes),
			// A weight is a league rule, not a projection: printed exactly.
			Direction: c.Direction, Weight: draftDetailFloat(c.Weight),
		}
	}
	slots := make([]draftRosterSlotRow, len(page.League.RosterSlots))
	for i, s := range page.League.RosterSlots {
		slots[i] = draftRosterSlotRow{Position: s.Position, PositionType: s.PositionType, Count: s.Count, Starting: s.Starting}
	}
	parts := []string{draftRankingsHeader(page) + "\n"}
	for _, section := range []struct {
		title string
		rows  any
	}{
		{draftCategoriesSection, categories}, {draftRosterSlotsSection, slots}, {draftIssuesSection, draftDetailIssues(page.Issues)},
	} {
		text, err := csvSection(section.title, section.rows)
		if err != nil {
			return "", err
		}
		parts = append(parts, text)
	}
	players, err := draftPlayersText(page, labels)
	if err != nil {
		return "", err
	}
	return strings.Join(append(parts, players), ""), nil
}

func draftRankingsHeader(page draftrank.Page) string {
	var snapshotID, snapshotAsOf, scenarios, latest string
	if page.Snapshot != nil {
		snapshotID, snapshotAsOf = page.Snapshot.ID.String(), timeCell(page.Snapshot.AsOf)
		scenarios = joinCell(page.Snapshot.Scenarios)
	}
	if page.LatestSnapshotID != uuid.Nil {
		latest = page.LatestSnapshotID.String()
	}
	l := page.League
	var h metadataHeader
	for _, field := range []struct {
		key   string
		value any
	}{
		{"league_key", l.LeagueKey}, {"league_id", l.LeagueID}, {"season", l.Season}, {"league", l.Name},
		{"num_teams", l.NumTeams}, {"scoring_type", l.ScoringType}, {"format", l.Format}, {"provisional", l.Provisional},
		{"rules_source", l.RulesSource}, {"status", page.Status}, {"snapshot_id", snapshotID}, {"snapshot_as_of", snapshotAsOf},
		{"latest_snapshot_id", latest}, {"scenario", page.Scenario}, {"scenarios", scenarios}, {"total", page.Total},
		{"offset", page.Offset}, {"rows", len(page.Rows)}, {"next_offset", nextOffset(page)}, {"issues", len(page.Issues)},
	} {
		h.add(field.key, field.value)
	}
	return h.String()
}

// nextOffset is the offset of the next page, or "" on the last one.
func nextOffset(page draftrank.Page) string {
	next := page.Offset + len(page.Rows)
	if len(page.Rows) == 0 || next >= page.Total {
		return ""
	}
	return strconv.Itoa(next)
}

// draftPlayersText writes one CSV row per player. Its category columns
// depend on the league, so it cannot use csvSection's struct rows.
func draftPlayersText(page draftrank.Page, labels []string) (string, error) {
	if len(page.Rows) == 0 {
		return commentPrefix + draftPlayersSection + "\n" + noResultsText + "\n", nil
	}
	var buf bytes.Buffer
	buf.WriteString(commentPrefix + draftPlayersSection + "\n")
	w := csv.NewWriter(&buf)
	if err := w.Write(append(append([]string{}, draftPlayerColumns...), labels...)); err != nil {
		return "", fmt.Errorf("%s: write CSV header: %w", draftPlayersSection, err)
	}
	for i, row := range page.Rows {
		if err := w.Write(draftPlayerRecord(row, page.League.Categories)); err != nil {
			return "", fmt.Errorf("%s: write CSV row %d: %w", draftPlayersSection, i, err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", fmt.Errorf("%s: flush CSV: %w", draftPlayersSection, err)
	}
	return buf.String(), nil
}

func draftPlayerRecord(row draftrank.Row, categories []draftrank.Category) []string {
	p := row.Placement
	record := []string{
		strconv.Itoa(p.OverallRank), row.PlayerKey, row.Name, row.Team, joinCell(row.EligiblePositions),
		positiveInt(row.PositionRank), positiveInt(p.Tier), compactFloat(p.AdjustedValue), compactFloat(p.Uncertainty),
		strconv.Itoa(row.RankChange), row.Status, row.InjuryNote,
	}
	projected := make(map[int]float64, len(p.Contributions))
	for _, c := range p.Contributions {
		projected[c.StatID] = c.Projected
	}
	for _, c := range categories {
		cell := ""
		if value, ok := projected[c.StatID]; ok {
			cell = compactFloat(value)
		}
		record = append(record, cell)
	}
	return record
}

// positiveInt prints a rank or tier, or "" when it is unset.
func positiveInt(value int) string {
	if value <= 0 {
		return ""
	}
	return strconv.Itoa(value)
}

// compactFloat rounds value to draftRankingsSignificantDigits significant
// digits (draftRankingsFractionDigits below 1), keeping every integer
// digit, and drops trailing zeros. Zero, negative zero included, is "0".
func compactFloat(value float64) string {
	if value == 0 {
		return "0"
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return strconv.FormatFloat(value, 'f', -1, draftDetailFloatBitSize)
	}
	magnitude := int(math.Floor(math.Log10(math.Abs(value))))
	digits := draftRankingsSignificantDigits
	if magnitude < 0 {
		digits = draftRankingsFractionDigits
	}
	// No decimals once the integer digits reach the significant digits
	// (1234.6 is 1235, not 1200).
	decimals := min(max(digits-1-magnitude, 0), draftRankingsMaxDecimals)
	text := strconv.FormatFloat(value, 'f', decimals, draftDetailFloatBitSize)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	if text == "0" || text == "-0" {
		return strconv.FormatFloat(value, 'g', draftRankingsTinyDigits, draftDetailFloatBitSize)
	}
	return text
}

func stringValues[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}
