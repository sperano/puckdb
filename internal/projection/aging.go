package projection

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"time"
)

const (
	agingGroupCenter  = "C"
	agingGroupWing    = "W"
	agingGroupDefense = "D"
	agingGroupGoalie  = "G"
)

// AgingCurve is part of the persisted model config. A step is the weighted
// change in a count statistic per 60 minutes from Age to Age+1.
type AgingCurve struct {
	Version             string
	TrainingFloorSeason int
	ThroughSeason       int
	AgeReference        string
	Steps               []AgingStep
}

type AgingStep struct {
	Group              string
	Stat               Stat
	Age                int
	DeltaPer60         float64
	Pairs              int
	HarmonicTOISeconds float64
}

type agingKey struct {
	group string
	stat  Stat
	age   int
}

type agingSum struct {
	weightedDelta float64
	weight        float64
	pairs         int
}

type agingSeason struct {
	season    int
	birthDate time.Time
	group     string
	toi       float64
	groupTOI  float64
	counts    map[Stat]float64
}

var skaterAgingStats = []Stat{
	StatGoals, StatAssists, StatPoints, StatPlusMinus, StatPenaltyMinutes,
	StatPowerPlayPoints, StatShotsOnGoal, StatHits, StatBlockedShots,
}

var goalieAgingStats = []Stat{
	StatWins, StatShutouts, StatShotsAgainst, StatSaves, StatGoalsAgainst,
}

var goalieAppliedAgingStats = []Stat{
	StatWins, StatShutouts, StatShotsAgainst, StatSaves,
}

