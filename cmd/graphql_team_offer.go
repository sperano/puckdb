package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sperano/puckdb/internal/graph"
	"github.com/sperano/puckdb/internal/yahoolink"
)

const (
	resetTeamOfferMutation = `mutation($instance: String!, $username: String!) {
	resetTeamOffer(instance: $instance, username: $username)
}`
	resetInstanceTeamOffersMutation = `mutation($instance: String!) { resetInstanceTeamOffers(instance: $instance) }`
)

// userLookupCodes maps the reset mutations' error codes to the yahoolink
// errors they stand for.
var userLookupCodes = map[string]error{
	graph.ErrorCodeUserNotFound:  yahoolink.ErrUserNotFound,
	graph.ErrorCodeAmbiguousUser: yahoolink.ErrAmbiguousUser,
}

// userLookupError is a server-side user lookup failure: it reads as the
// server's message (yahoolink's own) and matches the yahoolink error.
type userLookupError struct {
	message string
	err     error
}

func (e *userLookupError) Error() string { return e.message }
func (e *userLookupError) Unwrap() error { return e.err }

// ResetTeamOffer calls the resetTeamOffer admin mutation: it reports
// whether instance's user named username had seen the team offer, and
// returns an error matching yahoolink.ErrUserNotFound or ErrAmbiguousUser
// when the name resolves to no single user.
func (c *GraphQLClient) ResetTeamOffer(ctx context.Context, instance, username string) (bool, error) {
	wasSeen, err := c.executeBoolMutation(ctx, resetTeamOfferMutation, "resetTeamOffer",
		map[string]any{"instance": instance, "username": username})
	if err != nil {
		return false, asUserLookupError(err)
	}
	return wasSeen, nil
}

// ResetInstanceTeamOffers calls the resetInstanceTeamOffers admin mutation
// and returns how many users of instance had seen the team offer.
func (c *GraphQLClient) ResetInstanceTeamOffers(ctx context.Context, instance string) (int, error) {
	resp, err := c.execute(ctx, resetInstanceTeamOffersMutation, map[string]any{"instance": instance})
	if err != nil {
		return 0, err
	}
	var data struct {
		ResetInstanceTeamOffers *int `json:"resetInstanceTeamOffers"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return 0, fmt.Errorf("parse response: %w", err)
	}
	if data.ResetInstanceTeamOffers == nil {
		return 0, errors.New("parse resetInstanceTeamOffers: missing from the response")
	}
	return *data.ResetInstanceTeamOffers, nil
}

// asUserLookupError turns a GraphQL error carrying a user lookup code into
// a userLookupError, leaving any other error as is.
func asUserLookupError(err error) error {
	var gqlErr *graphQLError
	if !errors.As(err, &gqlErr) {
		return err
	}
	if sentinel, ok := userLookupCodes[gqlErr.Code()]; ok {
		return &userLookupError{message: gqlErr.Message, err: sentinel}
	}
	return err
}
