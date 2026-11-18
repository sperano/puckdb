package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sperano/puckdb/internal/yahoolink"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newStaticGraphQLServer answers every request with body.
func newStaticGraphQLServer(t *testing.T, body string) *GraphQLClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return newClientForTestServer(srv)
}

// A response without the field is an error, not a count of zero.
func TestGraphQLClient_ResetInstanceTeamOffersMissingField(t *testing.T) {
	_, err := newStaticGraphQLServer(t, `{"data":{}}`).ResetInstanceTeamOffers(context.Background(), "puckdb")
	assert.EqualError(t, err, "parse resetInstanceTeamOffers: missing from the response")
}

// Only the user lookup codes become yahoolink errors; any other GraphQL
// error keeps its usual form.
func TestGraphQLClient_ResetTeamOfferErrorCodes(t *testing.T) {
	_, err := newStaticGraphQLServer(t,
		`{"errors":[{"message":"no such user: \"alice\" on instance \"puckdb\"","extensions":{"code":"USER_NOT_FOUND"}}],"data":null}`,
	).ResetTeamOffer(context.Background(), "puckdb", "alice")
	require.ErrorIs(t, err, yahoolink.ErrUserNotFound)
	assert.EqualError(t, err, `no such user: "alice" on instance "puckdb"`)

	_, err = newStaticGraphQLServer(t,
		`{"errors":[{"message":"internal error","extensions":{"code":"SOMETHING_ELSE"}}],"data":null}`,
	).ResetTeamOffer(context.Background(), "puckdb", "alice")
	assert.NotErrorIs(t, err, yahoolink.ErrUserNotFound)
	assert.NotErrorIs(t, err, yahoolink.ErrAmbiguousUser)
	assert.EqualError(t, err, "graphql error: internal error")

	_, err = newStaticGraphQLServer(t, `{"errors":[{"message":"boom","extensions":{"code":7}}],"data":null}`).
		ResetTeamOffer(context.Background(), "puckdb", "alice")
	assert.EqualError(t, err, "graphql error: boom", "a non-string code is no code")
}
