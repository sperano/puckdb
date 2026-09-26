package draft

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/sperano/puckdb/internal/projection"
)

const (
	tierGapSpreadFraction = 0.5
	seasonYearFactor      = 10_000
)

// NHLSeasonID returns the NHL season ID (e.g. 20262027) of a season's start
// year. draftrank.SeasonID delegates to this so the formula has one home;
// LoadStandInPool (standin_pool.go, TEMPORARY) also calls it directly, since
// internal/draft cannot import internal/draftrank, which imports
// internal/draft.
func NHLSeasonID(startYear int) int {
	return startYear*seasonYearFactor + startYear + 1
}

// BuildRanking values a complete Yahoo player pool against a frozen rules and
// projection snapshot. It never queries live state or changes scoring rules.
func BuildRanking(rules Snapshot, projected projection.Snapshot, pool []PoolPlayer, options RankingOptions) (RankingSnapshot, error) {
	scoring, err := ScoringFor(rules)
	if err != nil {
		return RankingSnapshot{}, err
	}
	options = options.normalized()
	if err := options.validate(scoring); err != nil {
		return RankingSnapshot{}, err
	}
	if len(pool) == 0 || projected.SourceDataHash == "" || projected.Config.ModelVersion == "" {
		return RankingSnapshot{}, fmt.Errorf("player pool, projection source hash and model version are required")
	}
	if !sameHockeySeason(rules.Rules.Season, projected.TargetSeason) {
		return RankingSnapshot{}, fmt.Errorf("rules season %d differs from projection season %d", rules.Rules.Season, projected.TargetSeason)
	}
	ruleHash, _, err := rules.Rules.Hash()
	if err != nil {
		return RankingSnapshot{}, err
	}
	if rules.Hash != "" && rules.Hash != ruleHash {
		return RankingSnapshot{}, fmt.Errorf("rules snapshot hash does not match normalized rules")
	}
	caps, err := capsFromRules(rules.Rules)
	if err != nil {
		return RankingSnapshot{}, err
	}
	if err := caps.validatePolicy(options.WorkloadCapPolicy); err != nil {
		return RankingSnapshot{}, err
	}
	units, err := demandUnits(rules.Rules, options)
	if err != nil {
		return RankingSnapshot{}, err
	}
	candidates, err := prepareCandidates(scoring, pool, projected, caps.gameCap, caps.goalieStartsCap)
	if err != nil {
		return RankingSnapshot{}, err
	}
	for _, stat := range scoring.Stats {
		if statCandidateCount(stat.StatID, candidates) == 0 {
			return RankingSnapshot{}, fmt.Errorf("pool has no players for scoring stat %d", stat.StatID)
		}
	}
	slices.SortFunc(scoring.Stats, func(a, b ScoringStat) int { return a.StatID - b.StatID })
	scoreCandidates(scoring, options, candidates)
	officialPlan := draftedCandidates(candidates, units, false)
	adjustedPlan := draftedCandidates(candidates, units, true)
	players := rankCandidates(candidates, officialPlan, adjustedPlan, options, scoring.Provisional)
	version, err := rankingIdentity(ruleHash, projected, pool, scoring, options)
	if err != nil {
		return RankingSnapshot{}, err
	}
	return RankingSnapshot{
		Version: version, RuleHash: ruleHash, ProjectionHash: projected.SourceDataHash,
		ProjectionVersion: projected.Config.ModelVersion, Scoring: scoring,
		Options: options, Assumptions: rankingAssumptions(scoring, options, caps), Players: players,
	}, nil
}

func rankingAssumptions(scoring Scoring, options RankingOptions, caps rankingCaps) []string {
	assumptions := []string{fmt.Sprintf("bench policy: %s; IR, IR+ and NA do not create ordinary demand", options.BenchPolicy)}
	if scoring.Format == FormatCategories {
		if scoring.Objective == ObjectiveHeadToHead {
			assumptions = append(assumptions, "head-to-head category contributions receive a downside penalty from projection uncertainty")
		} else {
			assumptions = append(assumptions, "season-long category contributions use full-season standardized estimates")
		}
	}
	if caps.gameCap > 0 {
		assumptions = append(assumptions, fmt.Sprintf("max_games_played %.0f uses the explicit %s cap policy", caps.gameCap, options.WorkloadCapPolicy))
	}
	if caps.goalieStartsCap > 0 {
		assumptions = append(assumptions, fmt.Sprintf("max_goalie_starts %.0f uses the explicit %s cap policy", caps.goalieStartsCap, options.WorkloadCapPolicy))
	}
	return assumptions
}

