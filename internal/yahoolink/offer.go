package yahoolink

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ResetTeamOffer clears userID's team offer flag (TeamChoices.Offered), so
// the client opens the first-login team offer again on the user's next
// visit. It reports whether the user had seen the offer (false: it was
// still pending) and returns ErrUserNotFound for no such user. The user's
// links and deselections stay: the offer opens on the current selection.
func (s *PGStore) ResetTeamOffer(ctx context.Context, userID pgtype.UUID) (bool, error) {
	wasSeen, err := s.queries.ResetAppUserTeamOffer(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("%w: id %s", ErrUserNotFound, userID)
	}
	if err != nil {
		return false, fmt.Errorf("reset team offer: %w", err)
	}
	return wasSeen, nil
}

// ResetInstanceTeamOffers is ResetTeamOffer for every user of instance,
// returning how many had seen the offer.
func (s *PGStore) ResetInstanceTeamOffers(ctx context.Context, instance string) (int64, error) {
	n, err := s.queries.ResetInstanceTeamOffers(ctx, instance)
	if err != nil {
		return 0, fmt.Errorf("reset team offers of instance %q: %w", instance, err)
	}
	return n, nil
}
