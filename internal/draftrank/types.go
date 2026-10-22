// Package draftrank is the draft helper's ranking service: it refreshes and
// stores one immutable ranking snapshot per league (the baseline ranking
// and one ranking per news scenario), and serves filtered, searched, sorted
// and paginated views of a stored snapshot. GraphQL, the CLI and its CSV
// and JSON exports all read rankings through the same Service and View, so
// they return the same values and ranks for the same snapshot and filters.
// Nothing on the read path computes a ranking or calls an LLM.
package draftrank

import (
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/newsadjust"
)

// Scenario names one ranking of a snapshot: the baseline (no news) or one
// of the news scenarios.
type Scenario string

const (
	ScenarioBaseline     Scenario = "baseline"
	ScenarioConservative          = Scenario(newsadjust.ScenarioConservative)
	ScenarioBase                  = Scenario(newsadjust.ScenarioBase)
	ScenarioOptimistic            = Scenario(newsadjust.ScenarioOptimistic)
)

// Scenarios lists every scenario in display order.
var Scenarios = []Scenario{ScenarioBaseline, ScenarioConservative, ScenarioBase, ScenarioOptimistic}

// DefaultScenario is served when a request names none: the news-adjusted
// base case, or the baseline when the snapshot has no news scenarios.
const DefaultScenario = ScenarioBase

// Contribution is one scoring stat's part of a player's score.
type Contribution struct {
	StatID      int     `json:"statId"`
	Abbr        string  `json:"abbr"`
	Stat        string  `json:"stat"`
	Projected   float64 `json:"projected"`
	Official    float64 `json:"official"`
	Adjusted    float64 `json:"adjusted"`
	Weight      float64 `json:"weight"`
	Direction   string  `json:"direction"`
	Opportunity float64 `json:"opportunity"`
	Explanation string  `json:"explanation"`
}

// Placement is a player's valuation in one scenario's ranking. Ranks come
// from the complete pool: filters never renumber them.
type Placement struct {
	Scenario      Scenario       `json:"scenario"`
	OverallRank   int            `json:"overallRank"`
	PositionRanks map[string]int `json:"positionRanks"`
	Tier          int            `json:"tier"`
	OfficialScore float64        `json:"officialScore"`
	AdjustedScore float64        `json:"adjustedScore"`
	// ReplacementValue is the score of the best available replacement.
	ReplacementValue float64        `json:"replacementValue"`
	Value            float64        `json:"value"`
	AdjustedValue    float64        `json:"adjustedValue"`
	Uncertainty      float64        `json:"uncertainty"`
	Contributions    []Contribution `json:"contributions"`
	Explanations     []string       `json:"explanations"`
}

// Player is one pool player of a snapshot with every scenario placement and
// the news adjustment behind any difference.
type Player struct {
	PlayerKey         string   `json:"playerKey"`
	YahooPlayerID     int      `json:"yahooPlayerId"`
	NHLPlayerID       int64    `json:"nhlPlayerId,omitempty"`
	Name              string   `json:"name"`
	Team              string   `json:"team"`
	EligiblePositions []string `json:"eligiblePositions"`
	// RosterEligiblePositions retains Yahoo's full slot eligibility, including
	// reserve-only tags. EligiblePositions remains the base-position ranking view.
	RosterEligiblePositions []string                     `json:"rosterEligiblePositions"`
	Status                  string                       `json:"status,omitempty"`
	StatusFull              string                       `json:"statusFull,omitempty"`
	InjuryNote              string                       `json:"injuryNote,omitempty"`
	Placements              map[Scenario]Placement       `json:"placements"`
	Adjustment              *newsadjust.PlayerAdjustment `json:"adjustment,omitempty"`
}

// Category is one scoring stat of the league.
type Category struct {
	StatID        int      `json:"statId"`
	Abbr          string   `json:"abbr"`
	Name          string   `json:"name"`
	PositionTypes []string `json:"positionTypes"`
	Direction     string   `json:"direction"`
	Weight        float64  `json:"weight,omitempty"`
}

// League is what a ranking consumer needs to know about the league's rules.
type League struct {
	Season      int                `json:"season"`
	LeagueID    int                `json:"leagueId"`
	LeagueKey   string             `json:"leagueKey"`
	Name        string             `json:"name"`
	NumTeams    int                `json:"numTeams"`
	ScoringType string             `json:"scoringType"`
	Format      string             `json:"format,omitempty"`
	Objective   string             `json:"objective,omitempty"`
	Provisional bool               `json:"provisional"`
	RulesSource string             `json:"rulesSource"`
	RulesHash   string             `json:"rulesHash"`
	FetchedAt   time.Time          `json:"fetchedAt,omitzero"`
	Categories  []Category         `json:"categories"`
	RosterSlots []draft.RosterSlot `json:"rosterSlots"`
}

// Options are the explicit manager assumptions a snapshot was ranked with.
type Options struct {
	BenchPolicy        draft.BenchPolicy       `json:"benchPolicy"`
	WorkloadCapPolicy  draft.WorkloadCapPolicy `json:"workloadCapPolicy,omitempty"`
	UncertaintyPenalty float64                 `json:"uncertaintyPenalty"`
}