func validateAgingCurve(curve *AgingCurve) error {
	switch {
	case curve.Version != AgingCurveVersion:
		return fmt.Errorf("unsupported aging curve version %q", curve.Version)
	case curve.TrainingFloorSeason != AgingTrainingFloorSeason:
		return fmt.Errorf("aging curve training floor must be %d", AgingTrainingFloorSeason)
	case curve.ThroughSeason <= 0:
		return fmt.Errorf("aging curve through season must be positive")
	case curve.AgeReference != AgingAgeReference:
		return fmt.Errorf("aging curve age reference must be %q", AgingAgeReference)
	}
	seen := make(map[agingKey]struct{}, len(curve.Steps))
	for _, step := range curve.Steps {
		key := agingKey{group: step.Group, stat: step.Stat, age: step.Age}
		if !validAgingStep(step) {
			return fmt.Errorf("invalid aging curve step %s/%s/%d", step.Group, step.Stat, step.Age)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate aging curve step %s/%s/%d", step.Group, step.Stat, step.Age)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validAgingStep(step AgingStep) bool {
	if step.Age < 0 || !finite(step.DeltaPer60) || step.Pairs <= 0 ||
		!finite(step.HarmonicTOISeconds) || step.HarmonicTOISeconds <= 0 {
		return false
	}
	if step.Group == agingGroupGoalie {
		return slices.Contains(goalieAgingStats, step.Stat)
	}
	return slices.Contains([]string{agingGroupCenter, agingGroupWing, agingGroupDefense}, step.Group) &&
		slices.Contains(skaterAgingStats, step.Stat)
}

func fitAgingCurve(input Input) *AgingCurve {
	curve := &AgingCurve{
		Version: AgingCurveVersion, TrainingFloorSeason: AgingTrainingFloorSeason,
		ThroughSeason: historyFloorSeason(input.TargetSeason, 1), AgeReference: AgingAgeReference,
	}
	sums := make(map[agingKey]agingSum)
	accumulateAgingPairs(sums, collectSkaterAgingSeasons(input.Skaters, input.TargetSeason), skaterAgingStats)
	accumulateAgingPairs(sums, collectGoalieAgingSeasons(input.Goalies, input.TargetSeason), goalieAgingStats)
	for key, sum := range sums {
		curve.Steps = append(curve.Steps, AgingStep{
			Group: key.group, Stat: key.stat, Age: key.age,
			DeltaPer60: sum.weightedDelta / sum.weight,
			Pairs:      sum.pairs, HarmonicTOISeconds: sum.weight,
		})
	}
	slices.SortFunc(curve.Steps, func(a, b AgingStep) int {
		if order := cmp.Compare(a.Group, b.Group); order != 0 {
			return order
		}
		if order := cmp.Compare(a.Stat, b.Stat); order != 0 {
			return order
		}
		return cmp.Compare(a.Age, b.Age)
	})
	return curve
}

func collectSkaterAgingSeasons(rows []SkaterSeason, target int) map[int64]map[int]*agingSeason {
	players := make(map[int64]map[int]*agingSeason)
	for _, row := range rows {
		if !eligibleAgingSeason(row.Season, target, row.BirthDate, row.TOISeconds) {
			continue
		}
		group := agingSkaterGroup(row.Position)
		if group == "" {
			continue
		}
		counts := map[Stat]float64{
			StatGoals: float64(row.Goals), StatAssists: float64(row.Assists),
			StatPoints: float64(row.Goals + row.Assists), StatPlusMinus: float64(row.PlusMinus),
			StatPenaltyMinutes: float64(row.PenaltyMinutes), StatPowerPlayPoints: float64(row.PowerPlayPoints),
			StatShotsOnGoal: float64(row.ShotsOnGoal), StatHits: float64(row.Hits),
			StatBlockedShots: float64(row.BlockedShots),
		}
		mergeAgingSeason(players, row.PlayerID, agingSeason{
			season: row.Season, birthDate: row.BirthDate, group: group,
			toi: float64(row.TOISeconds), groupTOI: float64(row.TOISeconds), counts: counts,
		})
	}
	return players
}

func collectGoalieAgingSeasons(rows []GoalieSeason, target int) map[int64]map[int]*agingSeason {
	players := make(map[int64]map[int]*agingSeason)
	for _, row := range rows {
		if !eligibleAgingSeason(row.Season, target, row.BirthDate, row.TOISeconds) {
			continue
		}
		counts := map[Stat]float64{
			StatWins: float64(row.Wins), StatShutouts: float64(row.Shutouts),
			StatShotsAgainst: float64(row.ShotsAgainst), StatSaves: float64(row.Saves),
			StatGoalsAgainst: float64(row.GoalsAgainst),
		}
		mergeAgingSeason(players, row.PlayerID, agingSeason{
			season: row.Season, birthDate: row.BirthDate, group: agingGroupGoalie,
			toi: float64(row.TOISeconds), groupTOI: float64(row.TOISeconds), counts: counts,
		})
	}
	return players
}

func eligibleAgingSeason(season, target int, birthDate time.Time, toi int) bool {
	return season >= AgingTrainingFloorSeason && season < target && !birthDate.IsZero() && toi > 0
}

func mergeAgingSeason(players map[int64]map[int]*agingSeason, id int64, row agingSeason) {
	if players[id] == nil {
		players[id] = make(map[int]*agingSeason)
	}
	current := players[id][row.season]
	if current == nil {
		players[id][row.season] = &row
		return
	}
	if row.groupTOI > current.groupTOI || row.groupTOI == current.groupTOI && row.group < current.group {
		current.group = row.group
		current.groupTOI = row.groupTOI
	}
	current.toi += row.toi
	for stat, count := range row.counts {
		current.counts[stat] += count
	}
}

func accumulateAgingPairs(sums map[agingKey]agingSum, players map[int64]map[int]*agingSeason, stats []Stat) {
	for _, playerID := range sortedAgingPlayerIDs(players) {
		seasons := players[playerID]
		for _, season := range sortedAgingSeasons(seasons) {
			older := seasons[season]
			next := seasons[nextSeason(season)]
			if next == nil || older.group != next.group {
				continue
			}
			age := ageAtSeason(older.birthDate, season)
			if age < 0 || ageAtSeason(next.birthDate, next.season) != age+1 {
				continue
			}
			weight := 2 * older.toi * next.toi / (older.toi + next.toi)
			for _, stat := range stats {
				delta := (next.counts[stat]/next.toi - older.counts[stat]/older.toi) * secondsPerHour
				key := agingKey{group: older.group, stat: stat, age: age}
				sum := sums[key]
				sum.weightedDelta += weight * delta
				sum.weight += weight
				sum.pairs++
				sums[key] = sum
			}
		}
	}
}

func sortedAgingPlayerIDs(players map[int64]map[int]*agingSeason) []int64 {
	ids := make([]int64, 0, len(players))
	for id := range players {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func sortedAgingSeasons(seasons map[int]*agingSeason) []int {
	values := make([]int, 0, len(seasons))
	for season := range seasons {
		values = append(values, season)
	}
	slices.Sort(values)
	return values
}

func agingSkaterGroup(position string) string {
	switch position {
	case "C":
		return agingGroupCenter
	case "LW", "RW", "F":
		return agingGroupWing
	case "D":
		return agingGroupDefense
	default:
		return ""
	}
}

func nextSeason(season int) int {
	start := seasonStartYear(season) + 1
	return start*10_000 + start + 1
}

func ageAtSeason(birthDate time.Time, season int) int {
	if birthDate.IsZero() {
		return -1
	}
	reference := time.Date(seasonStartYear(season)+1, time.January, 1, 0, 0, 0, 0, time.UTC)
	age := reference.Year() - birthDate.Year()
	if birthDate.Month() > reference.Month() ||
		birthDate.Month() == reference.Month() && birthDate.Day() > reference.Day() {
		age--
	}
	return age
}

func agingDelta(curve *AgingCurve, group string, stat Stat, fromAge, toAge int) float64 {
	if curve == nil || fromAge < 0 || toAge <= fromAge {
		return 0
	}
	var delta float64
	for _, step := range curve.Steps {
		if step.Group == group && step.Stat == stat && step.Age >= fromAge && step.Age < toAge {
			delta += step.DeltaPer60
		}
	}
	return delta
}

func ageEstimate(value Estimate, deltaPer60, toiSeconds, fraction float64, nonNegative bool) Estimate {
	mean := value.Mean + deltaPer60*toiSeconds/secondsPerHour
	if nonNegative {
		mean = math.Max(0, mean)
	}
	return estimate(mean, fraction, nonNegative)
}
