package projection

import "math"

// Position codes used both for the player's own position and for grouping
// peers. Faceoffs are grouped more narrowly than other stats: see
// faceoffPositionGroup.
const (
	positionCentre       = "C"
	positionGroupDefense = "D"
	positionGroupForward = "F"

	faceoffGroupCentre    = "C"
	faceoffGroupNonCentre = "W"
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

type skaterHistory struct {
	teamID       int64
	position     string
	contextAge   int
	contextGames int
	seasons      map[int]struct{}
	games        int
	weight       float64
	totals       skaterTotals
}

func projectSkaters(cfg Config, targetSeason int, rows []SkaterSeason) []PlayerProjection {
	peers := make(map[string]skaterTotals)
	faceoffPeers := make(map[string]skaterTotals)
	history := make(map[int64]*skaterHistory)
	for _, row := range rows {
		age, ok := seasonAge(targetSeason, row.Season)
		if !ok || age >= cfg.LookbackSeasons || row.GamesPlayed <= 0 {
			continue
		}
		weight := seasonWeight(cfg.SeasonDecay, age)
		if row.TOISeconds > 0 {
			group := positionGroup(row.Position)
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
			player = &skaterHistory{seasons: make(map[int]struct{}), contextAge: cfg.LookbackSeasons}
			history[row.PlayerID] = player
		}
		updateSkaterContext(player, row, age)
		if _, seen := player.seasons[row.Season]; !seen {
			player.seasons[row.Season] = struct{}{}
			player.weight += weight
		}
		player.games += row.GamesPlayed
		addSkaterSeason(&player.totals, row, weight)
	}

	result := make([]PlayerProjection, 0, len(history))
	for playerID, player := range history {
		result = append(result, buildSkaterProjection(
			cfg, playerID, *player,
			peers[positionGroup(player.position)],
			faceoffPeers[faceoffPositionGroup(player.position)],
		))
	}
	return result
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
	playerID int64,
	history skaterHistory,
	peers skaterTotals,
	faceoffPeers skaterTotals,
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
	projectSkaterStat(values, StatFaceoffsWon, history.totals.faceoffsWon, faceoffPeers.faceoffsWon, history, faceoffPeers, cfg, expectedTOI, fraction, true)
	projectSkaterStat(values, StatFaceoffsLost, history.totals.faceoffsLost, faceoffPeers.faceoffsLost, history, faceoffPeers, cfg, expectedTOI, fraction, true)
	values[StatPoints] = sumEstimates(values[StatGoals], values[StatAssists])

	return skaterProjection(playerID, history, fraction, cfg.MinimumHistoryGames, values)
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
