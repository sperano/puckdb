package draftrank

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/sperano/puckdb/internal/draft"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// ErrInvalidQuery wraps every rejected view request.
var ErrInvalidQuery = errors.New("invalid ranking query")

// SortField orders a view. Ties always fall back to overall rank, then
// player key, so every order is total and pages never overlap.
type SortField string

const (
	SortOverallRank   SortField = "overall_rank"
	SortPositionRank  SortField = "position_rank"
	SortName          SortField = "name"
	SortTeam          SortField = "team"
	SortScore         SortField = "score"
	SortValue         SortField = "value"
	SortAdjustedValue SortField = "adjusted_value"
	SortUncertainty   SortField = "uncertainty"
	SortTier          SortField = "tier"
	SortBaselineRank  SortField = "baseline_rank"
	SortRankChange    SortField = "rank_change"
)

// SortDirection overrides a field's natural direction (ranks, tiers, names
// and uncertainty ascending; scores, values and rank changes descending).
type SortDirection string

const (
	SortNatural    SortDirection = ""
	SortAscending  SortDirection = "asc"
	SortDescending SortDirection = "desc"
)

// descendingByDefault lists the fields whose natural order is descending.
var descendingByDefault = map[SortField]bool{
	SortScore: true, SortValue: true, SortAdjustedValue: true, SortRankChange: true,
}

// ViewPositions are the positions a view can filter on.
var ViewPositions = []string{draft.PositionCenter, draft.PositionLeftWing, draft.PositionRightWing, draft.PositionDefense, draft.PositionGoalie}

// Query selects rows of one snapshot. Positions match with OR semantics;
// PlayerKeys restricts the view to those players (a comparison). Limit 0
// returns every row after Offset.
type Query struct {
	Scenario   Scenario
	Positions  []string
	PlayerKeys []string
	Search     string
	Sort       SortField
	Direction  SortDirection
	Offset     int
	Limit      int
}

// Row is one player in the selected scenario. RankChange is the baseline
// rank minus the scenario rank (positive: news moved the player up).
// PositionRank is the best rank among the filtered (or, unfiltered, all
// eligible) positions.
type Row struct {
	Player
	Placement    Placement `json:"placement"`
	BaselineRank int       `json:"baselineRank"`
	RankChange   int       `json:"rankChange"`
	PositionRank int       `json:"positionRank"`
}

// View is one page of a snapshot.
type View struct {
	Scenario Scenario `json:"scenario"`
	Issues   []Issue  `json:"issues,omitempty"`
	Total    int      `json:"total"`
	Offset   int      `json:"offset"`
	Limit    int      `json:"limit"`
	Rows     []Row    `json:"rows"`
}

// View filters, searches, sorts and paginates the snapshot. It never
// recomputes a value or renumbers a rank.
func (s *Snapshot) View(q Query) (View, error) {
	q, err := normalizeQuery(q)
	if err != nil {
		return View{}, err
	}
	view := View{Offset: q.Offset, Limit: q.Limit}
	view.Scenario, view.Issues = s.scenarioFor(q.Scenario)
	rows := s.matchingRows(q, view.Scenario)
	sortRows(rows, q)
	view.Total = len(rows)
	view.Rows = page(rows, q.Offset, q.Limit)
	return view, nil
}

func normalizeQuery(q Query) (Query, error) {
	if q.Offset < 0 || q.Limit < 0 {
		return q, fmt.Errorf("%w: offset and limit must not be negative", ErrInvalidQuery)
	}
	if q.Scenario != "" && !slices.Contains(Scenarios, q.Scenario) {
		return q, fmt.Errorf("%w: unknown scenario %q", ErrInvalidQuery, q.Scenario)
	}
	if q.Sort == "" {
		q.Sort = SortOverallRank
	}
	if !validSort(q.Sort) {
		return q, fmt.Errorf("%w: unknown sort %q", ErrInvalidQuery, q.Sort)
	}
	if q.Direction != SortNatural && q.Direction != SortAscending && q.Direction != SortDescending {
		return q, fmt.Errorf("%w: unknown sort direction %q", ErrInvalidQuery, q.Direction)
	}
	positions, err := normalizePositions(q.Positions)
	if err != nil {
		return q, err
	}
	q.Positions = positions
	return q, nil
}

func validSort(field SortField) bool {
	switch field {
	case SortOverallRank, SortPositionRank, SortName, SortTeam, SortScore, SortValue,
		SortAdjustedValue, SortUncertainty, SortTier, SortBaselineRank, SortRankChange:
		return true
	}
	return false
}

// normalizePositions upper-cases, validates and de-duplicates positions.
func normalizePositions(positions []string) ([]string, error) {
	var out []string
	for _, position := range positions {
		position = strings.ToUpper(strings.TrimSpace(position))
		if position == "" {
			continue
		}
		if !slices.Contains(ViewPositions, position) {
			return nil, fmt.Errorf("%w: unknown position %q (use %s)", ErrInvalidQuery, position, strings.Join(ViewPositions, ", "))
		}
		if !slices.Contains(out, position) {
			out = append(out, position)
		}
	}
	return out, nil
}

