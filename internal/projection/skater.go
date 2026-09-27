package projection

import (
	"math"
	"time"
)

// Position codes used both for the player's own position and for grouping
// peers. Faceoffs are grouped more narrowly than other stats: see
// faceoffPositionGroup.
const (
	positionCentre       = "C"
	positionGroupDefense = "D"
	positionGroupForward = "F"

	faceoffGroupCentre    = "C"
	faceoffGroupNonCentre = "W"
	evenStrengthLinemates = 4

	neutralLinemateAdjustment = 1.0
)

type skaterTotals struct {
	toiSeconds      float64
	games           float64
	goals           float64
	assists         float64
	plusMinus       float64
	penaltyMinutes  float64
	powerPlayPoints float64
	shotsOnGoal     float64
	hits            float64
	blockedShots    float64
	faceoffsWon     float64
	faceoffsLost    float64
}

type linemateTotals struct {
	weightedPoints float64
	weightedTOI    float64
	sharedTOI      float64
}

type skaterHistory struct {
	teamID        int64
	position      string
	contextAge    int
	contextGames  int
	contextSeason int
	birthDate     time.Time
	seasons       map[int]struct{}
	games         int
	weight        float64
	totals        skaterTotals
	linemates     map[string]linemateTotals
}

func projectSkaters(cfg Config, targetSeason int, rows []SkaterSeason) []PlayerProjection {
	peers := make(map[string]skaterTotals)
	faceoffPeers := make(map[string]skaterTotals)
	history := make(map[int64]*skaterHistory)
	leagueLinemates := make(map[string]linemateTotals)
	for _, row := range rows {
		age, ok := seasonAge(targetSeason, row.Season)
		if !ok || age >= cfg.LookbackSeasons || row.GamesPlayed <= 0 {
			continue
		}
		weight := seasonWeight(cfg.SeasonDecay, age)
		group := positionGroup(row.Position)
		leagueContext := leagueLinemates[group]
		addLinemateSeason(&leagueContext, row, weight)
		leagueLinemates[group] = leagueContext
		if row.TOISeconds > 0 {
			peer := peers[group]
			addSkaterSeason(&peer, row, weight)
			peers[group] = peer

			faceoffGroup := faceoffPositionGroup(row.Position)
			faceoffPeer := faceoffPeers[faceoffGroup]
			addSkaterSeason(&faceoffPeer, row, weight)
			faceoffPeers[faceoffGroup] = faceoffPeer
		}

		player := history[row.PlayerID]
		if player == nil {
			player = &skaterHistory{
				seasons: make(map[int]struct{}), linemates: make(map[string]linemateTotals),
				contextAge: cfg.LookbackSeasons,
			}
			history[row.PlayerID] = player
		}
		updateSkaterContext(player, row, age)
		if _, seen := player.seasons[row.Season]; !seen {
			player.seasons[row.Season] = struct{}{}
			player.weight += weight
		}
		player.games += row.GamesPlayed
		addSkaterSeason(&player.totals, row, weight)
		playerContext := player.linemates[group]
		addLinemateSeason(&playerContext, row, weight)
		player.linemates[group] = playerContext
	}

	result := make([]PlayerProjection, 0, len(history))
	for playerID, player := range history {
		result = append(result, buildSkaterProjection(
			cfg, targetSeason, playerID, *player,
			peers[positionGroup(player.position)],
			faceoffPeers[faceoffPositionGroup(player.position)],
			leagueLinemates,
		))
	}
	return result
}

func addLinemateSeason(total *linemateTotals, row SkaterSeason, weight float64) {
	if row.LinemateTOISeconds <= 0 || !finite(row.LinematePointsPer60) || row.LinematePointsPer60 < 0 {
		return
	}
	weightedTOI := weight * float64(row.LinemateTOISeconds)
	total.weightedPoints += row.LinematePointsPer60 * weightedTOI
	total.weightedTOI += weightedTOI
	total.sharedTOI += float64(row.LinemateTOISeconds)
}

func updateSkaterContext(history *skaterHistory, row SkaterSeason, age int) {
	newer := age < history.contextAge
	moreGames := age == history.contextAge && row.GamesPlayed > history.contextGames
	stableTie := age == history.contextAge && row.GamesPlayed == history.contextGames &&
		(row.Position < history.position || row.Position == history.position && row.TeamID < history.teamID)
	if !newer && !moreGames && !stableTie {
		return
	}
	history.teamID = row.TeamID
	history.position = row.Position
	history.contextAge = age
	history.contextGames = row.GamesPlayed
	history.contextSeason = row.Season
	history.birthDate = row.BirthDate
}

