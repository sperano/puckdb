package mcpserver

import (
	"cmp"
	"math"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// Yahoo position types of a roster row and of a scoring category.
const (
	skaterPositionType = "P"
	goaliePositionType = "G"
	// nhlGoaliePosition is players.position for a goalie.
	nhlGoaliePosition = "G"
	secondsPerHour    = 3600
	// minimumComparisonPlayers is the fewest compared players a z-score
	// needs: one player has no spread.
	minimumComparisonPlayers = 2
	// zScoreEpsilon snaps a z-score that is zero but for rounding (a ratio
	// category's player with no volume) to 0.
	zScoreEpsilon = 1e-9
)

// valuePositionTypes lists the position types in output order.
var valuePositionTypes = []string{skaterPositionType, goaliePositionType}

// ratioStat is a category computed from two totals: numerator /
// denominator × scale.
type ratioStat struct {
	numerator   projection.Stat
	denominator projection.Stat
	scale       float64
}

// ratioStats are the ratio categories the box scores give.
var ratioStats = map[projection.Stat]ratioStat{
	projection.StatGoalsAgainstAvg: {numerator: projection.StatGoalsAgainst, denominator: projection.StatTOISeconds, scale: secondsPerHour},
	projection.StatSavePercentage:  {numerator: projection.StatSaves, denominator: projection.StatShotsAgainst, scale: 1},
}

// valueCategory is one scoring category of the league the tool values.
type valueCategory struct {
	statID       int32
	abbr         string
	name         string
	positionType string
	direction    draft.Direction
	stat         projection.Stat
	ratio        *ratioStat
}

// statTotals are a player's totals by projection stat.
type statTotals map[projection.Stat]float64

// value is the category's value of totals; ok is false for a ratio with a
// zero denominator.
func (c valueCategory) value(t statTotals) (float64, bool) {
	if c.ratio == nil {
		return t[c.stat], true
	}
	denominator := t[c.ratio.denominator]
	if denominator == 0 {
		return 0, false
	}
	return t[c.ratio.numerator] / denominator * c.ratio.scale, true
}

// valueCategories maps the league's scoring categories (disabled and
// display-only ones left out, as draft.StatCategory.Scores does) to box-score stats with projection.YahooCategoryStat, ordered by
// stat ID; the categories it cannot map are returned as unsupported.
func valueCategories(rows []sqlcdb.YahooLeagueStatCategory) (supported []valueCategory, unsupported []string) {
	for _, row := range rows {
		if !row.Enabled || row.IsOnlyDisplayStat {
			continue
		}
		kind, stat, ok := projection.YahooCategoryStat(int(row.StatID))
		if !ok {
			unsupported = append(unsupported, categoryAbbr(row))
			continue
		}
		category := valueCategory{
			statID: row.StatID, abbr: categoryAbbr(row), name: row.Name, positionType: row.PositionType,
			direction: draft.DirectionFromStoredSortOrder(row.SortOrder.Int16, row.SortOrder.Valid), stat: stat,
		}
		if category.positionType == "" {
			category.positionType = positionTypeOfKind(kind)
		}
		if ratio, isRatio := ratioStats[stat]; isRatio {
			category.ratio = &ratio
		}
		supported = append(supported, category)
	}
	slices.SortFunc(supported, func(a, b valueCategory) int { return cmp.Compare(a.statID, b.statID) })
	return supported, unsupported
}

func categoryAbbr(row sqlcdb.YahooLeagueStatCategory) string {
	for _, label := range []string{row.DisplayName, row.Abbr, row.Name} {
		if label != "" {
			return label
		}
	}
	return ""
}

func positionTypeOfKind(kind projection.PlayerKind) string {
	if kind == projection.PlayerKindGoalie {
		return goaliePositionType
	}
	return skaterPositionType
}

// windowPlayer is one player's line of ListYahooLeaguePlayerWindowTotals,
// or the sum of a player's lines.
type windowPlayer struct {
	yahooPlayerID int32
	playerKey     string
	nhlPlayerID   pgtype.Int8
	name          string
	positionType  string
	days          int
	activeDays    int
	lastDate      pgtype.Date
	totals        statTotals
}

// games is the games played the totals cover.
func (p windowPlayer) games() float64 {
	return p.totals[projection.StatGamesPlayed]
}

func windowPlayerOf(row sqlcdb.ListYahooLeaguePlayerWindowTotalsRow) windowPlayer {
	positionType := row.PositionType
	if positionType != skaterPositionType && positionType != goaliePositionType {
		positionType = skaterPositionType
		if row.NhlPosition == nhlGoaliePosition {
			positionType = goaliePositionType
		}
	}
	games := row.SkaterGames
	if positionType == goaliePositionType {
		games = row.GoalieGames
	}
	return windowPlayer{
		yahooPlayerID: row.YahooPlayerID, playerKey: row.PlayerKey, nhlPlayerID: row.NhlPlayerID, name: row.Name,
		positionType: positionType, days: int(row.Days), activeDays: int(row.ActiveDays), lastDate: row.LastDate,
		totals: rowTotals(row, games),
	}
}

func rowTotals(row sqlcdb.ListYahooLeaguePlayerWindowTotalsRow, games int64) statTotals {
	totals := map[projection.Stat]int64{
		projection.StatGamesPlayed: games, projection.StatGoals: row.Goals, projection.StatAssists: row.Assists,
		projection.StatPoints: row.Points, projection.StatPlusMinus: row.PlusMinus,
		projection.StatPenaltyMinutes: row.PenaltyMinutes, projection.StatPowerPlayPoints: row.PowerPlayPoints,
		projection.StatShotsOnGoal: row.ShotsOnGoal, projection.StatHits: row.Hits,
		projection.StatBlockedShots: row.BlockedShots, projection.StatFaceoffsWon: row.FaceoffsWon,
		projection.StatFaceoffsLost: row.FaceoffsLost, projection.StatWins: row.Wins,
		projection.StatShutouts: row.Shutouts, projection.StatGoalsAgainst: row.GoalsAgainst,
		projection.StatShotsAgainst: row.ShotsAgainst, projection.StatSaves: row.Saves,
		projection.StatTOISeconds: row.GoalieTOISeconds,
	}
	out := make(statTotals, len(totals))
	for stat, value := range totals {
		out[stat] = float64(value)
	}
	return out
}

// add sums another line of the same player into p.
func (p *windowPlayer) add(other windowPlayer) {
	p.days += other.days
	p.activeDays += other.activeDays
	if other.lastDate.Time.After(p.lastDate.Time) {
		p.lastDate = other.lastDate
	}
	for stat, value := range other.totals {
		p.totals[stat] += value
	}
}

// comparisonPlayers are the matched players rostered by any team of the
// league on to, by position type, with their totals summed over every team
// that rostered them in the window.
func comparisonPlayers(rows []sqlcdb.ListYahooLeaguePlayerWindowTotalsRow, to pgtype.Date) map[string][]windowPlayer {
	byPlayer := make(map[int32]*windowPlayer)
	var onTo []int32
	for _, row := range rows {
		player := windowPlayerOf(row)
		if !player.nhlPlayerID.Valid {
			continue
		}
		if existing, seen := byPlayer[player.yahooPlayerID]; seen {
			existing.add(player)
		} else {
			byPlayer[player.yahooPlayerID] = &player
		}
		if row.LastDate.Time.Equal(to.Time) {
			onTo = append(onTo, player.yahooPlayerID)
		}
	}
	slices.Sort(onTo)
	out := make(map[string][]windowPlayer, len(valuePositionTypes))
	for _, id := range slices.Compact(onTo) {
		player := byPlayer[id]
		out[player.positionType] = append(out[player.positionType], *player)
	}
	return out
}

// categoryScale is the mean and spread of one category's comparison
// values; rate is the pooled ratio of a ratio category.
type categoryScale struct {
	mean, sd, rate float64
	usable         bool
}

// zInput is the value a category's z-score is computed from: the total of
// a counting category, or for a ratio the numerator above what the pooled
// comparison rate gives for the player's denominator (goals against above
// the league's average for the minutes played), so a ratio counts by
// volume.
func (c valueCategory) zInput(t statTotals, rate float64) float64 {
	if c.ratio == nil {
		return t[c.stat]
	}
	return t[c.ratio.numerator] - rate*t[c.ratio.denominator]
}

func scaleOf(c valueCategory, players []windowPlayer) categoryScale {
	if len(players) < minimumComparisonPlayers || c.direction == draft.DirectionUnknown {
		return categoryScale{}
	}
	var scale categoryScale
	if c.ratio != nil {
		var numerator, denominator float64
		for _, p := range players {
			numerator += p.totals[c.ratio.numerator]
			denominator += p.totals[c.ratio.denominator]
		}
		if denominator == 0 {
			return categoryScale{}
		}
		scale.rate = numerator / denominator
	}
	var sum, sumSquares float64
	for _, p := range players {
		x := c.zInput(p.totals, scale.rate)
		sum += x
		sumSquares += x * x
	}
	n := float64(len(players))
	scale.mean = sum / n
	scale.sd = math.Sqrt(math.Max(sumSquares/n-scale.mean*scale.mean, 0))
	scale.usable = true
	return scale
}

// z is the player's oriented z-score in the category (positive = helps the
// team); ok is false when the category has no usable scale.
func (s categoryScale) z(c valueCategory, t statTotals) (float64, bool) {
	if !s.usable {
		return 0, false
	}
	if s.sd == 0 {
		return 0, true
	}
	z := (c.zInput(t, s.rate) - s.mean) / s.sd
	if math.Abs(z) < zScoreEpsilon {
		return 0, true
	}
	if c.direction == draft.LowerIsBetter {
		z = -z
	}
	return z, true
}

// valuedPlayer is one matched team player with its category z-scores and
// projected pace.
type valuedPlayer struct {
	windowPlayer
	onRoster bool
	rank     int
	value    float64
	z        map[int32]float64
	pace     map[int32]float64
}

// valuePlayers scores the team's matched players of one position type
// against the comparison players and ranks the ones still on the roster by
// total value; players who left the team follow, unranked.
func valuePlayers(players []windowPlayer, categories []valueCategory, comparison []windowPlayer,
	projected map[int64]statTotals, to pgtype.Date) []valuedPlayer {
	scales := make(map[int32]categoryScale, len(categories))
	for _, c := range categories {
		scales[c.statID] = scaleOf(c, comparison)
	}
	valued := make([]valuedPlayer, 0, len(players))
	for _, p := range players {
		v := valuedPlayer{windowPlayer: p, onRoster: p.lastDate.Time.Equal(to.Time),
			z: make(map[int32]float64, len(categories)), pace: make(map[int32]float64, len(categories))}
		for _, c := range categories {
			if z, ok := scales[c.statID].z(c, p.totals); ok {
				v.z[c.statID] = z
				v.value += z
			}
			if pace, ok := projectedPace(c, projected[p.nhlPlayerID.Int64], p.games()); ok {
				v.pace[c.statID] = pace
			}
		}
		valued = append(valued, v)
	}
	slices.SortStableFunc(valued, compareValued)
	rank := 0
	for i := range valued {
		if valued[i].onRoster {
			rank++
			valued[i].rank = rank
		}
	}
	return valued
}

// compareValued puts rostered players first, then higher value, then name
// and player key, so equal values keep a stable order.
func compareValued(a, b valuedPlayer) int {
	if a.onRoster != b.onRoster {
		if a.onRoster {
			return -1
		}
		return 1
	}
	if order := cmp.Compare(b.value, a.value); order != 0 {
		return order
	}
	if order := strings.Compare(a.name, b.name); order != 0 {
		return order
	}
	return strings.Compare(a.playerKey, b.playerKey)
}

// projectedPace is what the projection expects of the player over the
// games played: the projected per-game rate × games for a counting
// category, the projected ratio itself for a ratio category.
func projectedPace(c valueCategory, projected statTotals, games float64) (float64, bool) {
	if projected == nil {
		return 0, false
	}
	if c.ratio != nil {
		return c.value(projected)
	}
	projectedGames := projected[projection.StatGamesPlayed]
	value, exists := projected[c.stat]
	if !exists || projectedGames <= 0 {
		return 0, false
	}
	return value / projectedGames * games, true
}
