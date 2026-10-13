// Package evenstrength derives one game's even-strength totals from its
// shift chart: the shared even-strength time on ice of every pair of
// teammates (even_strength_pair_toi) and each skater's own even-strength
// time on ice and points (even_strength_skater_games).
//
// Each period is split into atomic half-open segments at every shift start
// and end. A segment is even strength when exactly two clubs are on the ice,
// each with one goalie and the same number of skaters, 3 through 5. A goal
// credits an even-strength point to its scorer and assisters when its
// situation code is equal strength with both goalies in and the player's own
// even-strength segment in that period covers the goal's clock time
// (start < t <= end), so a goal on a segment boundary counts for the segment
// it ends.
//
// The derivation used to be one SQL statement per table; its interval
// overlap join misled the planner into nested loops that cost seconds per
// game. It runs here over the game's few hundred shift rows instead, with
// results identical to that SQL (checked against it in
// legacy_equivalence_pg_test.go).
package evenstrength

import (
	"cmp"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	clubsOnIce     = 2
	goaliesInNet   = 1
	minEvenSkaters = 3
	maxEvenSkaters = 5

	secondsPerMinute = 60
	decimalBase      = 10

	// Digit places of a situation code, counted from the right.
	homeGoalieDigit  = 0
	homeSkatersDigit = 1
	awaySkatersDigit = 2
	awayGoalieDigit  = 3
)

// clockPattern accepts the "MM:SS" game clock the shift chart and the play
// by play use; anything else (empty, garbled) makes a shift or goal unusable.
var clockPattern = regexp.MustCompile(`^[0-9]{1,2}:[0-5][0-9]$`)

// Shift is one regular shift of a player with a box-score row on its club.
type Shift struct {
	PlayerID int64
	TeamID   int64
	Period   int32
	Start    string // "MM:SS" elapsed in the period, as stored
	End      string
	IsSkater bool // false: a goalie
}

// Goal is one goal of the game.
type Goal struct {
	Period    int32
	Clock     string  // "MM:SS" elapsed in the period, as stored
	Situation *int32  // [away goalie][away skaters][home skaters][home goalie]; nil when unknown
	Credited  []int64 // scorer, then assisters; players the feed omits are left out
}

// PairTOI is the even-strength time a skater shared with a teammate on the
// same club. Both directions of a pair are separate rows.
type PairTOI struct {
	PlayerID      int64
	TeammateID    int64
	SharedSeconds int32
}

// SkaterGame is a skater's even-strength time on ice and points for one club.
type SkaterGame struct {
	PlayerID   int64
	TeamID     int64
	TOISeconds int32
	Points     int32
}

// Totals is one game's derived rows, sorted by player then teammate or club.
type Totals struct {
	Pairs   []PairTOI
	Skaters []SkaterGame
}

// stint is one skater on one club during one even-strength segment.
type stint struct {
	period     int32
	start, end int32
	teamID     int64
	playerID   int64
}

// onIce is one player on one club during a segment.
type onIce struct {
	teamID   int64
	playerID int64
	isSkater bool
}

type timedShift struct {
	onIce
	start, end int32
}

// Derive computes one game's even-strength totals from its shifts and goals.
func Derive(shifts []Shift, goals []Goal) Totals {
	stints := evenStrengthStints(shifts)
	return Totals{Pairs: pairTOI(stints), Skaters: skaterGames(stints, goals)}
}

// parseClock converts an "MM:SS" clock to seconds.
func parseClock(clock string) (int32, bool) {
	if !clockPattern.MatchString(clock) {
		return 0, false
	}
	minutes, seconds, _ := strings.Cut(clock, ":")
	m, _ := strconv.ParseInt(minutes, decimalBase, 32)
	s, _ := strconv.ParseInt(seconds, decimalBase, 32)
	return int32(m*secondsPerMinute + s), true
}

// evenStrengthStints segments each period and returns every skater stint in
// its even-strength segments.
func evenStrengthStints(shifts []Shift) []stint {
	byPeriod := map[int32][]timedShift{}
	for _, s := range shifts {
		start, okStart := parseClock(s.Start)
		end, okEnd := parseClock(s.End)
		if !okStart || !okEnd || start >= end {
			continue
		}
		byPeriod[s.Period] = append(byPeriod[s.Period], timedShift{
			onIce: onIce{teamID: s.TeamID, playerID: s.PlayerID, isSkater: s.IsSkater},
			start: start,
			end:   end,
		})
	}
	var stints []stint
	for _, period := range slices.Sorted(maps.Keys(byPeriod)) {
		stints = append(stints, periodStints(period, byPeriod[period])...)
	}
	return stints
}

// periodStints splits one period at every shift boundary and keeps the
// skaters of the even-strength segments. Duplicate shifts count once.
func periodStints(period int32, shifts []timedShift) []stint {
	bounds := boundaries(shifts)
	segments := make([][]onIce, len(bounds)-1)
	for _, s := range shifts {
		first, _ := slices.BinarySearch(bounds, s.start)
		last, _ := slices.BinarySearch(bounds, s.end)
		for i := first; i < last; i++ {
			if !slices.Contains(segments[i], s.onIce) {
				segments[i] = append(segments[i], s.onIce)
			}
		}
	}
	var stints []stint
	for i, players := range segments {
		if !isEvenStrength(players) {
			continue
		}
		for _, p := range players {
			if p.isSkater {
				stints = append(stints, stint{
					period: period, start: bounds[i], end: bounds[i+1],
					teamID: p.teamID, playerID: p.playerID,
				})
			}
		}
	}
	return stints
}