func addSkaterSeason(total *skaterTotals, row SkaterSeason, weight float64) {
	total.toiSeconds += weight * float64(row.TOISeconds)
	total.games += weight * float64(row.GamesPlayed)
	total.goals += weight * float64(row.Goals)
	total.assists += weight * float64(row.Assists)
	total.plusMinus += weight * float64(row.PlusMinus)
	total.penaltyMinutes += weight * float64(row.PenaltyMinutes)
	total.powerPlayPoints += weight * float64(row.PowerPlayPoints)
	total.shotsOnGoal += weight * float64(row.ShotsOnGoal)
	total.hits += weight * float64(row.Hits)
	total.blockedShots += weight * float64(row.BlockedShots)
	total.faceoffsWon += weight * float64(row.FaceoffsWon)
	total.faceoffsLost += weight * float64(row.FaceoffsLost)
}

func buildSkaterProjection(
	cfg Config,
	targetSeason int,
	playerID int64,
	history skaterHistory,
	peers skaterTotals,
	faceoffPeers skaterTotals,
	leagueLinemates map[string]linemateTotals,
) PlayerProjection {
	expectedGames := clamp(history.totals.games/history.weight, 0, cfg.MaxGames)
	priorGames := cfg.SkaterPriorTOISeconds / typicalSkaterTOIPerGame
	fraction := uncertainty(cfg, history.totals.games+priorGames)
	values := map[Stat]Estimate{
		StatGamesPlayed: cappedEstimate(expectedGames, fraction, cfg.MaxGames),
	}
	if history.totals.toiSeconds <= 0 {
		fraction = cfg.MaximumUncertainty
		return skaterProjection(playerID, history, fraction, cfg.MinimumHistoryGames, values)
	}
	toiPerGame := history.totals.toiSeconds / history.totals.games
	expectedTOI := expectedGames * toiPerGame
	values[StatTOISeconds] = estimate(expectedTOI, fraction, true)

	projectSkaterStat(values, StatGoals, history.totals.goals, peers.goals, history, peers, cfg, expectedTOI, fraction, true)
	projectSkaterStat(values, StatAssists, history.totals.assists, peers.assists, history, peers, cfg, expectedTOI, fraction, true)
	projectSkaterStat(values, StatPlusMinus, history.totals.plusMinus, peers.plusMinus, history, peers, cfg, expectedTOI, fraction, false)
	projectSkaterStat(values, StatPenaltyMinutes, history.totals.penaltyMinutes, peers.penaltyMinutes, history, peers, cfg, expectedTOI, fraction, true)
	projectSkaterStat(values, StatPowerPlayPoints, history.totals.powerPlayPoints, peers.powerPlayPoints, history, peers, cfg, expectedTOI, fraction, true)
	projectSkaterStat(values, StatShotsOnGoal, history.totals.shotsOnGoal, peers.shotsOnGoal, history, peers, cfg, expectedTOI, fraction, true)
	projectSkaterStat(values, StatHits, history.totals.hits, peers.hits, history, peers, cfg, expectedTOI, fraction, true)
	projectSkaterStat(values, StatBlockedShots, history.totals.blockedShots, peers.blockedShots, history, peers, cfg, expectedTOI, fraction, true)
	if supportsFaceoffs(cfg.ModelVersion) {
		projectSkaterStat(values, StatFaceoffsWon, history.totals.faceoffsWon, faceoffPeers.faceoffsWon, history, faceoffPeers, cfg, expectedTOI, fraction, true)
		projectSkaterStat(values, StatFaceoffsLost, history.totals.faceoffsLost, faceoffPeers.faceoffsLost, history, faceoffPeers, cfg, expectedTOI, fraction, true)
	}
	var linemateContext *LinemateContext
	if supportsLinemateContext(cfg.ModelVersion) {
		linemateContext = adjustForLinemates(values, history.linemates, leagueLinemates, cfg)
	}
	if supportsAgingCurve(cfg.ModelVersion) {
		ageSkaterValues(cfg.AgingCurve, history, targetSeason, values, expectedTOI, fraction)
	}
	values[StatPoints] = sumEstimates(values[StatGoals], values[StatAssists])

	projection := skaterProjection(playerID, history, fraction, cfg.MinimumHistoryGames, values)
	projection.LinemateContext = linemateContext
	return projection
}

func adjustForLinemates(
	values map[Stat]Estimate,
	player, league map[string]linemateTotals,
	cfg Config,
) *LinemateContext {
	observedPoints, averagePoints, exposure, sharedTOI := aggregateLinemateContext(player, league)
	if exposure <= 0 {
		return nil
	}
	observed := observedPoints / exposure
	average := averagePoints / exposure
	exposurePrior := cfg.SkaterPriorTOISeconds * evenStrengthLinemates
	contextFactor := linemateAdjustmentFactor(observed, average, cfg.LinemateRegressionStrength, exposure, exposurePrior)
	adjustNonPowerPlayScoring(values, contextFactor)
	return &LinemateContext{
		ObservedPointsPer60: observed,
		AveragePointsPer60:  average,
		SharedTOISeconds:    sharedTOI,
		AdjustmentFactor:    contextFactor,
	}
}

