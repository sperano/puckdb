package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/mauricereport"
)

// MauriceReportingReader serves the @mauriceReporting fields
// (mauricereport.PGStore in production).
type MauriceReportingReader interface {
	Instances(ctx context.Context, page mauricereport.PageRequest) (mauricereport.InstancePage, error)
	Users(ctx context.Context, instance string, page mauricereport.PageRequest) (mauricereport.UserPage, error)
	Conversations(ctx context.Context, filter mauricereport.ConversationFilter, page mauricereport.PageRequest) (mauricereport.ConversationPage, error)
	Conversation(ctx context.Context, instance, id string, includeDeleted bool) (mauricereport.Conversation, bool, error)
	Overview(ctx context.Context, filter mauricereport.ConversationFilter) (mauricereport.Overview, error)
	UserActivity(ctx context.Context, filter mauricereport.ConversationFilter, page mauricereport.PageRequest) (mauricereport.UserActivityPage, error)
	MauriceReportingDetailReader
}

// errMauriceReportingNotConfigured is returned when the resolver has no
// reporting reader (no database).
var errMauriceReportingNotConfigured = errors.New("maurice reporting is not configured")

// reportingClientErrors are the reporting errors a client can act on,
// returned as they are; they name only the client's own input.
var reportingClientErrors = []error{
	mauricereport.ErrInvalidArgument,
	mauricereport.ErrInvalidCursor,
	mauricereport.ErrUserNotInInstance,
}

// clientReportingError returns err when it is one of reportingClientErrors
// or the request's own end, and otherwise logs it and returns
// errMauriceInternal, so no SQL or driver text reaches the client.
func clientReportingError(op string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	for _, known := range reportingClientErrors {
		if errors.Is(err, known) {
			return err
		}
	}
	log.Error().Err(err).Str("operation", op).Msg("maurice reporting request failed")
	return errMauriceInternal
}

// reportingPage converts the GraphQL paging arguments: an omitted limit is
// the default, an explicit one must be 1..MaxLimit.
func reportingPage(limit *int, cursor *string) (mauricereport.PageRequest, error) {
	var page mauricereport.PageRequest
	if limit != nil {
		if *limit < 1 {
			return page, fmt.Errorf("%w: limit %d is outside 1..%d", mauricereport.ErrInvalidArgument, *limit, mauricereport.MaxLimit)
		}
		page.Limit = *limit
	}
	if cursor != nil {
		page.Cursor = *cursor
	}
	return page, nil
}

func (r *Resolver) mauriceReportingInstances(ctx context.Context, limit *int, cursor *string) (*model.MauriceReportingInstancePage, error) {
	if r.MauriceReporting == nil {
		return nil, errMauriceReportingNotConfigured
	}
	page, err := reportingPage(limit, cursor)
	if err != nil {
		return nil, err
	}
	result, err := r.MauriceReporting.Instances(ctx, page)
	if err != nil {
		return nil, clientReportingError("mauriceReportingInstances", err)
	}
	out := &model.MauriceReportingInstancePage{
		Items:      make([]*model.MauriceReportingInstance, len(result.Instances)),
		NextCursor: optionalString(result.NextCursor),
	}
	for i, instance := range result.Instances {
		out.Items[i] = &model.MauriceReportingInstance{Name: instance.Name, UserCount: int(instance.UserCount)}
	}
	return out, nil
}

func (r *Resolver) mauriceReportingUsers(ctx context.Context, instance string, limit *int, cursor *string) (*model.MauriceReportingUserPage, error) {
	if r.MauriceReporting == nil {
		return nil, errMauriceReportingNotConfigured
	}
	page, err := reportingPage(limit, cursor)
	if err != nil {
		return nil, err
	}
	result, err := r.MauriceReporting.Users(ctx, instance, page)
	if err != nil {
		return nil, clientReportingError("mauriceReportingUsers", err)
	}
	out := &model.MauriceReportingUserPage{
		Items:      make([]*model.MauriceReportingUser, len(result.Users)),
		NextCursor: optionalString(result.NextCursor),
	}
	for i, u := range result.Users {
		out.Items[i] = reportingUser(u)
	}
	return out, nil
}

