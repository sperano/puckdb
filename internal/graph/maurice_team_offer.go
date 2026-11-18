package graph

import (
	"context"
	"errors"

	"github.com/rs/zerolog/log"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/sperano/puckdb/internal/yahoolink"
)

// Error codes (extensions.code) of the team offer reset mutations, which the
// CLI maps back to the yahoolink errors.
const (
	ErrorCodeUserNotFound  = "USER_NOT_FOUND"
	ErrorCodeAmbiguousUser = "AMBIGUOUS_USER"
)

// TeamOfferResetter clears Maurice users' team offer flag for the @admin
// resetTeamOffer and resetInstanceTeamOffers mutations (yahoolink.PGStore
// in production).
type TeamOfferResetter interface {
	ResetUserTeamOffer(ctx context.Context, instance, username string) (bool, error)
	ResetInstanceTeamOffers(ctx context.Context, instance string) (int64, error)
}

var (
	// errTeamOffersNotConfigured is returned by the reset mutations when the
	// resolver has no TeamOffers (no database).
	errTeamOffersNotConfigured = errors.New("team offer reset is not configured")
	// errEmptyInstance rejects a reset naming no instance.
	errEmptyInstance = errors.New("instance must not be empty")
	// errEmptyUsername rejects a reset naming no user, which never means
	// every user.
	errEmptyUsername = errors.New("username must not be empty")
)

func (r *Resolver) resetTeamOffer(ctx context.Context, instance, username string) (bool, error) {
	if err := r.checkTeamOfferReset(instance); err != nil {
		return false, err
	}
	if username == "" {
		return false, errEmptyUsername
	}
	wasSeen, err := r.TeamOffers.ResetUserTeamOffer(ctx, instance, username)
	if err != nil {
		return false, clientTeamOfferError("resetTeamOffer", err)
	}
	log.Info().Str("instance", instance).Str("username", username).Bool("was_seen", wasSeen).
		Msg("reset user's team offer")
	return wasSeen, nil
}

func (r *Resolver) resetInstanceTeamOffers(ctx context.Context, instance string) (int, error) {
	if err := r.checkTeamOfferReset(instance); err != nil {
		return 0, err
	}
	n, err := r.TeamOffers.ResetInstanceTeamOffers(ctx, instance)
	if err != nil {
		return 0, clientTeamOfferError("resetInstanceTeamOffers", err)
	}
	log.Info().Str("instance", instance).Int64("users", n).Msg("reset instance's team offers")
	return int(n), nil
}

// checkTeamOfferReset checks the resolver can reset team offers and the
// request names an instance.
func (r *Resolver) checkTeamOfferReset(instance string) error {
	if r.TeamOffers == nil {
		return errTeamOffersNotConfigured
	}
	if instance == "" {
		return errEmptyInstance
	}
	return nil
}

// clientTeamOfferError gives an unknown or ambiguous username its error
// code, returns the request's own end as is, and otherwise logs err and
// returns errMauriceInternal, so driver or SQL text never reaches the client.
func clientTeamOfferError(op string, err error) error {
	switch {
	case errors.Is(err, yahoolink.ErrUserNotFound):
		return &gqlerror.Error{Message: err.Error(), Extensions: map[string]any{"code": ErrorCodeUserNotFound}}
	case errors.Is(err, yahoolink.ErrAmbiguousUser):
		return &gqlerror.Error{Message: err.Error(), Extensions: map[string]any{"code": ErrorCodeAmbiguousUser}}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	}
	log.Error().Err(err).Str("operation", op).Msg("team offer reset failed")
	return errMauriceInternal
}
