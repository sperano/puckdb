package projection

import (
	"cmp"
	"maps"
	"slices"
)

// Team environment: a skater's goals, assists, shots and power-play points
// depend partly on the club around them. The model measures each club's
// goals-for, shots-for and power-play opportunities per game over the same
// weighted lookback seasons it reads player history from, regresses each
// rate toward the league average by TeamEnvironmentPriorGames, and expresses
// it as an index (1 = league average). A skater whose target-season club
// differs from the clubs behind their history has those rates scaled by the
// target club's index over their history's games-weighted index, capped at
// TeamEnvironmentMaxChange either way. Clubs without environment rows count
// as league average; goalies are not adjusted.

// teamTotals accumulates a club's (or the league's) weighted environment.
type teamTotals struct {
	games                  float64
	goalsFor               float64
	shotsFor               float64
	powerPlayGames         float64
	powerPlayOpportunities float64
}

// teamIndex is a club's regressed environment relative to league average.
type teamIndex struct {
	goals     float64
	shots     float64
	powerPlay float64
}

var neutralTeamIndex = teamIndex{goals: 1, shots: 1, powerPlay: 1}

type teamEnvironment struct {
	indexes map[int64]teamIndex
	abbrevs map[int64]string
	targets map[int64]int64
	splits  map[playerSeason][]SkaterClubSeason
}

type playerSeason struct {
	playerID int64
	season   int
}

type teamAbbrev struct {
	abbrev string
	age    int
}

func buildTeamEnvironment(cfg Config, targetSeason int, rows []TeamSeason, targets []PlayerTeam, splits []SkaterClubSeason) teamEnvironment {
	if !supportsTeamEnvironment(cfg.ModelVersion) {
		// Earlier versions neither hash nor read team inputs: no target
		// clubs, so their players keep their most recent history club.
		return teamEnvironment{}
	}
	env := teamEnvironment{
		indexes: make(map[int64]teamIndex), abbrevs: make(map[int64]string),
		targets: make(map[int64]int64, len(targets)), splits: splitSeasons(splits),
	}
	for _, target := range targets {
		if target.TeamID > 0 {
			env.targets[target.PlayerID] = target.TeamID
		}
	}
	totals := make(map[int64]teamTotals)
	names := make(map[int64]teamAbbrev)
	var league teamTotals
	for _, row := range rows {
		age, ok := seasonAge(targetSeason, row.Season)
		if !ok || age >= cfg.LookbackSeasons || row.GamesPlayed <= 0 || row.TeamID <= 0 {
			continue
		}
		weight := seasonWeight(cfg.SeasonDecay, age)
		club := totals[row.TeamID]
		addTeamSeason(&club, row, weight)
		totals[row.TeamID] = club
		addTeamSeason(&league, row, weight)
		if name, seen := names[row.TeamID]; row.Abbrev != "" && (!seen || age < name.age) {
			names[row.TeamID] = teamAbbrev{abbrev: row.Abbrev, age: age}
		}
	}
	prior := cfg.TeamEnvironmentPriorGames
	for teamID, club := range totals {
		env.indexes[teamID] = teamIndex{
			goals:     regressedIndex(club.goalsFor, club.games, league.goalsFor, league.games, prior),
			shots:     regressedIndex(club.shotsFor, club.games, league.shotsFor, league.games, prior),
			powerPlay: regressedIndex(club.powerPlayOpportunities, club.powerPlayGames, league.powerPlayOpportunities, league.powerPlayGames, prior),
		}
	}
	for teamID, name := range names {
		env.abbrevs[teamID] = name.abbrev
	}
	return env
}

// splitSeasons groups split-season club games by player and season, clubs
// in ID order so crediting them is deterministic.
func splitSeasons(rows []SkaterClubSeason) map[playerSeason][]SkaterClubSeason {
	out := make(map[playerSeason][]SkaterClubSeason)
	for _, row := range rows {
		key := playerSeason{playerID: row.PlayerID, season: row.Season}
		out[key] = append(out[key], row)
	}
	for _, clubs := range out {
		slices.SortFunc(clubs, func(a, b SkaterClubSeason) int { return cmp.Compare(a.TeamID, b.TeamID) })
	}
	return out
}

// addClubGames credits a history row's weighted games to the clubs they
// were played for: the per-club split, once per season, when the season
// was split between clubs, else the row's own club.
func (e teamEnvironment) addClubGames(history *skaterHistory, row SkaterSeason, weight float64, firstOfSeason bool) {
	split, isSplit := e.splits[playerSeason{playerID: row.PlayerID, season: row.Season}]
	if !isSplit {
		history.teamGames[row.TeamID] += weight * float64(row.GamesPlayed)
		return
	}
	if !firstOfSeason {
		return
	}
	for _, club := range split {
		history.teamGames[club.TeamID] += weight * float64(club.GamesPlayed)
	}
}