// RankingOptions returns the draft ranking options.
func (o Options) RankingOptions() draft.RankingOptions {
	return draft.RankingOptions{
		BenchPolicy:        o.BenchPolicy,
		WorkloadCapPolicy:  o.WorkloadCapPolicy,
		UncertaintyPenalty: o.UncertaintyPenalty,
	}
}

// ProjectionInfo identifies the baseline projection snapshot.
type ProjectionInfo struct {
	SnapshotID   uuid.UUID `json:"snapshotId"`
	ModelVersion string    `json:"modelVersion"`
	SourceHash   string    `json:"sourceHash"`
	AsOf         time.Time `json:"asOf"`
	// DataThrough is the last game date the projection read.
	DataThrough time.Time `json:"dataThrough,omitzero"`
}

// AdjustmentInfo identifies the news adjustment behind the scenarios.
type AdjustmentInfo struct {
	RunID         uuid.UUID `json:"runId"`
	ID            string    `json:"id"`
	PolicyVersion string    `json:"policyVersion"`
	Calibration   string    `json:"calibration"`
	Alerts        []string  `json:"alerts,omitempty"`
	// Warnings are stored events that could not be turned into versions.
	Warnings []string `json:"warnings,omitempty"`
}

// SourceFreshness is one news source's coverage when the snapshot was built.
type SourceFreshness struct {
	SourceID            string    `json:"sourceId"`
	Publisher           string    `json:"publisher"`
	Scope               string    `json:"scope"`
	Status              string    `json:"status"`
	DataAsOf            time.Time `json:"dataAsOf,omitzero"`
	LastSuccessAt       time.Time `json:"lastSuccessAt,omitzero"`
	ConsecutiveFailures int       `json:"consecutiveFailures"`
	LastError           string    `json:"lastError,omitempty"`
}

// Meta is everything about a snapshot except its players.
type Meta struct {
	League   League `json:"league"`
	PoolSize int    `json:"poolSize"`
	// PoolFetchedAt is the oldest Yahoo fetch time in the pool.
	PoolFetchedAt time.Time           `json:"poolFetchedAt,omitzero"`
	Projection    ProjectionInfo      `json:"projection"`
	Adjustment    *AdjustmentInfo     `json:"adjustment,omitempty"`
	Options       Options             `json:"options"`
	Assumptions   []string            `json:"assumptions"`
	Versions      map[Scenario]string `json:"versions"`
	Scenarios     []Scenario          `json:"scenarios"`
	News          []SourceFreshness   `json:"news"`
	// Unavailable lists what the snapshot could not provide and why.
	Unavailable []Issue `json:"unavailable,omitempty"`
}

// SnapshotInfo identifies a stored snapshot and holds everything about it
// except its players.
type SnapshotInfo struct {
	ID        uuid.UUID `json:"id"`
	Identity  string    `json:"identity"`
	AsOf      time.Time `json:"asOf"`
	CreatedAt time.Time `json:"createdAt"`
	Meta
}

// Snapshot is one stored, immutable ranking refresh of a league.
type Snapshot struct {
	SnapshotInfo
	// Players are ordered by baseline rank.
	Players []Player `json:"players"`
}

// HasScenario reports whether the snapshot ranked a scenario.
func (s *SnapshotInfo) HasScenario(scenario Scenario) bool {
	return slices.Contains(s.Scenarios, scenario)
}

// ResolveScenario selects the scenario a consumer serves for a requested
// scenario and reports any fallback as a typed issue. An empty request means
// the default scenario: the news-adjusted base case, or the baseline when the
// snapshot has no news scenarios (a normal outcome reported with no issue). An
// explicit request the snapshot did not rank falls back to the baseline with a
// scenario-unavailable issue; when neither the requested scenario nor the
// baseline was ranked, the result has an empty scenario and a scenario-missing
// issue. Every consumer (view, recommendations, board) resolves through this
// method so they cannot disagree on the scenario shown.
func (s *SnapshotInfo) ResolveScenario(requested Scenario) (Scenario, []Issue) {
	if requested == "" {
		if s.HasScenario(DefaultScenario) {
			return DefaultScenario, nil
		}
		if s.HasScenario(ScenarioBaseline) {
			return ScenarioBaseline, nil
		}
		return "", []Issue{scenarioMissingIssue(DefaultScenario)}
	}
	if s.HasScenario(requested) {
		return requested, nil
	}
	if s.HasScenario(ScenarioBaseline) {
		return ScenarioBaseline, []Issue{scenarioFallbackIssue(requested)}
	}
	return "", []Issue{scenarioMissingIssue(requested)}
}

func scenarioFallbackIssue(requested Scenario) Issue {
	return Issue{Code: IssueScenarioFallback,
		Message: fmt.Sprintf("snapshot has no %s scenario (news adjustments unavailable); serving the baseline ranking", requested)}
}

func scenarioMissingIssue(requested Scenario) Issue {
	return Issue{Code: IssueScenarioMissing,
		Message: fmt.Sprintf("snapshot has no %s scenario and no baseline ranking", requested)}
}

// Note: draftrecommend keeps its own legacy message strings for the same two
// codes, verbatim, to decode stored runs from before issues carried codes.
// New results use the messages above; do not "unify" them with the legacy
// strings or old runs would stop decoding.
