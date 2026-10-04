// Package draftrecommend evaluates a fixed draft session and ranking snapshot.
// It is deterministic and does not call Yahoo, news services, or an LLM.
package draftrecommend

import (
	"time"

	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/draftsession"
)

const RecommendationVersion = "draft-recommendation-v1"

// PickSlot is one pick in the verified chronological draft order. Supplying
// every pick captures snake, linear, and traded-pick formats without guessing.
type PickSlot struct {
	Key    draftsession.PickKey `json:"key"`
	TeamID int                  `json:"teamId"`
}

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

type CategoryReason struct {
	StatID int     `json:"statId"`
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
}

type WaitRisk struct {
	Kind           string    `json:"kind"`
	Source         string    `json:"source"`
	SourceVersion  string    `json:"sourceVersion"`
	SourceAsOf     time.Time `json:"sourceAsOf"`
	PlayerADP      float64   `json:"playerAdp"`
	NextPickNumber int       `json:"nextPickNumber"`
}

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
	Issues                []string           `json:"issues,omitempty"`
	BestValue             *Candidate         `json:"bestValue,omitempty"`
	BestRosterFit         *Candidate         `json:"bestRosterFit,omitempty"`
	Candidates            []Candidate        `json:"candidates"`
	GeneratedAt           time.Time          `json:"generatedAt"`
	LatencyMillis         int64              `json:"latencyMillis"`
}

type ReplayCase struct {
	Result            Result `json:"result"`
	SelectedPlayerKey string `json:"selectedPlayerKey"`
}

type ReplayMetrics struct {
	Cases        int     `json:"cases"`
	Correct      int     `json:"correct"`
	Correctness  float64 `json:"correctness"`
	P50LatencyMS int64   `json:"p50LatencyMs"`
	P95LatencyMS int64   `json:"p95LatencyMs"`
}
