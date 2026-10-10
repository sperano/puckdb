package graph

import (
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/graph/model"
)

// Conversions from draftrank values to GraphQL models. They copy values
// unchanged: the API returns exactly what the shared service computed.

func gqlScenario(s draftrank.Scenario) model.DraftScenario {
	return model.DraftScenario(strings.ToUpper(string(s)))
}

func gqlScenarios[S ~string](scenarios []S) []model.DraftScenario {
	out := make([]model.DraftScenario, 0, len(scenarios))
	for _, s := range scenarios {
		out = append(out, gqlScenario(draftrank.Scenario(s)))
	}
	return out
}

func gqlIssues(issues []draftrank.Issue) []*model.DraftIssue {
	out := make([]*model.DraftIssue, 0, len(issues))
	for _, issue := range issues {
		out = append(out, &model.DraftIssue{Code: model.DraftIssueCode(issue.Code), Message: issue.Message})
	}
	return out
}

// issueMessages renders board and recommendation issues for the Maurice
// board's string lists. Their codes include ones outside the DraftIssueCode
// enum, so they are not exposed as DraftIssue.
func issueMessages(issues []draftrank.Issue) []string {
	out := make([]string, 0, len(issues))
	for _, issue := range issues {
		out = append(out, issue.Message)
	}
	return out
}

func gqlPage(page draftrank.Page, now time.Time) *model.DraftRankingsPage {
	out := &model.DraftRankingsPage{
		League: gqlLeague(page.League), Status: model.DraftRankingStatus(page.Status),
		Snapshot: gqlSnapshot(page.Snapshot), LatestSnapshotID: optionalUUID(page.LatestSnapshotID),
		Refresh: gqlRefresh(page.Refresh), Issues: gqlIssues(page.Issues),
		TotalCount: page.Total, Offset: page.Offset, Limit: page.Limit,
		Rows: make([]*model.DraftRankedPlayer, 0, len(page.Rows)),
	}
	if page.Scenario != "" {
		scenario := gqlScenario(page.Scenario)
		out.Scenario = &scenario
	}
	for _, row := range page.Rows {
		out.Rows = append(out.Rows, gqlRow(row, now))
	}
	return out
}

func gqlSummary(summary draftrank.LeagueSummary) *model.DraftLeagueSummary {
	return &model.DraftLeagueSummary{
		League: gqlLeague(summary.League), Status: model.DraftRankingStatus(summary.Status),
		Snapshot: gqlSnapshot(summary.Snapshot), Refresh: gqlRefresh(summary.Refresh), Issues: gqlIssues(summary.Issues),
	}
}

func gqlLeague(league draftrank.League) *model.DraftLeague {
	out := &model.DraftLeague{
		Season: league.Season, LeagueID: league.LeagueID, LeagueKey: league.LeagueKey, Name: league.Name,
		NumTeams: league.NumTeams, ScoringType: league.ScoringType, Format: optionalString(league.Format),
		Objective: optionalString(league.Objective), Provisional: league.Provisional, RulesSource: league.RulesSource,
		RulesHash: league.RulesHash, RulesFetchedAt: optionalTime(league.FetchedAt),
		Categories:  make([]*model.DraftCategory, 0, len(league.Categories)),
		RosterSlots: make([]*model.DraftRosterSlot, 0, len(league.RosterSlots)),
	}
	for _, c := range league.Categories {
		category := &model.DraftCategory{
			StatID: c.StatID, Abbr: c.Abbr, Name: c.Name, PositionTypes: nonNilStrings(c.PositionTypes), Direction: c.Direction,
		}
		if league.Format == string(draft.FormatPoints) {
			category.Weight = new(c.Weight)
		}
		out.Categories = append(out.Categories, category)
	}
	for _, slot := range league.RosterSlots {
		out.RosterSlots = append(out.RosterSlots, &model.DraftRosterSlot{
			Position: slot.Position, PositionType: optionalString(slot.PositionType), Count: slot.Count, Starting: slot.Starting,
		})
	}
	return out
}

func gqlSnapshot(info *draftrank.SnapshotInfo) *model.DraftSnapshot {
	if info == nil {
		return nil
	}
	out := &model.DraftSnapshot{
		ID: info.ID.String(), Identity: info.Identity, AsOf: info.AsOf, CreatedAt: info.CreatedAt,
		PoolSize: info.PoolSize, PoolFetchedAt: optionalTime(info.PoolFetchedAt),
		Projection: &model.DraftProjectionInfo{
			SnapshotID: info.Projection.SnapshotID.String(), ModelVersion: info.Projection.ModelVersion,
			SourceHash: info.Projection.SourceHash, AsOf: info.Projection.AsOf, DataThrough: optionalTime(info.Projection.DataThrough),
		},
		Options:     gqlOptions(info.Options),
		Assumptions: nonNilStrings(info.Assumptions), Scenarios: gqlScenarios(info.Scenarios),
		Unavailable: gqlIssues(info.Unavailable), News: make([]*model.DraftNewsSource, 0, len(info.News)),
	}
	if a := info.Adjustment; a != nil {
		out.Adjustment = &model.DraftAdjustmentInfo{
			RunID: a.RunID.String(), ID: a.ID, PolicyVersion: a.PolicyVersion, Calibration: a.Calibration,
			Alerts: nonNilStrings(a.Alerts), Warnings: nonNilStrings(a.Warnings),
		}
	}
	for _, s := range info.Scenarios {
		out.Versions = append(out.Versions, &model.DraftScenarioVersion{Scenario: gqlScenario(s), Version: info.Versions[s]})
	}
	for _, n := range info.News {
		out.News = append(out.News, &model.DraftNewsSource{
			SourceID: n.SourceID, Publisher: n.Publisher, Scope: n.Scope, Status: n.Status,
			DataAsOf: optionalTime(n.DataAsOf), LastSuccessAt: optionalTime(n.LastSuccessAt),
			ConsecutiveFailures: n.ConsecutiveFailures, LastError: optionalString(n.LastError),
		})
	}
	return out
}