func rankCandidates(
	candidates []rankingCandidate,
	officialPlan, adjustedPlan draftPlan,
	options RankingOptions,
	provisional bool,
) []RankedPlayer {
	players := make([]RankedPlayer, 0, len(candidates))
	for i, candidate := range candidates {
		baseline := replacementValue(i, candidates, officialPlan)
		adjustedBaseline := replacementValue(i, candidates, adjustedPlan)
		value := candidate.official - baseline
		adjusted := candidate.adjusted - adjustedBaseline
		adjusted -= options.UncertaintyPenalty * candidate.projection.Uncertainty * math.Abs(adjusted)
		explanations := []string{
			fmt.Sprintf("league scoring: %.3f; best available replacement across %s: %.3f", candidate.official, strings.Join(candidate.positions, "/"), baseline),
			fmt.Sprintf("projection uncertainty %.1f%%; bench demand %s", candidate.projection.Uncertainty*100, options.BenchPolicy),
		}
		if provisional {
			explanations = append(explanations, "rules came from a temporary stand-in and are provisional")
		}
		players = append(players, RankedPlayer{
			PlayerKey: candidate.pool.PlayerKey, Name: candidate.pool.Name,
			EligiblePositions: slices.Clone(candidate.positions), PositionRanks: make(map[string]int),
			OfficialScore: candidate.official, AdjustedScore: candidate.adjusted,
			BaselineValue: baseline, Value: value, AdjustedValue: adjusted,
			Uncertainty: candidate.projection.Uncertainty, Contributions: candidate.parts,
			Explanations: explanations,
		})
	}
	slices.SortFunc(players, compareRankedPlayers)
	assignRanksAndTiers(players)
	return players
}

func compareRankedPlayers(a, b RankedPlayer) int {
	if a.AdjustedValue > b.AdjustedValue {
		return -1
	}
	if a.AdjustedValue < b.AdjustedValue {
		return 1
	}
	if a.Value > b.Value {
		return -1
	}
	if a.Value < b.Value {
		return 1
	}
	return strings.Compare(a.PlayerKey, b.PlayerKey)
}

func assignRanksAndTiers(players []RankedPlayer) {
	if len(players) == 0 {
		return
	}
	var mean, sumSquares float64
	for _, player := range players {
		mean += player.AdjustedValue
		sumSquares += player.AdjustedValue * player.AdjustedValue
	}
	mean /= float64(len(players))
	spread := math.Sqrt(math.Max(sumSquares/float64(len(players))-mean*mean, 0))
	positionCounts := make(map[string]int)
	tier := 1
	for i := range players {
		if i > 0 && players[i-1].AdjustedValue-players[i].AdjustedValue > tierGapSpreadFraction*spread {
			tier++
		}
		players[i].OverallRank = i + 1
		players[i].Tier = tier
		for _, position := range players[i].EligiblePositions {
			positionCounts[position]++
			players[i].PositionRanks[position] = positionCounts[position]
		}
	}
}

func rankingIdentity(ruleHash string, projected projection.Snapshot, pool []PoolPlayer, scoring Scoring, options RankingOptions) (string, error) {
	type poolEntry struct {
		PlayerKey string
		Name      string
		Positions []string
	}
	poolIdentity := make([]poolEntry, 0, len(pool))
	for _, player := range pool {
		poolIdentity = append(poolIdentity, poolEntry{
			PlayerKey: player.PlayerKey,
			Name:      player.Name,
			Positions: eligibleBasePositions(player.EligiblePositions),
		})
	}
	slices.SortFunc(poolIdentity, func(a, b poolEntry) int {
		return strings.Compare(a.PlayerKey, b.PlayerKey)
	})
	projectionPlayers := slices.Clone(projected.Players)
	slices.SortFunc(projectionPlayers, func(a, b projection.PlayerProjection) int {
		return strings.Compare(a.PlayerKey, b.PlayerKey)
	})
	identity := struct {
		Version        string
		RuleHash       string
		ProjectionHash string
		ModelVersion   string
		ModelConfig    projection.Config
		Players        []projection.PlayerProjection
		Pool           []poolEntry
		Scoring        Scoring
		Options        RankingOptions
	}{RankingVersion, ruleHash, projected.SourceDataHash, projected.Config.ModelVersion, projected.Config, projectionPlayers, poolIdentity, scoring, options}
	data, err := json.Marshal(identity)
	if err != nil {
		return "", fmt.Errorf("encode ranking identity: %w", err)
	}
	digest := sha256.Sum256(data)
	return RankingVersion + ":" + hex.EncodeToString(digest[:]), nil
}

func sameHockeySeason(leagueSeason, projectionSeason int) bool {
	if leagueSeason == projectionSeason {
		return true
	}
	startYear := projectionSeason / seasonYearFactor
	endYear := projectionSeason % seasonYearFactor
	return leagueSeason == startYear && endYear == startYear+1
}
