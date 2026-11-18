package graph

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sperano/puckdb/internal/appuser"
	"github.com/sperano/puckdb/internal/graph/generated"
)

// mauriceMe answers the request user's greeting name, and needs a user.
func TestMauriceMe(t *testing.T) {
	schema := handler.New(generated.NewExecutableSchema(generated.Config{Resolvers: &Resolver{}}))
	schema.AddTransport(transport.POST{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if name := req.Header.Get("X-Test-First-Name"); name != "" {
			req = req.WithContext(appuser.WithUser(req.Context(), appuser.User{ID: "user-1", FirstName: name}))
		}
		schema.ServeHTTP(w, req)
	}))
	t.Cleanup(server.Close)
	const query = `{ mauriceMe { firstName } }`

	resp := postGraphQL(t, server, http.Header{"X-Test-First-Name": {"Éric"}}, query, nil)
	require.Empty(t, resp.Errors)
	var data struct {
		MauriceMe struct {
			FirstName string `json:"firstName"`
		} `json:"mauriceMe"`
	}
	require.NoError(t, json.Unmarshal(resp.Data, &data))
	assert.Equal(t, "Éric", data.MauriceMe.FirstName)

	resp = postGraphQL(t, server, nil, query, nil)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, appuser.ErrUnauthenticated.Error(), resp.Errors[0].Message)
}