func gqlOptions(o draftrank.Options) *model.DraftRankingOptions {
	out := &model.DraftRankingOptions{
		BenchPolicy: model.DraftBenchPolicy(strings.ToUpper(o.BenchPolicy)), UncertaintyPenalty: o.UncertaintyPenalty,
	}
	if o.WorkloadCapPolicy == string(draft.WorkloadCapsPerPlayer) {
		out.WorkloadCapPolicy = new(model.DraftWorkloadCapPolicyPerPlayer)
	}
	return out
}

func gqlRefresh(r *draftrank.Refresh) *model.DraftRefresh {
	if r == nil {
		return nil
	}
	out := &model.DraftRefresh{
		ID: r.ID.String(), RunID: r.RunID, State: model.DraftRefreshState(strings.ToUpper(string(r.State))),
		Error: optionalString(r.Error), SnapshotID: optionalUUID(r.SnapshotID), StartedAt: r.StartedAt,
		FinishedAt: optionalTime(r.FinishedAt),
	}
	if r.Code != "" {
		out.Code = new(model.DraftIssueCode(r.Code))
	}
	return out
}

func gqlRow(row draftrank.Row, now time.Time) *model.DraftRankedPlayer {
	p := row.Placement
	out := &model.DraftRankedPlayer{
		PlayerKey: row.PlayerKey, YahooPlayerID: row.YahooPlayerID, Name: row.Name, Team: row.Team,
		EligiblePositions: nonNilStrings(row.EligiblePositions), Status: optionalString(row.Status),
		StatusFull: optionalString(row.StatusFull), InjuryNote: optionalString(row.InjuryNote),
		OverallRank: p.OverallRank, PositionRank: row.PositionRank,
		PositionRanks: gqlPositionRanks(row.EligiblePositions, p.PositionRanks), Tier: p.Tier,
		OfficialScore: p.OfficialScore, AdjustedScore: p.AdjustedScore, ReplacementValue: p.ReplacementValue,
		Value: p.Value, AdjustedValue: p.AdjustedValue, Uncertainty: p.Uncertainty,
		BaselineRank: row.BaselineRank, RankChange: row.RankChange,
		Contributions: gqlContributions(p.Contributions), Explanations: nonNilStrings(p.Explanations),
		Adjustment: gqlAdjustment(row.Adjustment, now),
	}
	if row.NHLPlayerID != 0 {
		out.NhlPlayerID = new(row.NHLPlayerID)
	}
	for _, s := range draftrank.Scenarios {
		if placement, ranked := row.Placements[s]; ranked {
			out.Placements = append(out.Placements, gqlPlacement(row.EligiblePositions, placement))
		}
	}
	return out
}

func gqlPlacement(eligible []string, p draftrank.Placement) *model.DraftPlacement {
	return &model.DraftPlacement{
		Scenario: gqlScenario(p.Scenario), OverallRank: p.OverallRank, PositionRanks: gqlPositionRanks(eligible, p.PositionRanks),
		Tier: p.Tier, OfficialScore: p.OfficialScore, AdjustedScore: p.AdjustedScore, ReplacementValue: p.ReplacementValue,
		Value: p.Value, AdjustedValue: p.AdjustedValue, Uncertainty: p.Uncertainty,
		Contributions: gqlContributions(p.Contributions), Explanations: nonNilStrings(p.Explanations),
	}
}

// gqlPositionRanks lists ranks in the player's eligibility order.
func gqlPositionRanks(eligible []string, ranks map[string]int) []*model.DraftPositionRank {
	out := make([]*model.DraftPositionRank, 0, len(ranks))
	for _, position := range eligible {
		if rank, ranked := ranks[position]; ranked {
			out = append(out, &model.DraftPositionRank{Position: position, Rank: rank})
		}
	}
	return out
}

func gqlContributions(contributions []draftrank.Contribution) []*model.DraftContribution {
	out := make([]*model.DraftContribution, 0, len(contributions))
	for _, c := range contributions {
		out = append(out, &model.DraftContribution{
			StatID: c.StatID, Abbr: c.Abbr, Stat: c.Stat, Projected: c.Projected, Official: c.Official,
			Adjusted: c.Adjusted, Weight: c.Weight, Direction: c.Direction, Opportunity: c.Opportunity, Explanation: c.Explanation,
		})
	}
	return out
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func optionalUUID(id uuid.UUID) *string {
	if id == uuid.Nil {
		return nil
	}
	return new(id.String())
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return slices.Clone(values)
}