func reportingUser(u mauricereport.User) *model.MauriceReportingUser {
	return &model.MauriceReportingUser{
		ID: u.ID, Instance: u.Instance, AuthProvider: u.AuthProvider, Username: u.Username,
		DisplayName: u.DisplayName, Email: u.Email, FirstSeenAt: u.FirstSeenAt,
		ProfileUpdatedAt: u.ProfileUpdatedAt, DisabledAt: u.DisabledAt,
	}
}

func (r *Resolver) mauriceReportingConversations(ctx context.Context, filter model.MauriceReportingConversationFilter, limit *int, cursor *string) (*model.MauriceReportingConversationPage, error) {
	if r.MauriceReporting == nil {
		return nil, errMauriceReportingNotConfigured
	}
	page, err := reportingPage(limit, cursor)
	if err != nil {
		return nil, err
	}
	result, err := r.MauriceReporting.Conversations(ctx, reportingFilter(filter), page)
	if err != nil {
		return nil, clientReportingError("mauriceReportingConversations", err)
	}
	out := &model.MauriceReportingConversationPage{
		Items:      make([]*model.MauriceReportingConversation, len(result.Conversations)),
		NextCursor: optionalString(result.NextCursor),
	}
	for i, c := range result.Conversations {
		out.Items[i] = reportingConversation(c)
	}
	return out, nil
}

func (r *Resolver) mauriceReportingConversation(ctx context.Context, instance, id string, includeDeleted *bool) (*model.MauriceReportingConversation, error) {
	if r.MauriceReporting == nil {
		return nil, errMauriceReportingNotConfigured
	}
	c, found, err := r.MauriceReporting.Conversation(ctx, instance, id, includeDeleted != nil && *includeDeleted)
	if err != nil {
		return nil, clientReportingError("mauriceReportingConversation", err)
	}
	if !found {
		return nil, nil
	}
	return reportingConversation(c), nil
}

// reportingFilter converts the GraphQL filter; an omitted field is the zero
// value, which does not filter.
func reportingFilter(f model.MauriceReportingConversationFilter) mauricereport.ConversationFilter {
	out := mauricereport.ConversationFilter{
		Instance:       f.Instance,
		UserID:         derefString(f.UserID),
		From:           derefString(f.From),
		To:             derefString(f.To),
		Provider:       derefString(f.Provider),
		Model:          derefString(f.Model),
		ToolName:       derefString(f.ToolName),
		Search:         derefString(f.Search),
		IncludeDeleted: f.IncludeDeleted != nil && *f.IncludeDeleted,
	}
	for _, s := range f.Statuses {
		out.Statuses = append(out.Statuses, mauricereport.TurnStatus(strings.ToLower(string(s))))
	}
	return out
}

func reportingConversation(c mauricereport.Conversation) *model.MauriceReportingConversation {
	out := &model.MauriceReportingConversation{
		ID: c.ID, Instance: c.Instance, UserID: c.UserID, Username: c.Username, Title: c.Title,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, DeletedAt: c.DeletedAt,
		LastTurnStartedAt: c.LastTurnStartedAt,
		Turns:             reportingTurnCounts(c.Turns),
		Models:            make([]*model.MauriceReportingModel, len(c.Models)),
	}
	if c.Status != nil {
		status := model.MauriceReportingTurnStatus(strings.ToUpper(string(*c.Status)))
		out.Status = &status
	}
	for i, m := range c.Models {
		out.Models[i] = &model.MauriceReportingModel{Provider: m.Provider, Model: m.Model}
	}
	return out
}

// reportingTurnCounts converts turn counts and derives the outcome rates,
// over the terminal turns.
func reportingTurnCounts(c mauricereport.TurnCounts) *model.MauriceReportingTurnCounts {
	return &model.MauriceReportingTurnCounts{
		Total: int(c.Total()), Running: int(c.Running), Succeeded: int(c.Succeeded),
		Failed: int(c.Failed), Cancelled: int(c.Cancelled), Terminal: int(c.Terminal()),
		SucceededRate: c.Rate(c.Succeeded), FailedRate: c.Rate(c.Failed), CancelledRate: c.Rate(c.Cancelled),
	}
}
