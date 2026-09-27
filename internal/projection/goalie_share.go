package projection

import (
	"cmp"
	"maps"
	"math"
	"slices"
)

// halfLifeBase is the decay per half-life of a start's recency weight.
const halfLifeBase = 0.5

// applyGoalieStartShare is nhl-baseline-v7's correction of the goalie
// workload, which is otherwise a decay-weighted per-season average of games
// and starts that cannot see a club handing the net to one goalie late in a
// season or in the playoffs.
//
// A goalie's recent share is his recency-weighted share of his club's most
// recent started games (input.GoalieStarts). It applies only when his
// target-season club (input.TargetTeams) is the club of his most recent
// start; a goalie who changed clubs, or has no recent start, keeps his
// season-average starts. His starts become
//
//	blend·(share × MaxGames) + (1−blend)·(season-average starts)
//
// and his relief appearances (season-average games − starts) are kept on
// top. Every goalie with a target club then counts toward that club, whose
// projected starts are scaled down proportionally when they exceed
// MaxGames. Shots and TOI follow games; wins and shutouts follow starts.
func applyGoalieStartShare(cfg Config, input Input, workloads map[int64]goalieWorkload) {
	targets := make(map[int64]int64, len(input.TargetTeams))
	for _, target := range input.TargetTeams {
		targets[target.PlayerID] = target.TeamID
	}
	shares, recentClubs := recentGoalieShares(cfg, usableGoalieStarts(cfg, input.TargetSeason, input.GoalieStarts))
	clubs := make(map[int64][]int64)
	for _, playerID := range slices.Sorted(maps.Keys(workloads)) {
		club, ok := targets[playerID]
		if !ok {
			continue
		}
		if recent, started := recentClubs[playerID]; started && recent == club {
			workloads[playerID] = blendGoalieStarts(cfg, workloads[playerID], shares[club][playerID])
		}
		clubs[club] = append(clubs[club], playerID)
	}
	for _, members := range clubs {
		normalizeClubStarts(cfg, workloads, members)
	}
}

// usableGoalieStarts returns the starts inside the history window and the
// share window, sorted so the shares do not depend on input order. With a
// zero playoff weight the window counts regular-season games only, so
// playoffs are ignored rather than taking window slots at no weight.
func usableGoalieStarts(cfg Config, targetSeason int, starts []GoalieStart) []GoalieStart {
	floor := historyFloorSeason(targetSeason, cfg.LookbackSeasons)
	usable := slices.DeleteFunc(slices.Clone(starts), func(start GoalieStart) bool {
		return start.Season < floor || start.Season >= targetSeason || goalieStartRank(cfg, start) == 0
	})
	slices.SortFunc(usable, func(a, b GoalieStart) int {
		return cmp.Or(
			cmp.Compare(a.TeamID, b.TeamID),
			cmp.Compare(goalieStartRank(cfg, a), goalieStartRank(cfg, b)),
			cmp.Compare(a.PlayerID, b.PlayerID),
		)
	})
	return usable
}

// goalieStartRank is a start's 1-based position in the club's window the
// config reads, or 0 outside it.
func goalieStartRank(cfg Config, start GoalieStart) int {
	rank := start.RecencyRank
	if cfg.GoaliePlayoffWeight == 0 {
		rank = start.RegularSeasonRank
	}
	if rank <= 0 || rank > cfg.GoalieShareWindowGames {
		return 0
	}
	return rank
}

func goalieStartWeight(cfg Config, start GoalieStart) float64 {
	weight := math.Pow(halfLifeBase, float64(goalieStartRank(cfg, start)-1)/cfg.GoalieShareHalfLifeGames)
	if start.Playoff {
		weight *= cfg.GoaliePlayoffWeight
	}
	return weight
}

// recentGoalieShares returns each club's goalies' recency-weighted share of
// its started games, and each goalie's club of his most recent start. A
// game's weight enters the club's denominator once even if two goalies are
// flagged as its starter.
func recentGoalieShares(cfg Config, starts []GoalieStart) (map[int64]map[int64]float64, map[int64]int64) {
	shares := make(map[int64]map[int64]float64)
	denominators := make(map[int64]float64)
	counted := make(map[[2]int64]struct{})
	latest := make(map[int64]GoalieStart)
	for _, start := range starts {
		weight := goalieStartWeight(cfg, start)
		if shares[start.TeamID] == nil {
			shares[start.TeamID] = make(map[int64]float64)
		}
		shares[start.TeamID][start.PlayerID] += weight
		game := [2]int64{start.TeamID, int64(goalieStartRank(cfg, start))}
		if _, seen := counted[game]; !seen {
			counted[game] = struct{}{}
			denominators[start.TeamID] += weight
		}
		if previous, ok := latest[start.PlayerID]; !ok || newerGoalieStart(start, previous) {
			latest[start.PlayerID] = start
		}
	}
	for club, goalies := range shares {
		for playerID, weight := range goalies {
			goalies[playerID] = safeRate(weight, denominators[club])
		}
	}
	recentClubs := make(map[int64]int64, len(latest))
	for playerID, start := range latest {
		recentClubs[playerID] = start.TeamID
	}
	return shares, recentClubs
}

func newerGoalieStart(a, b GoalieStart) bool {
	if !a.GameDate.Equal(b.GameDate) {
		return a.GameDate.After(b.GameDate)
	}
	return a.TeamID < b.TeamID
}

func blendGoalieStarts(cfg Config, workload goalieWorkload, share float64) goalieWorkload {
	relief := math.Max(0, workload.games-workload.starts)
	starts := cfg.GoalieShareBlend*share*cfg.MaxGames + (1-cfg.GoalieShareBlend)*workload.starts
	return withGoalieStarts(cfg, workload, starts, relief)
}

// normalizeClubStarts scales a club's goalies' starts down proportionally
// when they sum to more than MaxGames, keeping each goalie's relief games.
func normalizeClubStarts(cfg Config, workloads map[int64]goalieWorkload, members []int64) {
	total := 0.0
	for _, playerID := range members {
		total += workloads[playerID].starts
	}
	if total <= cfg.MaxGames {
		return
	}
	scale := cfg.MaxGames / total
	for _, playerID := range members {
		workload := workloads[playerID]
		relief := math.Max(0, workload.games-workload.starts)
		workloads[playerID] = withGoalieStarts(cfg, workload, workload.starts*scale, relief)
	}
}

// withGoalieStarts sets a workload's starts, with relief games on top capped
// at MaxGames, and rescales shots and TOI with the new games.
func withGoalieStarts(cfg Config, workload goalieWorkload, starts, relief float64) goalieWorkload {
	workload.games = clamp(starts+relief, 0, cfg.MaxGames)
	workload.starts = clamp(starts, 0, workload.games)
	workload.shots = workload.games * workload.shotsPerGame
	workload.toi = workload.games * workload.toiPerGame
	return workload
}
