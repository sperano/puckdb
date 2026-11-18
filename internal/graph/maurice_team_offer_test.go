package graph

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sperano/puckdb/internal/graph/generated"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/yahoolink"
)

// teamOfferAdminToken is the admin token newTeamOfferServer accepts.
const teamOfferAdminToken = "offer-token"

// fakeTeamOfferResetter records each reset and answers with its fields.
type fakeTeamOfferResetter struct {
	wasSeen bool
	count   int64
	err     error
	calls   []string
}

func (f *fakeTeamOfferResetter) ResetUserTeamOffer(_ context.Context, instance, username string) (bool, error) {
	f.calls = append(f.calls, instance+"/"+username)
	return f.wasSeen, f.err
}

func (f *fakeTeamOfferResetter) ResetInstanceTeamOffers(_ context.Context, instance string) (int64, error) {
	f.calls = append(f.calls, instance)
	return f.count, f.err
}

// newTeamOfferServer serves the schema over resetter with the real @admin
// directive, accepting teamOfferAdminToken.
func newTeamOfferServer(t *testing.T, resetter TeamOfferResetter) *httptest.Server {
	t.Helper()
	schema := handler.New(generated.NewExecutableSchema(generated.Config{
		Resolvers:  &Resolver{TeamOffers: resetter},
		Directives: generated.DirectiveRoot{Admin: NewAdminDirective(AdminAuth{Token: teamOfferAdminToken})},
	}))
	schema.AddTransport(transport.POST{})
	server := httptest.NewServer(httpx.HeaderContext(schema))
	t.Cleanup(server.Close)
	return server
}

const (
	resetTeamOfferQuery          = `mutation { resetTeamOffer(instance: "maurice", username: "alice") }`
	resetInstanceTeamOffersQuery = `mutation { resetInstanceTeamOffers(instance: "maurice") }`
)

var teamOfferAdminHeaders = http.Header{httpx.HeaderAdminToken: {teamOfferAdminToken}}

func TestResetTeamOffer_Mutations(t *testing.T) {
	resetter := &fakeTeamOfferResetter{wasSeen: true, count: 3}
	server := newTeamOfferServer(t, resetter)

	resp := postGraphQL(t, server, teamOfferAdminHeaders, resetTeamOfferQuery, nil)
	require.Empty(t, resp.Errors)
	assert.JSONEq(t, `{"resetTeamOffer": true}`, string(resp.Data))
	resp = postGraphQL(t, server, teamOfferAdminHeaders, resetInstanceTeamOffersQuery, nil)
	require.Empty(t, resp.Errors)
	assert.JSONEq(t, `{"resetInstanceTeamOffers": 3}`, string(resp.Data))
	assert.Equal(t, []string{"maurice/alice", "maurice"}, resetter.calls)

	resetter.wasSeen = false
	resp = postGraphQL(t, server, teamOfferAdminHeaders, resetTeamOfferQuery, nil)
	require.Empty(t, resp.Errors)
	assert.JSONEq(t, `{"resetTeamOffer": false}`, string(resp.Data), "a pending offer was not seen")
}

// Without the admin token neither mutation reaches the store.
func TestResetTeamOffer_NonAdminRefused(t *testing.T) {
	resetter := &fakeTeamOfferResetter{}
	server := newTeamOfferServer(t, resetter)
	for _, headers := range []http.Header{nil, {httpx.HeaderAdminToken: {"wrong"}}} {
		for _, query := range []string{resetTeamOfferQuery, resetInstanceTeamOffersQuery} {
			resp := postGraphQL(t, server, headers, query, nil)
			require.Len(t, resp.Errors, 1, query)
			assert.Equal(t, errAdminAccessDenied.Error(), resp.Errors[0].Message, query)
		}
	}
	assert.Empty(t, resetter.calls)
}

// An unknown or ambiguous username carries its code and yahoolink's
// message, which the CLI prints as is.
func TestResetTeamOffer_UserLookupCodes(t *testing.T) {
	for code, cause := range map[string]error{
		ErrorCodeUserNotFound:  yahoolink.ErrUserNotFound,
		ErrorCodeAmbiguousUser: yahoolink.ErrAmbiguousUser,
	} {
		err := fmt.Errorf(`%w: "alice" on instance "maurice"`, cause)
		resp := postGraphQL(t, newTeamOfferServer(t, &fakeTeamOfferResetter{err: err}), teamOfferAdminHeaders, resetTeamOfferQuery, nil)
		require.Len(t, resp.Errors, 1, code)
		assert.Equal(t, err.Error(), resp.Errors[0].Message, code)
		assert.Equal(t, code, resp.Errors[0].Extensions["code"], code)
	}
}

// A storage fault becomes errMauriceInternal, without the driver text; the
// request's own end reaches the client as is.
func TestResetTeamOffer_ErrorMapping(t *testing.T) {
	ctx := context.Background()
	fault := &Resolver{TeamOffers: &fakeTeamOfferResetter{err: errors.New(`ERROR: relation "app_users" does not exist`)}}
	_, err := fault.resetTeamOffer(ctx, "maurice", "alice")
	assert.Equal(t, errMauriceInternal, err)
	_, err = fault.resetInstanceTeamOffers(ctx, "maurice")
	assert.Equal(t, errMauriceInternal, err)

	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		r := &Resolver{TeamOffers: &fakeTeamOfferResetter{err: fmt.Errorf("reset team offer: %w", cause)}}
		_, err := r.resetTeamOffer(ctx, "maurice", "alice")
		require.ErrorIs(t, err, cause)
		_, err = r.resetInstanceTeamOffers(ctx, "maurice")
		require.ErrorIs(t, err, cause)
	}
}

// An empty instance or username, or no store, is refused before any reset.
func TestResetTeamOffer_RefusesBeforeTheStore(t *testing.T) {
	ctx := context.Background()
	resetter := &fakeTeamOfferResetter{}
	r := &Resolver{TeamOffers: resetter}
	_, err := r.resetTeamOffer(ctx, "", "alice")
	require.ErrorIs(t, err, errEmptyInstance)
	_, err = r.resetTeamOffer(ctx, "maurice", "")
	require.ErrorIs(t, err, errEmptyUsername)
	_, err = r.resetInstanceTeamOffers(ctx, "")
	require.ErrorIs(t, err, errEmptyInstance)
	assert.Empty(t, resetter.calls)

	_, err = (&Resolver{}).resetTeamOffer(ctx, "maurice", "alice")
	require.ErrorIs(t, err, errTeamOffersNotConfigured)
	_, err = (&Resolver{}).resetInstanceTeamOffers(ctx, "maurice")
	require.ErrorIs(t, err, errTeamOffersNotConfigured)
}
