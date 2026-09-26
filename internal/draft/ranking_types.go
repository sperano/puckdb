package draft

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/sperano/puckdb/internal/projection"
)

// RankingVersion identifies the draft valuation method independently of its inputs.
const RankingVersion = "league-replacement-v2"

const (
	defaultCategoryWeight     = 1.0
	defaultUncertaintyPenalty = 0.0
	maxCategoryWeight         = 10.0
)

// BenchPolicy controls whether ordinary bench seats contribute to draft demand.
type BenchPolicy string

const (
	BenchIncluded BenchPolicy = "included"
	BenchExcluded BenchPolicy = "excluded"
)

// WorkloadCapPolicy says how an imported game/start cap is interpreted.
// Yahoo cap semantics must be confirmed before selecting a policy.
type WorkloadCapPolicy string

const (
	WorkloadCapsPerPlayer WorkloadCapPolicy = "per-player"
)

// RankingOptions are explicit manager assumptions, separate from Yahoo rules.
// CategoryWeights and UncertaintyPenalty affect AdjustedValue only.
type RankingOptions struct {
	BenchPolicy        BenchPolicy
	WorkloadCapPolicy  WorkloadCapPolicy
	CategoryWeights    map[int]float64
	UncertaintyPenalty float64
}

func (o RankingOptions) validate(scoring Scoring) error {
	if o.BenchPolicy != "" && o.BenchPolicy != BenchIncluded && o.BenchPolicy != BenchExcluded {
		return fmt.Errorf("unsupported bench policy %q", o.BenchPolicy)
	}
	if o.WorkloadCapPolicy != "" && o.WorkloadCapPolicy != WorkloadCapsPerPlayer {
		return fmt.Errorf("unsupported workload cap policy %q", o.WorkloadCapPolicy)
	}
	if !finiteRanking(o.UncertaintyPenalty) || o.UncertaintyPenalty < 0 {
		return fmt.Errorf("uncertainty penalty must be finite and nonnegative")
	}
	for id, weight := range o.CategoryWeights {
		if !finiteRanking(weight) || weight < 0 || weight > maxCategoryWeight {
			return fmt.Errorf("category %d preference must be in [0, %.0f]", id, maxCategoryWeight)
		}
		if !slices.ContainsFunc(scoring.Stats, func(stat ScoringStat) bool { return stat.StatID == id }) {
			return fmt.Errorf("category %d preference does not match a scoring stat", id)
		}
	}
	return nil
}

func (o RankingOptions) normalized() RankingOptions {
	if o.BenchPolicy == "" {
		o.BenchPolicy = BenchIncluded
	}
	if o.CategoryWeights != nil {
		if len(o.CategoryWeights) == 0 {
			o.CategoryWeights = nil
			return o
		}
		copyWeights := make(map[int]float64, len(o.CategoryWeights))
		for id, weight := range o.CategoryWeights {
			copyWeights[id] = weight
		}
		o.CategoryWeights = copyWeights
	}
	return o
}

// Contribution records the official rule contribution and optional preference.
type Contribution struct {
	StatID      int
	Stat        projection.Stat
	Projected   float64
	Official    float64
	Adjusted    float64
	Weight      float64
	Direction   Direction
	Opportunity float64
	Explanation string
}

// RankedPlayer is one fixed-snapshot valuation. PositionRanks includes every
// eligible base position; overall rank never depends on a filter.
type RankedPlayer struct {
	PlayerKey         string
	Name              string
	EligiblePositions []string
	OverallRank       int
	PositionRanks     map[string]int
	Tier              int
	OfficialScore     float64
	AdjustedScore     float64
	BaselineValue     float64
	Value             float64
	AdjustedValue     float64
	Uncertainty       float64
	Contributions     []Contribution
	Explanations      []string
}

// RankingSnapshot is the immutable result of scoring one complete league pool.
type RankingSnapshot struct {
	Version           string
	RuleHash          string
	ProjectionHash    string
	ProjectionVersion string
	Scoring           Scoring
	Options           RankingOptions
	Assumptions       []string
	Players           []RankedPlayer
}

// Filter returns a view of the fixed ranking. OR semantics apply to multiple
// positions, and a multi-position player is emitted exactly once.
func (r RankingSnapshot) Filter(positions ...string) []RankedPlayer {
	if len(positions) == 0 {
		return cloneRankedPlayers(r.Players)
	}
	wanted := make(map[string]bool, len(positions))
	for _, position := range positions {
		wanted[strings.ToUpper(strings.TrimSpace(position))] = true
	}
	filtered := make([]RankedPlayer, 0)
	for _, player := range r.Players {
		if slices.ContainsFunc(player.EligiblePositions, func(p string) bool { return wanted[p] }) {
			filtered = append(filtered, cloneRankedPlayer(player))
		}
	}
	return filtered
}

func cloneRankedPlayers(players []RankedPlayer) []RankedPlayer {
	cloned := make([]RankedPlayer, len(players))
	for i, player := range players {
		cloned[i] = cloneRankedPlayer(player)
	}
	return cloned
}

func cloneRankedPlayer(player RankedPlayer) RankedPlayer {
	player.EligiblePositions = slices.Clone(player.EligiblePositions)
	player.Contributions = slices.Clone(player.Contributions)
	player.Explanations = slices.Clone(player.Explanations)
	positions := make(map[string]int, len(player.PositionRanks))
	for position, rank := range player.PositionRanks {
		positions[position] = rank
	}
	player.PositionRanks = positions
	return player
}

func finiteRanking(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
