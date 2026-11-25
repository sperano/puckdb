package graph

import (
	"context"

	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/mauricereport"
)

func (r *Resolver) mauriceReportingOverview(ctx context.Context, filter model.MauriceReportingConversationFilter) (*model.MauriceReportingOverview, error) {
	if r.MauriceReporting == nil {
		return nil, errMauriceReportingNotConfigured
	}
	result, err := r.MauriceReporting.Overview(ctx, reportingFilter(filter))
	if err != nil {
		return nil, clientReportingError("mauriceReportingOverview", err)
	}
	out := &model.MauriceReportingOverview{
		UserCount: result.Users, ActiveUserCount: result.ActiveUsers,
		Activity: reportingActivity(result.Activity),
		Models:   make([]*model.MauriceReportingModelUsage, len(result.Models)),
	}
	for i, m := range result.Models {
		out.Models[i] = &model.MauriceReportingModelUsage{
			Provider: m.Model.Provider, Model: m.Model.Model, Turns: m.Turns, Usage: reportingUsage(m.Usage),
		}
	}
	return out, nil
}

func (r *Resolver) mauriceReportingUserActivity(ctx context.Context, filter model.MauriceReportingConversationFilter, limit *int, cursor *string) (*model.MauriceReportingUserActivityPage, error) {
	if r.MauriceReporting == nil {
		return nil, errMauriceReportingNotConfigured
	}
	page, err := reportingPage(limit, cursor)
	if err != nil {
		return nil, err
	}
	result, err := r.MauriceReporting.UserActivity(ctx, reportingFilter(filter), page)
	if err != nil {
		return nil, clientReportingError("mauriceReportingUserActivity", err)
	}
	out := &model.MauriceReportingUserActivityPage{
		Items:      make([]*model.MauriceReportingUserActivity, len(result.Users)),
		NextCursor: optionalString(result.NextCursor),
	}
	for i, u := range result.Users {
		out.Items[i] = &model.MauriceReportingUserActivity{
			User: reportingUser(u.User), Active: u.Active, Activity: reportingActivity(u.Activity),
		}
	}
	return out, nil
}

func reportingActivity(a mauricereport.Activity) *model.MauriceReportingActivity {
	return &model.MauriceReportingActivity{
		Conversations:      a.Conversations,
		Turns:              reportingTurnCounts(a.Turns),
		FirstTurnStartedAt: reportingTimePtr(a.FirstTurnStartedAt),
		LastTurnStartedAt:  reportingTimePtr(a.LastTurnStartedAt),
		TurnLatency:        reportingLatency(a.TurnLatency),
		Usage:              reportingUsage(a.Usage),
		TitleGeneration:    reportingUsage(a.TitleGeneration),
		ProcessingSeconds:  a.ProcessingSeconds,
	}
}

func reportingUsage(u mauricereport.Usage) *model.MauriceReportingUsage {
	return &model.MauriceReportingUsage{
		Calls:                    u.Calls,
		FailedCalls:              u.FailedCalls,
		InputTokens:              reportingTokens(u.InputTokens),
		OutputTokens:             reportingTokens(u.OutputTokens),
		CacheCreationInputTokens: reportingTokens(u.CacheCreationInputTokens),
		CacheReadInputTokens:     reportingTokens(u.CacheReadInputTokens),
		CallLatency:              reportingLatency(u.CallLatency),
	}
}

func reportingTokens(c mauricereport.TokenCount) *model.MauriceReportingTokenCount {
	return &model.MauriceReportingTokenCount{
		Total: c.Total(), ReportedCalls: c.ReportedCalls, UnreportedCalls: c.UnreportedCalls, Complete: c.Complete(),
	}
}

func reportingLatency(l mauricereport.Latency) *model.MauriceReportingLatency {
	return &model.MauriceReportingLatency{
		Count: l.Count, MeanSeconds: l.Mean, P50Seconds: l.P50, P95Seconds: l.P95, MaxSeconds: l.Max,
	}
}
