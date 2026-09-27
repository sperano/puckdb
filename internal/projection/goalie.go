package projection

import (
	"math"
	"time"
)

const typicalGoalieShotsPerGame = 30

type goalieTotals struct {
	games        float64
	starts       float64
	toiSeconds   float64
	wins         float64
	shutouts     float64
	shotsAgainst float64
	saves        float64
	goalsAgainst float64
}

type goalieHistory struct {
	teamID        int64
	contextAge    int
	contextGames  int
	contextSeason int
	birthDate     time.Time
	seasons       map[int]struct{}
	games         int
	weight        float64
	totals        goalieTotals
}

type goalieWorkload struct {
	games  float64
	starts float64
	shots  float64
	toi    float64
}

func projectGoalies(cfg Config, targetSeason int, rows []GoalieSeason) []PlayerProjection {
	var peers goalieTotals
	history := make(map[int64]*goalieHistory)
	for _, row := range rows {
		age, ok := seasonAge(targetSeason, row.Season)
		if !ok || age >= cfg.LookbackSeasons || row.GamesPlayed <= 0 {
			continue
		}
		weight := seasonWeight(cfg.SeasonDecay, age)
		addGoalieSeason(&peers, row, weight)
		player := history[row.PlayerID]
		if player == nil {
			player = &goalieHistory{seasons: make(map[int]struct{}), contextAge: cfg.LookbackSeasons}
			history[row.PlayerID] = player
		}
		updateGoalieContext(player, row, age)
		if _, seen := player.seasons[row.Season]; !seen {
			player.seasons[row.Season] = struct{}{}
			player.weight += weight
		}
		player.games += row.GamesPlayed
		addGoalieSeason(&player.totals, row, weight)
	}

	result := make([]PlayerProjection, 0, len(history))
	for playerID, player := range history {
		result = append(result, buildGoalieProjection(cfg, targetSeason, playerID, *player, peers))
	}
	return result
}

func updateGoalieContext(history *goalieHistory, row GoalieSeason, age int) {
	newer := age < history.contextAge
	moreGames := age == history.contextAge && row.GamesPlayed > history.contextGames
	stableTie := age == history.contextAge && row.GamesPlayed == history.contextGames && row.TeamID < history.teamID
	if !newer && !moreGames && !stableTie {
		return
	}
	history.teamID = row.TeamID
	history.contextAge = age
	history.contextGames = row.GamesPlayed
	history.contextSeason = row.Season
	history.birthDate = row.BirthDate
}

func addGoalieSeason(total *goalieTotals, row GoalieSeason, weight float64) {
	total.games += weight * float64(row.GamesPlayed)
	total.starts += weight * float64(row.GamesStarted)
	total.toiSeconds += weight * float64(row.TOISeconds)
	total.wins += weight * float64(row.Wins)
	total.shutouts += weight * float64(row.Shutouts)
	total.shotsAgainst += weight * float64(row.ShotsAgainst)
	total.saves += weight * float64(row.Saves)
	total.goalsAgainst += weight * float64(row.GoalsAgainst)
}

func buildGoalieProjection(cfg Config, targetSeason int, playerID int64, history goalieHistory, peers goalieTotals) PlayerProjection {
	priorGames := cfg.GoaliePriorShots / typicalGoalieShotsPerGame
	fraction := uncertainty(cfg, history.totals.shotsAgainst/typicalGoalieShotsPerGame+priorGames)
	workload := projectGoalieWorkload(cfg, history, peers, priorGames)
	values := projectGoalieValues(cfg, history, peers, workload, priorGames, fraction)
	ageGoalieValues(cfg.AgingCurve, history, targetSeason, values, fraction)
	return PlayerProjection{
		PlayerKey:           playerKey(playerID),
		PlayerID:            playerIDPointer(playerID),
		TeamID:              optionalIDPointer(history.teamID),
		Kind:                PlayerKindGoalie,
		Position:            "G",
		Source:              SourceInternal,
		HistorySeasons:      len(history.seasons),
		HistoryGames:        history.games,
		SampleExposure:      history.totals.shotsAgainst,
		Uncertainty:         fraction,
		InsufficientHistory: history.games < cfg.MinimumHistoryGames,
		Values:              values,
	}
}

func ageGoalieValues(curve *AgingCurve, history goalieHistory, targetSeason int, values map[Stat]Estimate, fraction float64) {
	from := ageAtSeason(history.birthDate, history.contextSeason)
	to := ageAtSeason(history.birthDate, targetSeason)
	if curve == nil || from < 0 || to <= from {
		return
	}
	toi := values[StatTOISeconds].Mean
	changed := false
	// Shots and saves are the independent shot components. Goals against is
	// derived below; its fitted curve remains an auditable identity because
	// shots against = saves + goals against for every source row.
	for _, stat := range goalieAppliedAgingStats {
		delta := agingDelta(curve, agingGroupGoalie, stat, from, to)
		if delta == 0 {
			continue
		}
		changed = true
		values[stat] = ageEstimate(values[stat], delta, toi, fraction, true)
	}
	if !changed {
		return
	}
	shots := values[StatShotsAgainst].Mean
	saves := clamp(values[StatSaves].Mean, 0, shots)
	values[StatSaves] = estimate(saves, fraction, true)
	values[StatGoalsAgainst] = estimate(shots-saves, fraction, true)
	values[StatWins] = capEstimate(values[StatWins], values[StatGamesStarted].High)
	values[StatShutouts] = capEstimate(values[StatShutouts], values[StatGamesStarted].High)
	values[StatSavePercentage] = ratioEstimate(values[StatSaves], values[StatShotsAgainst], 1, 1)
	values[StatGoalsAgainstAvg] = ratioEstimate(values[StatGoalsAgainst], values[StatTOISeconds], secondsPerHour, 0)
}