func addTeamSeason(total *teamTotals, row TeamSeason, weight float64) {
	total.games += weight * float64(row.GamesPlayed)
	total.goalsFor += weight * float64(row.GoalsFor)
	total.shotsFor += weight * float64(row.ShotsFor)
	total.powerPlayGames += weight * float64(row.PowerPlayGames)
	total.powerPlayOpportunities += weight * float64(row.PowerPlayOpportunities)
}

// regressedIndex is a club's per-game rate, shrunk toward the league rate
// by prior games of league-average play, divided by the league rate.
func regressedIndex(value, exposure, leagueValue, leagueExposure, prior float64) float64 {
	leagueRate := safeRate(leagueValue, leagueExposure)
	if leagueRate <= 0 || exposure+prior <= 0 {
		return 1
	}
	return (value + leagueRate*prior) / (exposure + prior) / leagueRate
}

func (e teamEnvironment) index(teamID int64) teamIndex {
	if index, exists := e.indexes[teamID]; exists {
		return index
	}
	return neutralTeamIndex
}

// historyIndex is the games-weighted index of the clubs behind a player's
// history. Clubs are summed in ID order so the result is deterministic.
func (e teamEnvironment) historyIndex(teamGames map[int64]float64) teamIndex {
	var sum teamIndex
	var games float64
	for _, teamID := range slices.Sorted(maps.Keys(teamGames)) {
		weight := teamGames[teamID]
		index := e.index(teamID)
		sum.goals += weight * index.goals
		sum.shots += weight * index.shots
		sum.powerPlay += weight * index.powerPlay
		games += weight
	}
	if games <= 0 {
		return neutralTeamIndex
	}
	return teamIndex{goals: sum.goals / games, shots: sum.shots / games, powerPlay: sum.powerPlay / games}
}

// applyTeamEnvironment moves a skater projection to their target-season
// club: TeamID becomes that club and, when any of their weighted history
// was played elsewhere, the team-dependent estimates are scaled and the
// adjustment recorded for the ranking explanations.
func applyTeamEnvironment(cfg Config, env teamEnvironment, player *PlayerProjection, history skaterHistory) {
	if player.PlayerID == nil {
		return
	}
	target, known := env.targets[*player.PlayerID]
	if !known {
		return
	}
	player.TeamID = playerIDPointer(target)
	otherShare := otherClubShare(history.teamGames, target)
	if cfg.TeamEnvironmentMaxChange <= 0 || otherShare <= 0 {
		return
	}
	factors := teamFactors(env.historyIndex(history.teamGames), env.index(target), cfg.TeamEnvironmentMaxChange)
	for stat, factor := range factors {
		value, exists := player.Values[stat]
		if !exists {
			// Nothing to scale (a skater without TOI has no rate
			// projections): the factor is not reported either.
			delete(factors, stat)
			continue
		}
		player.Values[stat] = scaleEstimate(value, factor)
	}
	if len(factors) == 0 {
		return
	}
	if _, exists := player.Values[StatPoints]; exists {
		player.Values[StatPoints] = sumEstimates(player.Values[StatGoals], player.Values[StatAssists])
	}
	player.TeamEnvironment = &TeamEnvironment{
		FromTeamID: history.teamID, FromTeam: env.abbrevs[history.teamID],
		ToTeamID: target, ToTeam: env.abbrevs[target],
		OtherClubShare: otherShare, Factors: factors,
	}
}

// teamFactors is each team-dependent stat's capped ratio of the target
// club's index to the history's.
func teamFactors(from, to teamIndex, limit float64) map[Stat]float64 {
	goals := teamFactor(to.goals, from.goals, limit)
	return map[Stat]float64{
		StatGoals:           goals,
		StatAssists:         goals,
		StatShotsOnGoal:     teamFactor(to.shots, from.shots, limit),
		StatPowerPlayPoints: teamFactor(to.powerPlay, from.powerPlay, limit),
	}
}

func otherClubShare(teamGames map[int64]float64, target int64) float64 {
	var total float64
	for _, teamID := range slices.Sorted(maps.Keys(teamGames)) {
		total += teamGames[teamID]
	}
	if total <= 0 {
		return 0
	}
	return (total - teamGames[target]) / total
}

func teamFactor(to, from, limit float64) float64 {
	if from <= 0 {
		return 1
	}
	return clamp(to/from, 1-limit, 1+limit)
}

func scaleEstimate(value Estimate, factor float64) Estimate {
	return Estimate{Mean: value.Mean * factor, Low: value.Low * factor, High: value.High * factor}
}
