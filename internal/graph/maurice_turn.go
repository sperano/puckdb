package graph

import (
	"context"
	"errors"

	"github.com/rs/zerolog/log"

	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/maurice"
)

// errMissingIdempotencyKey rejects a mauriceChatStart without a key: a client
// that loses the start answer finds its turn again only by resending it.
var errMissingIdempotencyKey = errors.New("idempotencyKey must not be empty")

// errMauriceInternal replaces a turn error the client cannot act on (a
// storage fault), so driver or SQL text never reaches it.
var errMauriceInternal = errors.New("internal error")

// mauriceClientErrors are the turn errors a client can act on, returned as
// they are.
var mauriceClientErrors = []error{
	maurice.ErrConversationNotFound,
	maurice.ErrTurnInProgress,
	maurice.ErrIdempotencyKeyReused,
	maurice.ErrInvalidIdempotencyKey,
	maurice.ErrServiceClosed,
	maurice.ErrTooManyTurns,
}

// clientTurnError returns err when it is one of mauriceClientErrors or the
// request's own end (cancelled or past its deadline: no server fault, so not
// logged), and otherwise logs it and returns errMauriceInternal.
func clientTurnError(op string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	for _, known := range mauriceClientErrors {
		if errors.Is(err, known) {
			return err
		}
	}
	log.Error().Err(err).Str("operation", op).Msg("maurice turn request failed")
	return errMauriceInternal
}

func (r *Resolver) mauriceChatStart(ctx context.Context, conversationID *string, message, idempotencyKey string) (*model.MauriceTurnStart, error) {
	svc, userID, err := r.scopedMaurice(ctx)
	if err != nil {
		return nil, err
	}
	if idempotencyKey == "" {
		return nil, errMissingIdempotencyKey
	}
	ref, err := svc.StartChat(ctx, maurice.ChatRequest{
		UserID: userID, ConversationID: conversationID, Message: message, IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return nil, clientTurnError("mauriceChatStart", err)
	}
	return &model.MauriceTurnStart{TurnID: ref.TurnID, ConversationID: ref.ConversationID}, nil
}

// mauriceTurn returns nil for a turn the user cannot read
// (maurice.ErrTurnNotFound).
func (r *Resolver) mauriceTurn(ctx context.Context, id string) (*model.MauriceTurn, error) {
	svc, userID, err := r.scopedMaurice(ctx)
	if err != nil {
		return nil, err
	}
	turn, err := svc.GetTurn(ctx, userID, id)
	if errors.Is(err, maurice.ErrTurnNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, clientTurnError("mauriceTurn", err)
	}
	return mauriceTurnModel(turn), nil
}

// mauriceTurnModel maps a stored turn to the API: a cancelled turn is
// FAILED (only a server shutdown cancels one), its class in error.
func mauriceTurnModel(turn *maurice.Turn) *model.MauriceTurn {
	out := &model.MauriceTurn{
		ID:             turn.ID,
		ConversationID: turn.ConversationID,
		ToolsUsed:      orEmpty(turn.ToolsUsed),
		Warnings:       orEmpty(turn.Warnings),
	}
	switch turn.Status {
	case maurice.TurnRunning:
		out.Status = model.MauriceTurnStatusRunning
	case maurice.TurnSucceeded:
		out.Status = model.MauriceTurnStatusSucceeded
		out.MessageID, out.Content = &turn.MessageID, &turn.Content
	default:
		out.Status = model.MauriceTurnStatusFailed
		class := string(turn.ErrorClass)
		out.Error = &class
	}
	return out
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