func projectGoalieWorkload(cfg Config, history goalieHistory, peers goalieTotals, priorGames float64) goalieWorkload {
	games := clamp(history.totals.games/history.weight, 0, cfg.MaxGames)
	starts := clamp(history.totals.starts/history.weight, 0, games)
	shotsPerGame := regressedGoalieRate(
		history.totals.shotsAgainst,
		history.totals.games,
		peers.shotsAgainst,
		peers.games,
		priorGames,
	)
	toiPerGame := regressedGoalieRate(
		history.totals.toiSeconds,
		history.totals.games,
		peers.toiSeconds,
		peers.games,
		priorGames,
	)
	return goalieWorkload{
		games: games, starts: starts,
		shots: games * shotsPerGame, toi: games * toiPerGame,
	}
}

func projectGoalieValues(
	cfg Config,
	history goalieHistory,
	peers goalieTotals,
	workload goalieWorkload,
	priorGames float64,
	fraction float64,
) map[Stat]Estimate {
	saveRate := regressedGoalieRate(
		history.totals.saves,
		history.totals.shotsAgainst,
		peers.saves,
		peers.shotsAgainst,
		cfg.GoaliePriorShots,
	)
	goalsAgainstRate := regressedGoalieRate(
		history.totals.goalsAgainst,
		history.totals.shotsAgainst,
		peers.goalsAgainst,
		peers.shotsAgainst,
		cfg.GoaliePriorShots,
	)
	values := map[Stat]Estimate{
		StatGamesPlayed:  cappedEstimate(workload.games, fraction, cfg.MaxGames),
		StatGamesStarted: estimate(workload.starts, fraction, true),
		StatTOISeconds:   estimate(workload.toi, fraction, true),
		StatShotsAgainst: estimate(workload.shots, fraction, true),
		StatSaves:        estimate(workload.shots*saveRate, fraction, true),
		StatGoalsAgainst: estimate(workload.shots*goalsAgainstRate, fraction, true),
	}
	values[StatGamesStarted] = capEstimate(values[StatGamesStarted], values[StatGamesPlayed].High)
	values[StatWins] = estimate(workload.starts*regressedGoalieRate(
		history.totals.wins, history.totals.starts,
		peers.wins, peers.starts, priorGames,
	), fraction, true)
	values[StatWins] = capEstimate(values[StatWins], values[StatGamesStarted].High)
	values[StatShutouts] = estimate(workload.starts*regressedGoalieRate(
		history.totals.shutouts, history.totals.starts,
		peers.shutouts, peers.starts, priorGames,
	), fraction, true)
	values[StatShutouts] = capEstimate(values[StatShutouts], values[StatGamesStarted].High)
	values[StatSavePercentage] = ratioEstimate(values[StatSaves], values[StatShotsAgainst], 1, 1)
	values[StatGoalsAgainstAvg] = ratioEstimate(
		values[StatGoalsAgainst], values[StatTOISeconds], secondsPerHour, 0,
	)
	return values
}

func regressedGoalieRate(
	playerValue float64,
	playerExposure float64,
	peerValue float64,
	peerExposure float64,
	priorExposure float64,
) float64 {
	peerRate := safeRate(peerValue, peerExposure)
	denominator := playerExposure + priorExposure
	if denominator <= 0 {
		return peerRate
	}
	return (playerValue + peerRate*priorExposure) / denominator
}

func ratioEstimate(numerator, denominator Estimate, scale, maximum float64) Estimate {
	if denominator.Mean <= 0 {
		return Estimate{}
	}
	mean := numerator.Mean / denominator.Mean * scale
	low := 0.0
	if denominator.High > 0 {
		low = numerator.Low / denominator.High * scale
	}
	highDenominator := denominator.Low
	if highDenominator <= 0 {
		highDenominator = denominator.Mean
	}
	high := numerator.High / highDenominator * scale
	if math.IsNaN(high) || math.IsInf(high, 0) {
		high = mean
	}
	if maximum > 0 {
		high = math.Min(high, maximum)
	}
	return normalizeEstimate(Estimate{Mean: mean, Low: low, High: high}, true)
}

func capEstimate(value Estimate, maximum float64) Estimate {
	value.Low = math.Min(value.Low, maximum)
	value.Mean = math.Min(value.Mean, maximum)
	value.High = math.Min(value.High, maximum)
	return normalizeEstimate(value, true)
}
