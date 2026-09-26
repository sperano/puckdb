package graph

import (
	"strings"
	"time"

	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/newsadjust"
)

// gqlAdjustment converts a player's news adjustment: every reason with its
// evidence, the effect and changed stats per scenario, and the overrides
// with the values they replaced (and their state at now).
func gqlAdjustment(a *newsadjust.PlayerAdjustment, now time.Time) *model.DraftAdjustment {
	if a == nil {
		return nil
	}
	out := &model.DraftAdjustment{
		Reasons: make([]*model.DraftNewsReason, 0, len(a.Reasons)), Effects: make([]*model.DraftScenarioEffect, 0, len(a.Effects)),
		Changes: make([]*model.DraftStatChange, 0, len(a.Changes)), Overrides: make([]*model.DraftAppliedOverride, 0, len(a.Overrides)),
		Assumptions: nonNilStrings(a.Assumptions), Alerts: nonNilStrings(a.Alerts),
		BaselineUncertainty: a.BaselineUncertainty, Uncertainty: a.Uncertainty,
	}
	for _, reason := range a.Reasons {
		out.Reasons = append(out.Reasons, gqlReason(reason))
	}
	for _, s := range newsadjust.Scenarios {
		if effect, exists := a.Effects[s]; exists {
			out.Effects = append(out.Effects, &model.DraftScenarioEffect{
				Scenario: gqlScenario(draftrank.Scenario(s)), MissedGames: effect.MissedGames, Availability: effect.Availability,
				GamesFactor: effect.GamesFactor, IceTimeFactor: effect.IceTime, PowerPlayFactor: effect.PowerPlay,
				GoalieStartsFactor: effect.GoalieStarts, TeamID: effect.TeamID,
			})
		}
	}
	for _, change := range a.Changes {
		out.Changes = append(out.Changes, gqlStatChange(change))
	}
	for _, applied := range a.Overrides {
		out.Overrides = append(out.Overrides, &model.DraftAppliedOverride{
			Override: gqlOverride(draftrank.OverrideStatus{Override: applied.Override, State: draftrank.OverrideStateAt(applied.Override, now)}),
			Scenario: gqlScenario(draftrank.Scenario(applied.Scenario)), Original: applied.Original, Value: applied.Value,
		})
	}
	return out
}

func gqlReason(r newsadjust.Reason) *model.DraftNewsReason {
	out := &model.DraftNewsReason{
		EventID: r.EventID, Version: r.Version, Type: string(r.Type), Status: string(r.Status),
		DurationKind: string(r.Duration.Kind), EffectiveFrom: r.EffectiveFrom, EffectiveUntil: optionalTime(r.EffectiveUntil),
		Outcome: string(r.Outcome), Detail: r.Detail, Scenarios: gqlScenarios(r.Scenarios),
		Evidence: make([]*model.DraftEvidence, 0, len(r.Evidence)), LatestEvidenceAt: optionalTime(r.LatestEvidenceAt), AgeHours: r.AgeHours,
	}
	if r.IncidentID != 0 {
		out.IncidentID = new(r.IncidentID)
	}
	if r.Duration.Games != 0 {
		out.DurationGames = new(r.Duration.Games)
	}
	for _, e := range r.Evidence {
		out.Evidence = append(out.Evidence, &model.DraftEvidence{
			VersionID: e.VersionID, Publisher: e.Publisher, Kind: string(e.Kind), URL: optionalString(e.URL),
			ReportedAt: e.ReportedAt, RetrievedAt: e.RetrievedAt, Quote: optionalString(e.Quote),
		})
	}
	return out
}

func gqlStatChange(change newsadjust.StatChange) *model.DraftStatChange {
	out := &model.DraftStatChange{Stat: string(change.Stat), Baseline: &model.DraftStatEstimate{
		Mean: change.Baseline.Mean, Low: change.Baseline.Low, High: change.Baseline.High,
	}}
	for _, s := range newsadjust.Scenarios {
		if estimate, exists := change.Adjusted[s]; exists {
			out.Adjusted = append(out.Adjusted, &model.DraftScenarioEstimate{
				Scenario: gqlScenario(draftrank.Scenario(s)),
				Estimate: &model.DraftStatEstimate{Mean: estimate.Mean, Low: estimate.Low, High: estimate.High},
			})
		}
	}
	return out
}

func gqlOverride(o draftrank.OverrideStatus) *model.DraftOverride {
	out := &model.DraftOverride{
		ID: o.ID, PlayerKey: o.PlayerKey, LeagueKey: optionalString(o.LeagueKey),
		Kind: model.DraftOverrideKind(strings.ToUpper(string(o.Kind))), EventID: optionalString(o.EventID),
		Value: o.Value, Reason: o.Reason, CreatedBy: optionalString(o.CreatedBy), CreatedAt: o.CreatedAt,
		ExpiresAt: optionalTime(o.ExpiresAt), ResetAt: optionalTime(o.ResetAt), ResetReason: optionalString(o.ResetReason),
		State: model.DraftOverrideState(strings.ToUpper(string(o.State))),
	}
	if o.Scenario != "" {
		out.Scenario = new(gqlScenario(draftrank.Scenario(o.Scenario)))
	}
	if o.Input != "" {
		out.Input = new(model.DraftOverrideInput(strings.ToUpper(string(o.Input))))
	}
	return out
}

// overrideFromInput converts a create request; enums map to the stored
// lower-case names.
func overrideFromInput(input model.DraftOverrideCreateInput) newsadjust.Override {
	o := newsadjust.Override{
		PlayerKey: strings.TrimSpace(input.PlayerKey), Kind: newsadjust.OverrideKind(strings.ToLower(string(input.Kind))),
		Reason: input.Reason,
	}
	if input.LeagueKey != nil {
		o.LeagueKey = strings.TrimSpace(*input.LeagueKey)
	}
	if input.EventID != nil {
		o.EventID = *input.EventID
	}
	if input.Scenario != nil {
		o.Scenario = newsadjust.Scenario(strings.ToLower(string(*input.Scenario)))
	}
	if input.Input != nil {
		o.Input = newsadjust.Input(strings.ToLower(string(*input.Input)))
	}
	if input.Value != nil {
		o.Value = *input.Value
	}
	if input.ExpiresAt != nil {
		o.ExpiresAt = input.ExpiresAt.UTC()
	}
	return o
}