// boundaries returns the sorted distinct start and end seconds of shifts.
func boundaries(shifts []timedShift) []int32 {
	bounds := make([]int32, 0, 2*len(shifts))
	for _, s := range shifts {
		bounds = append(bounds, s.start, s.end)
	}
	slices.Sort(bounds)
	return slices.Compact(bounds)
}

// isEvenStrength reports whether a segment has exactly two clubs, each with
// one goalie and the same number of skaters within the even-strength range.
func isEvenStrength(players []onIce) bool {
	type strength struct{ skaters, goalies int }
	byTeam := map[int64]strength{}
	for _, p := range players {
		s := byTeam[p.teamID]
		if p.isSkater {
			s.skaters++
		} else {
			s.goalies++
		}
		byTeam[p.teamID] = s
	}
	if len(byTeam) != clubsOnIce {
		return false
	}
	skaters := -1
	for _, s := range byTeam {
		if s.goalies != goaliesInNet || s.skaters < minEvenSkaters || s.skaters > maxEvenSkaters {
			return false
		}
		if skaters >= 0 && s.skaters != skaters {
			return false
		}
		skaters = s.skaters
	}
	return true
}

// pairTOI sums, for every ordered pair of skaters on the same club in the
// same segment, the segment's length.
func pairTOI(stints []stint) []PairTOI {
	type segmentClub struct {
		period, start int32
		teamID        int64
	}
	type pair struct{ player, teammate int64 }
	lines := map[segmentClub][]stint{}
	for _, s := range stints {
		key := segmentClub{s.period, s.start, s.teamID}
		lines[key] = append(lines[key], s)
	}
	shared := map[pair]int32{}
	for _, line := range lines {
		for _, player := range line {
			for _, teammate := range line {
				if player.playerID != teammate.playerID {
					shared[pair{player.playerID, teammate.playerID}] += player.end - player.start
				}
			}
		}
	}
	rows := make([]PairTOI, 0, len(shared))
	for p, seconds := range shared {
		rows = append(rows, PairTOI{PlayerID: p.player, TeammateID: p.teammate, SharedSeconds: seconds})
	}
	slices.SortFunc(rows, func(a, b PairTOI) int {
		return cmp.Or(cmp.Compare(a.PlayerID, b.PlayerID), cmp.Compare(a.TeammateID, b.TeammateID))
	})
	return rows
}

// skaterGames sums each skater's even-strength time per club and credits
// the even-strength points of goals inside the skater's stints.
func skaterGames(stints []stint, goals []Goal) []SkaterGame {
	type playerClub struct{ player, team int64 }
	toi := map[playerClub]int32{}
	for _, s := range stints {
		toi[playerClub{s.playerID, s.teamID}] += s.end - s.start
	}
	points := evenStrengthPoints(stints, goals)
	rows := make([]SkaterGame, 0, len(toi))
	for k, seconds := range toi {
		rows = append(rows, SkaterGame{PlayerID: k.player, TeamID: k.team, TOISeconds: seconds, Points: points[k.player]})
	}
	slices.SortFunc(rows, func(a, b SkaterGame) int {
		return cmp.Or(cmp.Compare(a.PlayerID, b.PlayerID), cmp.Compare(a.TeamID, b.TeamID))
	})
	return rows
}

// evenStrengthPoints counts, per player, the even-strength goals they are
// credited on, once per stint of theirs that covers the goal's clock time.
func evenStrengthPoints(stints []stint, goals []Goal) map[int64]int32 {
	type periodPlayer struct {
		period int32
		player int64
	}
	byPlayer := map[periodPlayer][]stint{}
	for _, s := range stints {
		key := periodPlayer{s.period, s.playerID}
		byPlayer[key] = append(byPlayer[key], s)
	}
	points := map[int64]int32{}
	for _, g := range goals {
		second, ok := parseClock(g.Clock)
		if !ok || g.Situation == nil || !isEvenStrengthSituation(*g.Situation) {
			continue
		}
		for _, player := range g.Credited {
			for _, s := range byPlayer[periodPlayer{g.Period, player}] {
				if second > s.start && second <= s.end {
					points[player]++
				}
			}
		}
	}
	return points
}

// isEvenStrengthSituation decodes a situation code
// ([away goalie][away skaters][home skaters][home goalie]): both goalies in
// and the same number of skaters, 3 through 5, on each side.
func isEvenStrengthSituation(code int32) bool {
	awayGoalie := situationDigit(code, awayGoalieDigit)
	awaySkaters := situationDigit(code, awaySkatersDigit)
	homeSkaters := situationDigit(code, homeSkatersDigit)
	homeGoalie := situationDigit(code, homeGoalieDigit)
	return awayGoalie == goaliesInNet && homeGoalie == goaliesInNet &&
		awaySkaters == homeSkaters &&
		awaySkaters >= minEvenSkaters && awaySkaters <= maxEvenSkaters
}

// situationDigit returns the digit at place (0 = rightmost) of a situation
// code, with Go's (and PostgreSQL's) truncating integer division.
func situationDigit(code int32, place int) int32 {
	for range place {
		code /= decimalBase
	}
	return code % decimalBase
}