// scenarioFor returns the scenario to serve and an issue when the requested
// one is not in the snapshot.
func (s *Snapshot) scenarioFor(requested Scenario) (Scenario, []Issue) {
	if requested == "" {
		if s.HasScenario(DefaultScenario) {
			return DefaultScenario, nil
		}
		return ScenarioBaseline, nil
	}
	if s.HasScenario(requested) {
		return requested, nil
	}
	return ScenarioBaseline, []Issue{{
		Code:    IssueScenarioFallback,
		Message: fmt.Sprintf("snapshot has no %s scenario (news adjustments unavailable); serving the baseline ranking", requested),
	}}
}

func (s *Snapshot) matchingRows(q Query, scenario Scenario) []Row {
	terms := searchTerms(q.Search)
	var keys map[string]bool
	if len(q.PlayerKeys) > 0 {
		keys = make(map[string]bool, len(q.PlayerKeys))
		for _, key := range q.PlayerKeys {
			keys[strings.TrimSpace(key)] = true
		}
	}
	rows := make([]Row, 0, len(s.Players))
	for _, player := range s.Players {
		if keys != nil && !keys[player.PlayerKey] {
			continue
		}
		if len(q.Positions) > 0 && !slices.ContainsFunc(player.EligiblePositions, func(p string) bool { return slices.Contains(q.Positions, p) }) {
			continue
		}
		if !matchesSearch(player, terms) {
			continue
		}
		rows = append(rows, rowOf(player, scenario, q.Positions))
	}
	return rows
}

func rowOf(player Player, scenario Scenario, positions []string) Row {
	placement := player.Placements[scenario]
	baseline := player.Placements[ScenarioBaseline].OverallRank
	row := Row{Player: player, Placement: placement, BaselineRank: baseline, RankChange: baseline - placement.OverallRank}
	if len(positions) == 0 {
		positions = player.EligiblePositions
	}
	for _, position := range positions {
		if rank, eligible := placement.PositionRanks[position]; eligible && (row.PositionRank == 0 || rank < row.PositionRank) {
			row.PositionRank = rank
		}
	}
	return row
}

// foldText lower-cases and strips accents so "stutzle" finds "Stützle".
func foldText(text string) string {
	folded, _, err := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), text)
	if err != nil {
		folded = text
	}
	return strings.ToLower(folded)
}

func searchTerms(search string) []string {
	return strings.Fields(foldText(search))
}

// matchesSearch requires every term to appear in the name, team or key.
func matchesSearch(player Player, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	haystack := foldText(player.Name + " " + player.Team + " " + player.PlayerKey)
	for _, term := range terms {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

func sortRows(rows []Row, q Query) {
	descending := descendingByDefault[q.Sort]
	switch q.Direction {
	case SortAscending:
		descending = false
	case SortDescending:
		descending = true
	}
	slices.SortStableFunc(rows, func(a, b Row) int {
		order := compareField(a, b, q.Sort)
		if descending {
			order = -order
		}
		return cmp.Or(order, cmp.Compare(a.Placement.OverallRank, b.Placement.OverallRank), strings.Compare(a.PlayerKey, b.PlayerKey))
	})
}

func compareField(a, b Row, field SortField) int {
	switch field {
	case SortPositionRank:
		return cmp.Compare(a.PositionRank, b.PositionRank)
	case SortName:
		return strings.Compare(foldText(a.Name), foldText(b.Name))
	case SortTeam:
		return strings.Compare(a.Team, b.Team)
	case SortScore:
		return cmp.Compare(a.Placement.OfficialScore, b.Placement.OfficialScore)
	case SortValue:
		return cmp.Compare(a.Placement.Value, b.Placement.Value)
	case SortAdjustedValue:
		return cmp.Compare(a.Placement.AdjustedValue, b.Placement.AdjustedValue)
	case SortUncertainty:
		return cmp.Compare(a.Placement.Uncertainty, b.Placement.Uncertainty)
	case SortTier:
		return cmp.Compare(a.Placement.Tier, b.Placement.Tier)
	case SortBaselineRank:
		return cmp.Compare(a.BaselineRank, b.BaselineRank)
	case SortRankChange:
		return cmp.Compare(a.RankChange, b.RankChange)
	}
	return cmp.Compare(a.Placement.OverallRank, b.Placement.OverallRank)
}

func page(rows []Row, offset, limit int) []Row {
	if offset >= len(rows) {
		return []Row{}
	}
	rows = rows[offset:]
	if limit > 0 && limit < len(rows) {
		rows = rows[:limit]
	}
	return rows
}
