// Package draftrecommend evaluates a fixed draft session and ranking snapshot.
// It is deterministic and does not call Yahoo, news services, or an LLM.
package draftrecommend

import (
	"time"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/draftsession"
)

// RecommendationVersion identifies the evaluation rules a Result was
// produced with; stored runs keep it so a replay can tell them apart.
const RecommendationVersion = "draft-recommendation-v1"

// PickSlot is one pick in the verified chronological draft order. Supplying
// every pick captures snake, linear, and traded-pick formats without guessing.
type PickSlot struct {
	Key    draftsession.PickKey `json:"key"`
	TeamID int                  `json:"teamId"`
}

// Strategy is the drafter's explicit preferences. CategoryWeights multiply
// a category's contribution to the selected scenario's adjusted value.
// RiskTolerance in [0, 1] applies only when RiskToleranceSet: it discounts a
// candidate's value by uncertainty × (1 − RiskTolerance), so 1 ignores risk.
type Strategy struct {
	CategoryWeights  map[int]float64 `json:"categoryWeights,omitempty"`
	RiskTolerance    float64         `json:"riskTolerance,omitempty"`
	RiskToleranceSet bool            `json:"riskToleranceSet,omitempty"`
}

// AvailabilitySource identifies external status evidence. An empty source
// means no external availability status is applied.
type AvailabilitySource struct {
	Name                 string            `json:"name,omitempty"`
	Version              string            `json:"version,omitempty"`
	AsOf                 time.Time         `json:"asOf,omitzero"`
	Unavailable          map[string]bool   `json:"unavailable,omitempty"`
	UnavailableReasons   map[string]string `json:"unavailableReasons,omitempty"`
	UnavailablePlayerIDs map[int]string    `json:"unavailablePlayerIds,omitempty"`
}

// Input is the single validated snapshot boundary for recommendation
// evaluation. Roster includes keepers and all selected players on our team.
type Input struct {
	Session          draftsession.State   `json:"session"`
	SessionLeagueKey string               `json:"sessionLeagueKey"`
	SessionSafe      bool                 `json:"sessionSafe"`
	SessionStale     bool                 `json:"sessionStale"`
	Ranking          *draftrank.Snapshot  `json:"ranking"`
	Scenario         draftrank.Scenario   `json:"scenario"`
	OurTeamID        int                  `json:"ourTeamId"`
	Roster           []draft.RosterPlayer `json:"roster"`
	KeeperPlayerKeys map[string]bool      `json:"keeperPlayerKeys,omitempty"`
	Order            []PickSlot           `json:"order,omitempty"`
	Strategy         Strategy             `json:"strategy"`
	Availability     AvailabilitySource   `json:"availability"`
	ADP              ADPSource            `json:"adp,omitempty"`
	RankingIssues    []draftrank.Issue    `json:"rankingIssues,omitempty"`
	GeneratedAt      time.Time            `json:"generatedAt"`
}

// ADPSource is optional sourced wait-risk evidence. ADP is in overall picks;
// it supports a labeled estimate only and never changes player valuation.
type ADPSource struct {
	Name     string             `json:"name,omitempty"`
	Version  string             `json:"version,omitempty"`
	AsOf     time.Time          `json:"asOf,omitzero"`
	ByPlayer map[string]float64 `json:"byPlayer,omitempty"`
}

// NumericReasons are the audited numbers behind a candidate: LeagueValue is
// the ranking's adjusted value, StrategyValue that value under the strategy's
// category weights, RiskAdjustment its uncertainty discount, MarginalValue
// StrategyValue minus RiskAdjustment, and NewsDelta the selected scenario's
// value minus the baseline's. AssignedSlot and AdditionalStarts are where the
// player fits the roster and how many starting assignments that adds;
// ScarcityCount counts the available players who fit that slot, and the
// position rank and tier fields compare the player with the next one who
// does.
type NumericReasons struct {
	LeagueValue        float64          `json:"leagueValue"`
	StrategyValue      float64          `json:"strategyValue"`
	RiskAdjustment     float64          `json:"riskAdjustment"`
	NewsDelta          float64          `json:"newsDelta"`
	MarginalValue      float64          `json:"marginalValue"`
	PositionRank       int              `json:"positionRank,omitempty"`
	NextPositionRank   int              `json:"nextPositionRank,omitempty"`
	Tier               int              `json:"tier,omitempty"`
	NextTier           int              `json:"nextTier,omitempty"`
	TierDrop           int              `json:"tierDrop,omitempty"`
	ScarcityCount      int              `json:"scarcityCount,omitempty"`
	AdditionalStarts   int              `json:"additionalStarts"`
	AssignedSlot       string           `json:"assignedSlot,omitempty"`
	CategoryStrengths  []CategoryReason `json:"categoryStrengths,omitempty"`
	CategoryWeaknesses []CategoryReason `json:"categoryWeaknesses,omitempty"`
	Tradeoffs          []string         `json:"tradeoffs,omitempty"`
}

