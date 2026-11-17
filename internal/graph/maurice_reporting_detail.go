package graph

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/mauricereport"
)

// MauriceReportingDetailReader serves the conversation detail fields; each
// read reports found false for a conversation, or a nested ID, outside the
// reference's scope.
type MauriceReportingDetailReader interface {
	Transcript(ctx context.Context, ref mauricereport.ConversationRef, page mauricereport.PageRequest) (mauricereport.MessagePage, bool, error)
	Turns(ctx context.Context, ref mauricereport.ConversationRef, page mauricereport.PageRequest) (mauricereport.TurnPage, bool, error)
	TurnCalls(ctx context.Context, ref mauricereport.ConversationRef, turnID string, page mauricereport.PageRequest) (mauricereport.CallPage, bool, error)
	TitleCalls(ctx context.Context, ref mauricereport.ConversationRef, page mauricereport.PageRequest) (mauricereport.CallPage, bool, error)
	ToolCalls(ctx context.Context, ref mauricereport.ConversationRef, turnID string, page mauricereport.PageRequest) (mauricereport.ToolCallPage, bool, error)
	CallInput(ctx context.Context, ref mauricereport.ConversationRef, callID string, page mauricereport.PageRequest) (mauricereport.CallInput, bool, error)
	ToolCall(ctx context.Context, ref mauricereport.ConversationRef, toolCallID string) (mauricereport.ToolCallDetail, bool, error)
	Payload(ctx context.Context, ref mauricereport.ConversationRef, req mauricereport.PayloadRequest) (mauricereport.Text, bool, error)
}

// detailRead runs one paged detail read for operation op: it checks the
// reader and the paging arguments, maps errors as the other reporting
// fields do, and converts a found result; nil when not found.
func detailRead[T, Out any](r *Resolver, op string, limit *int, cursor *string,
	read func(mauricereport.PageRequest) (T, bool, error), convert func(T) *Out) (*Out, error) {
	if r.MauriceReporting == nil {
		return nil, errMauriceReportingNotConfigured
	}
	page, err := reportingPage(limit, cursor)
	if err != nil {
		return nil, err
	}
	result, found, err := read(page)
	if err != nil {
		return nil, clientReportingError(op, err)
	}
	if !found {
		return nil, nil
	}
	return convert(result), nil
}

func reportingRef(ref model.MauriceReportingConversationRef) mauricereport.ConversationRef {
	return mauricereport.ConversationRef{
		Instance: ref.Instance, ConversationID: ref.ID,
		IncludeDeleted: ref.IncludeDeleted != nil && *ref.IncludeDeleted,
	}
}

func (r *Resolver) mauriceReportingTranscript(ctx context.Context, ref model.MauriceReportingConversationRef, limit *int, cursor *string) (*model.MauriceReportingMessagePage, error) {
	return detailRead(r, "mauriceReportingTranscript", limit, cursor,
		func(page mauricereport.PageRequest) (mauricereport.MessagePage, bool, error) {
			return r.MauriceReporting.Transcript(ctx, reportingRef(ref), page)
		},
		func(p mauricereport.MessagePage) *model.MauriceReportingMessagePage {
			return &model.MauriceReportingMessagePage{Items: convertAll(p.Messages, reportingMessage), NextCursor: optionalString(p.NextCursor)}
		})
}

func (r *Resolver) mauriceReportingTurns(ctx context.Context, ref model.MauriceReportingConversationRef, limit *int, cursor *string) (*model.MauriceReportingTurnPage, error) {
	return detailRead(r, "mauriceReportingTurns", limit, cursor,
		func(page mauricereport.PageRequest) (mauricereport.TurnPage, bool, error) {
			return r.MauriceReporting.Turns(ctx, reportingRef(ref), page)
		},
		func(p mauricereport.TurnPage) *model.MauriceReportingTurnPage {
			return &model.MauriceReportingTurnPage{Items: convertAll(p.Turns, reportingTurn), NextCursor: optionalString(p.NextCursor)}
		})
}

func (r *Resolver) mauriceReportingTurnCalls(ctx context.Context, ref model.MauriceReportingConversationRef, turnID string, limit *int, cursor *string) (*model.MauriceReportingCallPage, error) {
	return detailRead(r, "mauriceReportingTurnCalls", limit, cursor,
		func(page mauricereport.PageRequest) (mauricereport.CallPage, bool, error) {
			return r.MauriceReporting.TurnCalls(ctx, reportingRef(ref), turnID, page)
		}, reportingCallPage)
}

