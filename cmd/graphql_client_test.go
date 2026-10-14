package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Note: these tests mutate the global viper state, so they must not use
// t.Parallel (repo rule: parallel tests must not share process globals).

func TestGraphQLClientAdminTokenHeader(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		wantHeader string
	}{
		{name: "token configured is sent", token: "s3cret", wantHeader: "s3cret"},
		{name: "empty token sends no header", token: "", wantHeader: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotHeader string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotHeader = r.Header.Get(httpx.HeaderAdminToken)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"flushRedisDB":true}}`))
			}))
			defer srv.Close()

			viper.Set(config.FlagAdminToken, tt.token)
			t.Cleanup(func() { viper.Set(config.FlagAdminToken, "") })

			client := NewGraphQLClient(srv.URL)
			ok, err := client.FlushRedisDB(context.Background())
			require.NoError(t, err)
			assert.True(t, ok)
			assert.Equal(t, tt.wantHeader, gotHeader)
		})
	}
}

func TestGraphQLClientBasicAuth(t *testing.T) {
	tests := []struct {
		name     string
		user     string
		password string
		wantAuth bool
	}{
		{name: "credentials configured send basic auth", user: "eric", password: "app-pass", wantAuth: true},
		{name: "empty password sends no auth", user: "eric", password: "", wantAuth: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotUser, gotPass string
			var gotOK bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotUser, gotPass, gotOK = r.BasicAuth()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"flushRedisDB":true}}`))
			}))
			defer srv.Close()

			viper.Set(config.FlagAPIUser, tt.user)
			viper.Set(config.FlagAPIPassword, tt.password)
			t.Cleanup(func() {
				viper.Set(config.FlagAPIUser, "")
				viper.Set(config.FlagAPIPassword, "")
			})

			client := NewGraphQLClient(srv.URL)
			ok, err := client.FlushRedisDB(context.Background())
			require.NoError(t, err)
			assert.True(t, ok)
			assert.Equal(t, tt.wantAuth, gotOK)
			if tt.wantAuth {
				assert.Equal(t, tt.user, gotUser)
				assert.Equal(t, tt.password, gotPass)
			}
		})
	}
}

func TestNewGraphQLClient_DefaultEndpoint(t *testing.T) {
	wantBaseURL := fmt.Sprintf("http://%s:%d", config.DefaultAPIHost, config.DefaultAPIPort)
	require.Equal(t, wantBaseURL, config.DefaultAPIServerAddr)

	client := NewGraphQLClient(config.DefaultAPIServerAddr)
	require.Equal(t, wantBaseURL+"/graphql/query", client.endpoint)
}

// A non-200 GraphQL error body (gqlgen answers a query that fails
// validation with 422) surfaces its message; any other body keeps the
// status-based error.
func TestGraphQLClientNon200Errors(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{"graphql error body", http.StatusUnprocessableEntity,
			`{"errors":[{"message":"Cannot query field \"x\" on type \"Query\"."}],"data":null}`,
			`graphql error: Cannot query field "x" on type "Query".`},
		{"proxy page", http.StatusBadGateway, `<html>bad gateway</html>`,
			"unexpected status code 502: <html>bad gateway</html>"},
		{"json without errors", http.StatusInternalServerError, `{"data":null}`,
			`unexpected status code 500: {"data":null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := NewGraphQLClient("http://placeholder")
			c.endpoint = srv.URL
			_, err := c.execute(context.Background(), "{ x }", nil)
			require.EqualError(t, err, tc.wantErr)
		})
	}
}