// CategoryReason is one scoring category's contribution to a candidate's
// value.
type CategoryReason struct {
	StatID int     `json:"statId"`
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
}

// WaitRisk compares a candidate's sourced ADP with the drafter's next pick
// number. Kind is the labeled estimate; it never claims the player will or
// will not be available.
type WaitRisk struct {
	Kind           string    `json:"kind"`
	Source         string    `json:"source"`
	SourceVersion  string    `json:"sourceVersion"`
	SourceAsOf     time.Time `json:"sourceAsOf"`
	PlayerADP      float64   `json:"playerAdp"`
	NextPickNumber int       `json:"nextPickNumber"`
}

// Candidate is one available player with both recommendation ranks:
// ValueRank by marginal value, RosterFitRank by roster fit (starting
// assignments added, active over reserve placement, slot scarcity, then
// marginal value).
type Candidate struct {
	PlayerKey     string         `json:"playerKey"`
	Name          string         `json:"name"`
	Eligible      []string       `json:"eligible"`
	NewsRisk      []string       `json:"newsRisk,omitempty"`
	Explanations  []string       `json:"explanations"`
	ValueRank     int            `json:"valueRank"`
	RosterFitRank int            `json:"rosterFitRank"`
	Reasons       NumericReasons `json:"reasons"`
	WaitRisk      *WaitRisk      `json:"waitRisk,omitempty"`
}

// Result is one evaluation: the versions of every input it read, the board
// status (BoardStatus* constants) and issues, the current pick and turn
// estimate when an order was supplied, the best-value and best-roster-fit
// recommendations, and every candidate.
type Result struct {
	RecommendationVersion string             `json:"recommendationVersion"`
	SessionVersion        uint64             `json:"sessionVersion"`
	RankingSnapshotID     string             `json:"rankingSnapshotId"`
	RankingIdentity       string             `json:"rankingIdentity"`
	RankingVersion        string             `json:"rankingVersion"`
	RankingAsOf           time.Time          `json:"rankingAsOf"`
	Scenario              draftrank.Scenario `json:"scenario"`
	ProjectionVersion     string             `json:"projectionVersion"`
	RulesVersion          string             `json:"rulesVersion"`
	NewsVersion           string             `json:"newsVersion,omitempty"`
	Strategy              Strategy           `json:"strategy"`
	Availability          AvailabilitySource `json:"availability"`
	ADP                   ADPSource          `json:"adp,omitempty"`
	CurrentPick           *PickSlot          `json:"currentPick,omitempty"`
	PicksUntilNextTurn    *int               `json:"picksUntilNextTurn,omitempty"`
	BoardStatus           string             `json:"boardStatus"`
	Issues                Issues             `json:"issues,omitempty"`
	BestValue             *Candidate         `json:"bestValue,omitempty"`
	BestRosterFit         *Candidate         `json:"bestRosterFit,omitempty"`
	Candidates            []Candidate        `json:"candidates"`
	GeneratedAt           time.Time          `json:"generatedAt"`
	LatencyMillis         int64              `json:"latencyMillis"`
}

// ReplayCase is a stored result with the player the drafter actually
// selected.
type ReplayCase struct {
	Result            Result `json:"result"`
	SelectedPlayerKey string `json:"selectedPlayerKey"`
	// ShownRecommendationKey identifies the one recommendation actually
	// presented to the drafter. Empty retains legacy best-value semantics.
	ShownRecommendationKey string `json:"shownRecommendationKey,omitempty"`
}

// ReplayMetrics summarizes EvaluateReplay: how many selections matched the
// recommendation shown (Correct of Cases, as the Correctness fraction), and
// the median and 95th-percentile evaluation latency.
type ReplayMetrics struct {
	Cases        int     `json:"cases"`
	Correct      int     `json:"correct"`
	Correctness  float64 `json:"correctness"`
	P50LatencyMS int64   `json:"p50LatencyMs"`
	P95LatencyMS int64   `json:"p95LatencyMs"`
}