func (r *Resolver) mauriceReportingTitleCalls(ctx context.Context, ref model.MauriceReportingConversationRef, limit *int, cursor *string) (*model.MauriceReportingCallPage, error) {
	return detailRead(r, "mauriceReportingTitleCalls", limit, cursor,
		func(page mauricereport.PageRequest) (mauricereport.CallPage, bool, error) {
			return r.MauriceReporting.TitleCalls(ctx, reportingRef(ref), page)
		}, reportingCallPage)
}

func (r *Resolver) mauriceReportingToolCalls(ctx context.Context, ref model.MauriceReportingConversationRef, turnID string, limit *int, cursor *string) (*model.MauriceReportingToolCallPage, error) {
	return detailRead(r, "mauriceReportingToolCalls", limit, cursor,
		func(page mauricereport.PageRequest) (mauricereport.ToolCallPage, bool, error) {
			return r.MauriceReporting.ToolCalls(ctx, reportingRef(ref), turnID, page)
		},
		func(p mauricereport.ToolCallPage) *model.MauriceReportingToolCallPage {
			return &model.MauriceReportingToolCallPage{Items: convertAll(p.ToolCalls, reportingToolCall), NextCursor: optionalString(p.NextCursor)}
		})
}

func (r *Resolver) mauriceReportingCallInput(ctx context.Context, ref model.MauriceReportingConversationRef, callID string, limit *int, cursor *string) (*model.MauriceReportingCallInput, error) {
	return detailRead(r, "mauriceReportingCallInput", limit, cursor,
		func(page mauricereport.PageRequest) (mauricereport.CallInput, bool, error) {
			return r.MauriceReporting.CallInput(ctx, reportingRef(ref), callID, page)
		},
		func(in mauricereport.CallInput) *model.MauriceReportingCallInput {
			return &model.MauriceReportingCallInput{
				Call:            reportingCall(in.Call),
				SystemPrompt:    reportingText(in.SystemPrompt),
				Instruction:     reportingText(in.Instruction),
				ToolDefinitions: reportingText(in.ToolDefinitions),
				Inputs:          convertAll(in.Inputs, reportingInputMessage),
				NextCursor:      optionalString(in.NextCursor),
			}
		})
}

func (r *Resolver) mauriceReportingToolCall(ctx context.Context, ref model.MauriceReportingConversationRef, toolCallID string) (*model.MauriceReportingToolCallDetail, error) {
	return detailRead(r, "mauriceReportingToolCall", nil, nil,
		func(mauricereport.PageRequest) (mauricereport.ToolCallDetail, bool, error) {
			return r.MauriceReporting.ToolCall(ctx, reportingRef(ref), toolCallID)
		},
		func(d mauricereport.ToolCallDetail) *model.MauriceReportingToolCallDetail {
			return &model.MauriceReportingToolCallDetail{
				ToolCall:     reportingToolCall(d.ToolCall),
				ArgumentsRaw: reportingText(&d.ArgumentsRaw),
				Arguments:    reportingText(d.Arguments),
				Result:       reportingText(d.Result),
			}
		})
}

func (r *Resolver) mauriceReportingPayload(ctx context.Context, ref model.MauriceReportingConversationRef, kind model.MauriceReportingPayloadKind, id string, offset, limit *int) (*model.MauriceReportingText, error) {
	req := mauricereport.PayloadRequest{Kind: mauricereport.PayloadKind(strings.ToLower(string(kind))), ID: id}
	if offset != nil {
		req.Offset = int64(*offset)
	}
	if limit != nil {
		if *limit < 1 {
			return nil, payloadLimitError(*limit)
		}
		req.Limit = *limit
	}
	return detailRead(r, "mauriceReportingPayload", nil, nil,
		func(mauricereport.PageRequest) (mauricereport.Text, bool, error) {
			return r.MauriceReporting.Payload(ctx, reportingRef(ref), req)
		},
		func(t mauricereport.Text) *model.MauriceReportingText { return reportingText(&t) })
}

// payloadLimitError refuses an explicit payload limit below 1, which the
// store would read as the default.
func payloadLimitError(limit int) error {
	return fmt.Errorf("%w: limit %d is outside 1..%d", mauricereport.ErrInvalidArgument, limit, mauricereport.MaxPayloadRunes)
}

func convertAll[T, Out any](items []T, convert func(T) *Out) []*Out {
	out := make([]*Out, len(items))
	for i, item := range items {
		out[i] = convert(item)
	}
	return out
}