func aggregateLinemateContext(player, league map[string]linemateTotals) (
	observedPoints, averagePoints, exposure, sharedTOI float64,
) {
	for _, group := range [...]string{positionGroupForward, positionGroupDefense} {
		playerGroup := player[group]
		leagueGroup := league[group]
		if playerGroup.weightedTOI <= 0 || leagueGroup.weightedTOI <= 0 {
			continue
		}
		observedPoints += playerGroup.weightedPoints
		averagePoints += leagueGroup.weightedPoints / leagueGroup.weightedTOI * playerGroup.weightedTOI
		exposure += playerGroup.weightedTOI
		sharedTOI += playerGroup.sharedTOI
	}
	return observedPoints, averagePoints, exposure, sharedTOI
}

func linemateAdjustmentFactor(observed, average, strength, exposure, exposurePrior float64) float64 {
	contextTotal := observed + average
	if contextTotal <= 0 || exposure <= 0 {
		return neutralLinemateAdjustment
	}
	reliability := exposure / (exposure + exposurePrior)
	return neutralLinemateAdjustment + strength*reliability*(average-observed)/contextTotal
}

func adjustNonPowerPlayScoring(values map[Stat]Estimate, factor float64) {
	goals, assists := values[StatGoals], values[StatAssists]
	total := sumEstimates(goals, assists)
	powerPlay := values[StatPowerPlayPoints]
	values[StatGoals] = contextAdjustedEstimate(goals, total, powerPlay, factor)
	values[StatAssists] = contextAdjustedEstimate(assists, total, powerPlay, factor)
}

func contextAdjustedEstimate(value, total, powerPlay Estimate, factor float64) Estimate {
	return normalizeEstimate(Estimate{
		Mean: adjustedScoringComponent(value.Mean, total.Mean, powerPlay.Mean, factor),
		Low:  adjustedScoringComponent(value.Low, total.Low, powerPlay.Low, factor),
		High: adjustedScoringComponent(value.High, total.High, powerPlay.High, factor),
	}, true)
}

func adjustedScoringComponent(value, total, powerPlay, factor float64) float64 {
	if total <= 0 || factor == neutralLinemateAdjustment {
		return value
	}
	adjustedTotal := powerPlay + math.Max(total-powerPlay, 0)*factor
	return value * adjustedTotal / total
}

func ageSkaterValues(curve *AgingCurve, history skaterHistory, targetSeason int, values map[Stat]Estimate, toi, fraction float64) {
	from := ageAtSeason(history.birthDate, history.contextSeason)
	to := ageAtSeason(history.birthDate, targetSeason)
	group := agingSkaterGroup(history.position)
	for _, stat := range skaterAgingStats {
		if stat == StatPoints {
			continue
		}
		delta := agingDelta(curve, group, stat, from, to)
		if delta == 0 {
			continue
		}
		values[stat] = ageEstimate(values[stat], delta, toi, fraction, stat != StatPlusMinus)
	}
}

func skaterProjection(
	playerID int64,
	history skaterHistory,
	fraction float64,
	minimumHistoryGames int,
	values map[Stat]Estimate,
) PlayerProjection {
	return PlayerProjection{
		PlayerKey:           playerKey(playerID),
		PlayerID:            playerIDPointer(playerID),
		TeamID:              optionalIDPointer(history.teamID),
		Kind:                PlayerKindSkater,
		Position:            history.position,
		Source:              SourceInternal,
		HistorySeasons:      len(history.seasons),
		HistoryGames:        history.games,
		SampleExposure:      history.totals.toiSeconds,
		Uncertainty:         fraction,
		InsufficientHistory: history.games < minimumHistoryGames,
		Values:              values,
	}
}

func projectSkaterStat(
	values map[Stat]Estimate,
	stat Stat,
	playerValue float64,
	peerValue float64,
	history skaterHistory,
	peers skaterTotals,
	cfg Config,
	expectedTOI float64,
	fraction float64,
	nonNegative bool,
) {
	peerRate := safeRate(peerValue, peers.toiSeconds)
	rate := (playerValue + peerRate*cfg.SkaterPriorTOISeconds) /
		(history.totals.toiSeconds + cfg.SkaterPriorTOISeconds)
	values[stat] = estimate(rate*expectedTOI, fraction, nonNegative)
}

func positionGroup(position string) string {
	if position == positionGroupDefense {
		return positionGroupDefense
	}
	return positionGroupForward
}

// faceoffPositionGroup groups peers for faceoff regression. Centres take
// nearly every draw; lumping them with wingers under positionGroup's F/D
// split (used for every other stat) would regress a centre's faceoff rate
// toward a forward average dragged down by wingers who rarely take one, and
// would give wingers and defensemen a peer rate inflated by centres. Splitting
// centre from everyone else keeps each group's peer rate close to its own
// true faceoff usage.
func faceoffPositionGroup(position string) string {
	if position == positionCentre {
		return faceoffGroupCentre
	}
	return faceoffGroupNonCentre
}

func safeRate(value, exposure float64) float64 {
	if exposure <= 0 || math.IsNaN(value) {
		return 0
	}
	return value / exposure
}

func sumEstimates(a, b Estimate) Estimate {
	return Estimate{Mean: a.Mean + b.Mean, Low: a.Low + b.Low, High: a.High + b.High}
}
