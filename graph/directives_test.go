package graph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/httpx"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withAdminConfig sets the admin-group and admin-token viper values for the
// duration of a test and restores the previous values afterward via
// t.Cleanup. Tests that mutate viper globals must not run in parallel with
// each other (or with anything else touching these keys).
func withAdminConfig(t *testing.T, group, token string) {
	t.Helper()
	prevGroup := viper.GetString(config.FlagAdminGroup)
	prevToken := viper.GetString(config.FlagAdminToken)
	viper.Set(config.FlagAdminGroup, group)
	viper.Set(config.FlagAdminToken, token)
	t.Cleanup(func() {
		viper.Set(config.FlagAdminGroup, prevGroup)
		viper.Set(config.FlagAdminToken, prevToken)
	})
}

// contextWithHeaders drives httpx.HeaderContext through a real request/
// response round trip and returns the context it produces, so directive
// tests exercise the same code path production traffic does rather than
// poking an unexported context key directly.
func contextWithHeaders(t *testing.T, headers http.Header) context.Context {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/graphql/query", nil)
	if headers != nil {
		req.Header = headers
	}

	var captured context.Context
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = r.Context()
	})
	httpx.HeaderContext(next).ServeHTTP(httptest.NewRecorder(), req)
	return captured
}

func nextOK(_ context.Context) (interface{}, error) {
	return "ok", nil
}

func TestAdminDirective(t *testing.T) {
	tests := []struct {
		name        string
		adminGroup  string
		adminToken  string
		headers     http.Header
		noHeaders   bool // don't run the request through HeaderContext at all
		wantAllowed bool
	}{
		{
			name:        "group match single value",
			adminGroup:  "puckdb-admins",
			headers:     http.Header{"X-Authentik-Groups": []string{"puckdb-admins"}},
			wantAllowed: true,
		},
		{
			name:        "group mismatch",
			adminGroup:  "puckdb-admins",
			headers:     http.Header{"X-Authentik-Groups": []string{"some-other-group"}},
			wantAllowed: false,
		},
		{
			name:        "pipe separated groups, match not first",
			adminGroup:  "puckdb-admins",
			headers:     http.Header{"X-Authentik-Groups": []string{"editors|puckdb-admins|viewers"}},
			wantAllowed: true,
		},
		{
			name:        "comma separated groups tolerated",
			adminGroup:  "puckdb-admins",
			headers:     http.Header{"X-Authentik-Groups": []string{"editors, puckdb-admins ,viewers"}},
			wantAllowed: true,
		},
		{
			name:        "token match",
			adminGroup:  "puckdb-admins",
			adminToken:  "s3cr3t",
			headers:     http.Header{"X-Admin-Token": []string{"s3cr3t"}},
			wantAllowed: true,
		},
		{
			name:        "token mismatch",
			adminGroup:  "puckdb-admins",
			adminToken:  "s3cr3t",
			headers:     http.Header{"X-Admin-Token": []string{"wrong"}},
			wantAllowed: false,
		},
		{
			name:        "token disabled when configured empty",
			adminGroup:  "puckdb-admins",
			adminToken:  "",
			headers:     http.Header{"X-Admin-Token": []string{"anything"}},
			wantAllowed: false,
		},
		{
			name:        "empty headers denied",
			adminGroup:  "puckdb-admins",
			headers:     http.Header{},
			wantAllowed: false,
		},
		{
			name:        "no HeaderContext middleware in chain denied",
			adminGroup:  "puckdb-admins",
			noHeaders:   true,
			wantAllowed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withAdminConfig(t, tt.adminGroup, tt.adminToken)

			ctx := context.Background()
			if !tt.noHeaders {
				ctx = contextWithHeaders(t, tt.headers)
			}

			res, err := AdminDirective(ctx, nil, graphql.Resolver(nextOK))

			if tt.wantAllowed {
				require.NoError(t, err)
				assert.Equal(t, "ok", res)
			} else {
				require.Error(t, err)
				assert.Nil(t, res)
				assert.ErrorIs(t, err, errAdminAccessDenied)
			}
		})
	}
}

func TestSplitGroups(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "empty string", raw: "", want: []string{}},
		{name: "single group", raw: "puckdb-admins", want: []string{"puckdb-admins"}},
		{name: "pipe separated", raw: "a|b|c", want: []string{"a", "b", "c"}},
		{name: "comma separated", raw: "a,b,c", want: []string{"a", "b", "c"}},
		{name: "mixed separators with spaces", raw: "a | b, c ", want: []string{"a", "b", "c"}},
		{name: "collapses empty entries", raw: "a||,b", want: []string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, splitGroups(tt.raw))
		})
	}
}
