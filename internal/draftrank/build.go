package draftrank

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/newsadjust"
	"github.com/sperano/puckdb/internal/projection"
)

// SnapshotVersion identifies how snapshots are assembled from rankings.
const SnapshotVersion = "draft-rankings-v1"

// BuildInput is everything one snapshot is ranked from. Adjustment is nil
// when news adjustments were unavailable; Unavailable then says why.
type BuildInput struct {
	Rules           draft.Snapshot
	Pool            []draft.PoolPlayer
	Baseline        projection.Snapshot
	BaselineID      uuid.UUID
	Adjustment      *newsadjust.Result
	AdjustmentRunID uuid.UUID
	// AdjustmentWarnings are stored events that could not be loaded.
	AdjustmentWarnings []string
	Options            Options
	News               []news.SourceCoverage
	Unavailable        []Issue
	AsOf               time.Time
	// PoolNotes are provenance notes from draft.LoadLeaguePool, e.g. a
	// TEMPORARY stand-in pool's roster season and excluded players (see
	// draft.StandInPoolResult.Notes). Surfaced in Meta.Assumptions like any
	// other explicit ranking assumption.
	PoolNotes []string
}

// Build ranks the baseline and every news scenario with draft.BuildRanking
// under the same rules, pool and options, and assembles the snapshot.
func Build(in BuildInput) (Snapshot, error) {
	scoring, err := draft.ScoringFor(in.Rules)
	if err != nil {
		return Snapshot{}, refreshError(IssueUnsupportedScoring, err)
	}
	if len(in.Pool) == 0 {
		return Snapshot{}, refreshError(IssueMissingPool, fmt.Errorf("league %s has no draftable players", in.Rules.Rules.LeagueKey))
	}
	rankings, err := rankScenarios(in)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{SnapshotInfo: SnapshotInfo{AsOf: in.AsOf.UTC(), Meta: buildMeta(in, scoring, rankings)}}
	snapshot.Players = buildPlayers(in, scoring, rankings)
	snapshot.Identity, err = snapshotIdentity(snapshot.Meta)
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func rankScenarios(in BuildInput) (map[Scenario]draft.RankingSnapshot, error) {
	options := in.Options.RankingOptions()
	baseline, err := draft.BuildRanking(in.Rules, in.Baseline, in.Pool, options)
	if err != nil {
		return nil, refreshError(IssueRankingFailed, fmt.Errorf("baseline ranking: %w", err))
	}
	rankings := map[Scenario]draft.RankingSnapshot{ScenarioBaseline: baseline}
	if in.Adjustment == nil {
		return rankings, nil
	}
	adjustment := in.Adjustment
	if adjustment.BaselineHash != in.Baseline.SourceDataHash {
		return nil, refreshError(IssueInternalError, fmt.Errorf("adjustment %s was built from another baseline", adjustment.ID))
	}
	if adjustment.LeagueKey != in.Rules.Rules.LeagueKey {
		return nil, refreshError(IssueInternalError, fmt.Errorf("adjustment %s belongs to league %q", adjustment.ID, adjustment.LeagueKey))
	}
	for _, s := range newsadjust.Scenarios {
		projected, exists := adjustment.Snapshots[s]
		if !exists {
			return nil, refreshError(IssueInternalError, fmt.Errorf("adjustment %s has no %s snapshot", adjustment.ID, s))
		}
		ranking, err := draft.BuildRanking(in.Rules, projected, in.Pool, options)
		if err != nil {
			return nil, refreshError(IssueRankingFailed, fmt.Errorf("%s ranking: %w", s, err))
		}
		rankings[Scenario(s)] = ranking
	}
	return rankings, nil
}

func buildMeta(in BuildInput, scoring draft.Scoring, rankings map[Scenario]draft.RankingSnapshot) Meta {
	meta := Meta{
		League:   leagueOf(in.Rules, scoring, rankings[ScenarioBaseline].RuleHash),
		PoolSize: len(in.Pool), PoolFetchedAt: oldestFetch(in.Pool),
		Projection: ProjectionInfo{
			SnapshotID: in.BaselineID, ModelVersion: in.Baseline.Config.ModelVersion,
			SourceHash: in.Baseline.SourceDataHash, AsOf: in.Baseline.AsOf.UTC(), DataThrough: in.Baseline.SourceMaxGameDate,
		},
		Options:     in.Options,
		Assumptions: slices.Clone(rankings[ScenarioBaseline].Assumptions),
		Versions:    make(map[Scenario]string, len(rankings)),
		News:        freshnessOf(in.News),
		Unavailable: slices.Clone(in.Unavailable),
	}
	meta.Assumptions = append(meta.Assumptions, in.PoolNotes...)
	for _, s := range Scenarios {
		if ranking, exists := rankings[s]; exists {
			meta.Versions[s] = ranking.Version
			meta.Scenarios = append(meta.Scenarios, s)
		}
	}
	if a := in.Adjustment; a != nil {
		meta.Adjustment = &AdjustmentInfo{
			RunID: in.AdjustmentRunID, ID: a.ID, PolicyVersion: a.Policy.Version,
			Calibration: a.Policy.Calibration, Alerts: slices.Clone(a.Alerts), Warnings: slices.Clone(in.AdjustmentWarnings),
		}
		meta.Assumptions = append(meta.Assumptions, "news assumptions: "+a.Policy.Calibration)
	}
	return meta
}

// LeagueOf describes a league from its rules; a league whose scoring is
// unsupported still gets its name, size and categories.
func LeagueOf(rules draft.Snapshot) League {
	scoring, err := draft.ScoringFor(rules)
	if err != nil {
		return leagueOf(rules, draft.Scoring{Provisional: rules.Source != draft.SourceYahooAPI}, rulesHash(rules))
	}
	return leagueOf(rules, scoring, rulesHash(rules))
}

// rulesHash is the stored hash of the rules, or their canonical hash when
// the snapshot carries none; the two agree (BuildRanking checks it).
func rulesHash(rules draft.Snapshot) string {
	if rules.Hash != "" {
		return rules.Hash
	}
	hash, _, err := rules.Rules.Hash()
	if err != nil {
		return ""
	}
	return hash
}

func leagueOf(rules draft.Snapshot, scoring draft.Scoring, hash string) League {
	league := League{
		Season: rules.Rules.Season, LeagueID: rules.Rules.LeagueID, LeagueKey: rules.Rules.LeagueKey,
		Name: rules.Rules.Name, NumTeams: rules.Rules.NumTeams, ScoringType: rules.Rules.ScoringType,
		Format: string(scoring.Format), Objective: string(scoring.Objective), Provisional: scoring.Provisional,
		RulesSource: string(rules.Source), RulesHash: hash, FetchedAt: rules.FetchedAt.UTC(),
		Categories: []Category{}, RosterSlots: append([]draft.RosterSlot{}, rules.Rules.RosterSlots...),
	}
	stats := slices.Clone(scoring.Stats)
	slices.SortFunc(stats, func(a, b draft.ScoringStat) int { return a.StatID - b.StatID })
	for _, stat := range stats {
		league.Categories = append(league.Categories, Category{
			StatID: stat.StatID, Abbr: stat.Abbr, Name: stat.Name, PositionTypes: slices.Clone(stat.PositionTypes),
			Direction: string(stat.Direction), Weight: stat.Weight,
		})
	}
	return league
}

func oldestFetch(pool []draft.PoolPlayer) time.Time {
	var oldest time.Time
	for _, player := range pool {
		if oldest.IsZero() || player.FetchedAt.Before(oldest) {
			oldest = player.FetchedAt
		}
	}
	return oldest.UTC()
}

func freshnessOf(coverage []news.SourceCoverage) []SourceFreshness {
	out := make([]SourceFreshness, 0, len(coverage))
	for _, c := range coverage {
		out = append(out, SourceFreshness{
			SourceID: c.Source.ID, Publisher: c.Source.Publisher, Scope: c.Scope, Status: string(c.Status),
			DataAsOf: c.AsOf.UTC(), LastSuccessAt: c.State.LastSuccessAt.UTC(),
			ConsecutiveFailures: c.State.ConsecutiveFailures, LastError: c.State.LastError,
		})
	}
	slices.SortFunc(out, func(a, b SourceFreshness) int {
		return strings.Compare(a.SourceID+"\x00"+a.Scope, b.SourceID+"\x00"+b.Scope)
	})
	return out
}

func buildPlayers(in BuildInput, scoring draft.Scoring, rankings map[Scenario]draft.RankingSnapshot) []Player {
	abbrs := make(map[int]string, len(scoring.Stats))
	for _, stat := range scoring.Stats {
		abbrs[stat.StatID] = stat.Abbr
	}
	placements := make(map[string]map[Scenario]Placement, len(in.Pool))
	for scenario, ranking := range rankings {
		for _, ranked := range ranking.Players {
			if placements[ranked.PlayerKey] == nil {
				placements[ranked.PlayerKey] = make(map[Scenario]Placement, len(rankings))
			}
			placements[ranked.PlayerKey][scenario] = placementOf(scenario, ranked, abbrs)
		}
	}
	eligible := make(map[string][]string, len(in.Pool))
	for _, ranked := range rankings[ScenarioBaseline].Players {
		eligible[ranked.PlayerKey] = slices.Clone(ranked.EligiblePositions)
	}
	adjustments := make(map[string]*newsadjust.PlayerAdjustment)
	if in.Adjustment != nil {
		for i := range in.Adjustment.Players {
			adjustments[in.Adjustment.Players[i].PlayerKey] = &in.Adjustment.Players[i]
		}
	}
	players := make([]Player, 0, len(in.Pool))
	for _, p := range in.Pool {
		players = append(players, Player{
			PlayerKey: p.PlayerKey, YahooPlayerID: p.YahooPlayerID, NHLPlayerID: p.NHLPlayerID,
			Name: p.Name, Team: p.Team, EligiblePositions: eligible[p.PlayerKey],
			Status: p.Status, StatusFull: p.StatusFull, InjuryNote: p.InjuryNote,
			Placements: placements[p.PlayerKey], Adjustment: adjustments[p.PlayerKey],
		})
	}
	slices.SortFunc(players, func(a, b Player) int {
		return a.Placements[ScenarioBaseline].OverallRank - b.Placements[ScenarioBaseline].OverallRank
	})
	return players
}

func placementOf(scenario Scenario, ranked draft.RankedPlayer, abbrs map[int]string) Placement {
	contributions := make([]Contribution, 0, len(ranked.Contributions))
	for _, c := range ranked.Contributions {
		contributions = append(contributions, Contribution{
			StatID: c.StatID, Abbr: abbrs[c.StatID], Stat: string(c.Stat), Projected: c.Projected,
			Official: c.Official, Adjusted: c.Adjusted, Weight: c.Weight, Direction: string(c.Direction),
			Opportunity: c.Opportunity, Explanation: c.Explanation,
		})
	}
	ranks := make(map[string]int, len(ranked.PositionRanks))
	for position, rank := range ranked.PositionRanks {
		ranks[position] = rank
	}
	return Placement{
		Scenario: scenario, OverallRank: ranked.OverallRank, PositionRanks: ranks, Tier: ranked.Tier,
		OfficialScore: ranked.OfficialScore, AdjustedScore: ranked.AdjustedScore,
		ReplacementValue: ranked.BaselineValue, Value: ranked.Value, AdjustedValue: ranked.AdjustedValue,
		Uncertainty: ranked.Uncertainty, Contributions: contributions, Explanations: slices.Clone(ranked.Explanations),
	}
}

// snapshotIdentity hashes what the snapshot's values depend on: the
// ranking version of every scenario (which covers rules, projections, pool
// and options) and the adjustment identity.
func snapshotIdentity(meta Meta) (string, error) {
	adjustmentID := ""
	if meta.Adjustment != nil {
		adjustmentID = meta.Adjustment.ID
	}
	data, err := json.Marshal(struct {
		Version      string
		Versions     map[Scenario]string
		AdjustmentID string
	}{SnapshotVersion, meta.Versions, adjustmentID})
	if err != nil {
		return "", errors.Join(errors.New("encode snapshot identity"), err)
	}
	digest := sha256.Sum256(data)
	return SnapshotVersion + ":" + hex.EncodeToString(digest[:]), nil
}