func reportingText(t *mauricereport.Text) *model.MauriceReportingText {
	if t == nil {
		return nil
	}
	out := &model.MauriceReportingText{Text: t.Text, Offset: t.Offset, Length: t.Length, Truncated: t.Truncated()}
	if out.Truncated {
		end := t.End()
		out.NextOffset = &end
	}
	return out
}

func reportingStatus(s mauricereport.TurnStatus) model.MauriceReportingTurnStatus {
	return model.MauriceReportingTurnStatus(strings.ToUpper(string(s)))
}

func reportingMessage(m mauricereport.Message) *model.MauriceReportingMessage {
	return &model.MauriceReportingMessage{
		ID: m.ID, TurnID: m.TurnID, TurnNumber: int(m.TurnNumber), TurnStatus: reportingStatus(m.TurnStatus),
		Replayable: m.Replayable(), Number: int(m.Number),
		Role:      model.MauriceReportingMessageRole(strings.ToUpper(m.Role)),
		CreatedAt: m.CreatedAt, ContentLength: m.ContentLength, Content: reportingText(m.Content),
		ResultToolCallID: optionalID(m.ResultToolCallID), ToolCallCount: m.ToolCalls,
	}
}

func reportingInputMessage(in mauricereport.InputMessage) *model.MauriceReportingCallInputMessage {
	out := &model.MauriceReportingCallInputMessage{Number: int(in.Number), Withheld: in.Withheld, MessageID: in.Message.ID}
	if !in.Withheld {
		out.Message = reportingMessage(in.Message)
	}
	return out
}

func reportingTurn(t mauricereport.Turn) *model.MauriceReportingTurn {
	return &model.MauriceReportingTurn{
		ID: t.ID, Number: int(t.Number), Status: reportingStatus(t.Status), Replayable: t.Replayable(),
		ErrorClass: t.ErrorClass, StartedAt: t.StartedAt, CompletedAt: t.CompletedAt, Warnings: t.Warnings,
		MessageCount: t.Messages, ToolCallCount: t.ToolCalls, Usage: reportingUsage(t.Usage),
		Models: convertAll(t.Models, func(m mauricereport.Model) *model.MauriceReportingModel {
			return &model.MauriceReportingModel{Provider: m.Provider, Model: m.Model}
		}),
	}
}

func reportingCallPage(p mauricereport.CallPage) *model.MauriceReportingCallPage {
	return &model.MauriceReportingCallPage{Items: convertAll(p.Calls, reportingCall), NextCursor: optionalString(p.NextCursor)}
}

func reportingCall(c mauricereport.Call) *model.MauriceReportingCall {
	out := &model.MauriceReportingCall{
		ID: strconv.FormatInt(c.ID, 10), TurnID: c.TurnID, Kind: c.Kind, Provider: c.Provider, Model: c.Model,
		ProviderRequestID: c.ProviderRequestID, Status: c.Status, ErrorClass: c.ErrorClass,
		FinishReason: c.FinishReason, StartedAt: c.StartedAt, CompletedAt: c.CompletedAt,
		InputTokens: c.InputTokens, OutputTokens: c.OutputTokens,
		CacheCreationInputTokens: c.CacheCreationInputTokens, CacheReadInputTokens: c.CacheReadInputTokens,
		InputMessageCount: c.InputMessages, SystemPromptLength: c.SystemPromptLength,
		InstructionLength: c.InstructionLength, ToolDefinitionsLength: c.ToolDefinitionsLength,
		ToolCallCount: c.ToolCalls,
	}
	if c.Round != nil {
		round := int(*c.Round)
		out.Round = &round
	}
	return out
}

func reportingToolCall(c mauricereport.ToolCall) *model.MauriceReportingToolCall {
	return &model.MauriceReportingToolCall{
		ID: strconv.FormatInt(c.ID, 10), TurnID: c.TurnID, CallID: optionalID(c.CallID), MessageID: c.MessageID,
		Sequence: int(c.Sequence), ProviderToolCallID: c.ProviderToolCallID, Type: c.Type, Name: c.Name,
		Status: c.Status, StartedAt: c.StartedAt, CompletedAt: c.CompletedAt, ArgumentsValid: c.ArgumentsValid,
		ArgumentsRawLength: c.ArgumentsRawLength, ResultLength: c.ResultLength,
	}
}

// optionalID is a bigint row ID as its GraphQL string, nil for none.
func optionalID(id *int64) *string {
	if id == nil {
		return nil
	}
	s := strconv.FormatInt(*id, 10)
	return &s
}
