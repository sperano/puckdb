package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sperano/puckdb/internal/appuser"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/graph"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// runHTTPServerTestTimeout bounds how long a test waits for runHTTPServer to
// return after context cancellation or a listen failure.
const runHTTPServerTestTimeout = 5 * time.Second

// serverStartDeadline and serverPollInterval bound waitForListening's
// readiness polling of the asynchronously started test server.
const (
	serverStartDeadline = 2 * time.Second
	serverPollInterval  = 10 * time.Millisecond
)

// reservePort grabs an OS-assigned port on 127.0.0.1, closes the listener,
// and returns the bound address for the test server to reclaim. There is a
// small TOCTOU window where another process could take the port first; the
// loopback-only constraint and the immediate handoff make this acceptable
// for test purposes.
func reservePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

// waitForListening polls addr until a TCP connection succeeds or the
// deadline expires. runHTTPServer starts ListenAndServe asynchronously, so
// callers can't dial or cancel immediately.
func waitForListening(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(serverStartDeadline)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(serverPollInterval)
	}
	t.Fatalf("server at %s did not start listening", addr)
}

func TestRunHTTPServer_ReturnsNilAfterContextCancel(t *testing.T) {
	addr := reservePort(t)
	srv := &http.Server{Addr: addr, Handler: http.NewServeMux()}
	ctx, cancel := context.WithCancel(context.Background())
	// Bound the server goroutine's lifetime even when waitForListening
	// fails the test before the explicit cancel below is reached.
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})

	errCh := make(chan error, 1)
	go func() { errCh <- runHTTPServer(ctx, srv, false, "", "") }()

	waitForListening(t, addr)
	cancel()

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(runHTTPServerTestTimeout):
		t.Fatal("runHTTPServer did not return after context cancellation")
	}
}

func TestRunHTTPServer_ReturnsListenError(t *testing.T) {
	// Pre-bind a port so srv.ListenAndServe fails with "address already in use".
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()

	srv := &http.Server{Addr: l.Addr().String(), Handler: http.NewServeMux()}
	ctx := context.Background()

	errCh := make(chan error, 1)
	go func() { errCh <- runHTTPServer(ctx, srv, false, "", "") }()

	select {
	case err := <-errCh:
		require.Error(t, err, "ListenAndServe on an already-bound port should fail")
	case <-time.After(runHTTPServerTestTimeout):
		t.Fatal("runHTTPServer should have returned the listen error promptly")
	}
}

// newTestAPIRouter builds the real API router with no Redis, an empty
// resolver and pass-through sessions: enough for routes that touch neither.
func newTestAPIRouter() http.Handler {
	passThrough := func(next http.Handler) http.Handler { return next }
	return setupAPIRouter(nil, &graph.Resolver{}, appSessions{filter: passThrough, resolve: passThrough}, apiAuth{})
}

// TestAPIRouterRecoversHandlerPanics answers 500 for a panicking route
// instead of letting the panic reach net/http, which drops the connection
// and logs outside zerolog, and logs the panic as one zerolog line: with
// Recoverer above ChiLogger, chi would print it to stderr instead. Not
// parallel: it replaces the global logger.
func TestAPIRouterRecoversHandlerPanics(t *testing.T) {
	var buf bytes.Buffer
	previous := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = previous })
	passThrough := func(next http.Handler) http.Handler { return next }
	router := setupAPIRouter(nil, &graph.Resolver{}, appSessions{filter: passThrough, resolve: passThrough}, apiAuth{})
	router.Get("/test-panic", func(http.ResponseWriter, *http.Request) { panic("boom") })

	recorder := httptest.NewRecorder()
	require.NotPanics(t, func() {
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/test-panic", nil))
	})

	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	var panics []map[string]any
	for _, raw := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var line map[string]any
		require.NoError(t, json.Unmarshal(raw, &line), "%s", raw)
		if _, ok := line["panic"]; ok {
			panics = append(panics, line)
		}
	}
	require.Len(t, panics, 1, "one zerolog line for the panic")
	assert.Equal(t, "error", panics[0]["level"])
	assert.Equal(t, "/test-panic", panics[0]["path"])
	assert.Equal(t, "boom", panics[0]["panic"])
	assert.NotEmpty(t, panics[0]["stack"])
}

// TestGraphQLRecoverLogsPanicThroughZerolog checks that a resolver panic
// becomes one structured error line with its field path, and the client
// sees only the generic error.
func TestGraphQLRecoverLogsPanicThroughZerolog(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	ctx := graphql.WithOperationContext(context.Background(), &graphql.OperationContext{OperationName: "Boards"})
	ctx = graphql.WithFieldContext(ctx, &graphql.FieldContext{Field: graphql.CollectedField{Field: &ast.Field{Alias: "draftLeagues"}}})

	err := newGraphQLRecover(&logger)(ctx, "boom")

	var gqlErr *gqlerror.Error
	require.ErrorAs(t, err, &gqlErr)
	assert.Equal(t, graphQLInternalError, gqlErr.Message)
	var line map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))
	assert.Equal(t, "error", line["level"])
	assert.Equal(t, "boom", line["panic"])
	assert.Equal(t, "Boards", line["operation"])
	assert.Equal(t, "draftLeagues", line["path"])
	assert.Contains(t, line["stack"], "newGraphQLRecover")
}

// TestGraphQLRecoverLogsPanicWithoutContext covers a panic outside an
// operation and a field: the line has no operation or path.
func TestGraphQLRecoverLogsPanicWithoutContext(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)

	err := newGraphQLRecover(&logger)(context.Background(), "boom")

	var gqlErr *gqlerror.Error
	require.ErrorAs(t, err, &gqlErr)
	assert.Equal(t, graphQLInternalError, gqlErr.Message)
	var line map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))
	assert.Equal(t, "error", line["level"])
	assert.Equal(t, "boom", line["panic"])
	assert.NotEmpty(t, line["stack"])
	assert.NotContains(t, line, "operation")
	assert.NotContains(t, line, "path")
}

// TestAPIRouterServesDraftBoard requests the mounted board through the API
// router, whose Mount hands the handler the full /draft/... path.
func TestAPIRouterServesDraftBoard(t *testing.T) {
	router := newTestAPIRouter()
	for path, contentType := range map[string]string{
		draftBoardPath + "/":          "text/html",
		draftBoardPath + "/app.js":    "text/javascript",
		draftBoardPath + "/style.css": "text/css",
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusOK, recorder.Code, path)
		assert.Contains(t, recorder.Header().Get("Content-Type"), contentType, path)
		assert.NotEmpty(t, recorder.Body.Bytes(), path)
	}
}

// TestAPIRouterRedirectsDraftBoardRoot sends /draft to /draft/, where the
// page's relative asset links resolve under the mount.
func TestAPIRouterRedirectsDraftBoardRoot(t *testing.T) {
	recorder := httptest.NewRecorder()
	newTestAPIRouter().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, draftBoardPath+"?season=2025", nil))
	assert.Equal(t, http.StatusMovedPermanently, recorder.Code)
	assert.Equal(t, draftBoardPath+"/?season=2025", recorder.Header().Get("Location"))
}

// TestAPIRouterDoesNotServeDraftBoardClientTests keeps the client's Node
// tests and unknown nested paths unreachable through the mount.
func TestAPIRouterDoesNotServeDraftBoardClientTests(t *testing.T) {
	router := newTestAPIRouter()
	for _, path := range []string{draftBoardPath + "/app_test.js", draftBoardPath + "/web/app.js"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusNotFound, recorder.Code, path)
	}
}

// Users belong to one instance, so the API refuses to start without one.
func TestNewAppSessionsRequiresInstance(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set(config.FlagInstance, "  ")

	_, err := newAppSessions(nil)
	require.ErrorContains(t, err, "--"+config.FlagInstance+" (PUCKDB_INSTANCE) is required")
}

// A misconfigured login provider stops the API before it serves anything.
func TestNewAppSessionsRejectsBadProvider(t *testing.T) {
	for name, settings := range map[string]map[string]string{
		"unknown provider":           {config.FlagAuthProvider: "oidc"},
		"cloudflare without its app": {config.FlagAuthProvider: appuser.ProviderCloudflare},
		"cloudflare settings left on authentik": {
			config.FlagAuthProvider:               appuser.ProviderAuthentik,
			config.FlagCloudflareAccessTeamDomain: "team.cloudflareaccess.com",
			config.FlagCloudflareAccessAUD:        "aud",
		},
	} {
		t.Run(name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			viper.Set(config.FlagInstance, "maurice-crapettes")
			for k, v := range settings {
				viper.Set(k, v)
			}
			_, err := newAppSessions(nil)
			require.ErrorContains(t, err, "--"+config.FlagAuthProvider)
		})
	}
}

// The provider's header filter runs on every route: a request carrying both
// identities is refused on /ping, where no user is resolved.
func TestAPIRouterRejectsConflictingIdentities(t *testing.T) {
	passThrough := func(next http.Handler) http.Handler { return next }
	sessions := appSessions{filter: appuser.IgnoreForeignHeaders(appuser.AuthentikProvider{}), resolve: passThrough}
	router := setupAPIRouter(nil, &graph.Resolver{}, sessions, apiAuth{})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(appuser.HeaderUID, "subject-1")
	req.Header.Set(appuser.HeaderCfAccessJWT, "token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	assert.Equal(t, http.StatusUnauthorized, recorder.Code)

	req.Header.Del(appuser.HeaderCfAccessJWT)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	assert.Equal(t, http.StatusOK, recorder.Code)
}

// A valid configuration builds the configured provider's filter and user
// resolution; neither touches the database until a request resolves a user,
// and a rejected or missing identity never reaches it.
func TestNewAppSessionsBuildsProvider(t *testing.T) {
	const forgedValue = "forged"
	for name, tc := range map[string]struct {
		settings map[string]string
		// keeps is the identity header the provider leaves on a request
		// carrying only that header; the other provider's is dropped.
		keeps, drops string
		// jwtErr is what resolving a request with a malformed Cloudflare
		// token (and nothing else) returns.
		jwtErr error
	}{
		"authentik, the flag default": {
			keeps: appuser.HeaderUID, drops: appuser.HeaderCfAccessJWT, jwtErr: appuser.ErrUnauthenticated,
		},
		"cloudflare": {
			settings: map[string]string{
				config.FlagAuthProvider:               appuser.ProviderCloudflare,
				config.FlagCloudflareAccessTeamDomain: "team.cloudflareaccess.com",
				config.FlagCloudflareAccessAUD:        "aud",
			},
			keeps: appuser.HeaderCfAccessJWT, drops: appuser.HeaderUID, jwtErr: appuser.ErrInvalidToken,
		},
	} {
		t.Run(name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			viper.SetDefault(config.FlagAuthProvider, config.DefaultAuthProvider)
			viper.Set(config.FlagInstance, "maurice-crapettes")
			for k, v := range tc.settings {
				viper.Set(k, v)
			}
			sessions, err := newAppSessions(nil)
			require.NoError(t, err)

			for header, kept := range map[string]bool{tc.keeps: true, tc.drops: false} {
				var seen string
				filtered := sessions.filter(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					seen = r.Header.Get(header)
				}))
				req := httptest.NewRequest(http.MethodPost, "/graphql/query", nil)
				req.Header.Set(header, forgedValue)
				filtered.ServeHTTP(httptest.NewRecorder(), req)
				if kept {
					assert.Equal(t, forgedValue, seen, "the provider's own header %s is kept", header)
				} else {
					assert.Empty(t, seen, "the other provider's header %s is dropped", header)
				}
			}

			var resolveErr error
			resolved := sessions.resolve(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				_, resolveErr = appuser.Current(r.Context())
			}))
			req := httptest.NewRequest(http.MethodPost, "/graphql/query", nil)
			req.Header.Set(appuser.HeaderCfAccessJWT, "not-a-jwt")
			resolved.ServeHTTP(httptest.NewRecorder(), req)
			require.ErrorIs(t, resolveErr, tc.jwtErr)
		})
	}
}

// The header filter runs before httpx.HeaderContext, so the @admin
// directive never sees an Authentik group forged on a cloudflare instance.
func TestAPIRouterDropsForgedAdminGroup(t *testing.T) {
	const query = `{"query":"mutation { dropDatabase }"}`
	cloudflare, err := appuser.NewCloudflareProvider(appuser.CloudflareAccessConfig{
		TeamDomain: "team.cloudflareaccess.com", Audience: "aud",
	})
	require.NoError(t, err)
	passThrough := func(next http.Handler) http.Handler { return next }
	post := func(provider appuser.Provider) string {
		sessions := appSessions{filter: appuser.IgnoreForeignHeaders(provider), resolve: passThrough}
		router := setupAPIRouter(nil, &graph.Resolver{}, sessions, apiAuth{admin: graph.AdminAuth{Group: config.DefaultAdminGroup}})
		req := httptest.NewRequest(http.MethodPost, graphQLPath+"/query", strings.NewReader(query))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Authentik-Groups", config.DefaultAdminGroup)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder.Body.String()
	}

	assert.Contains(t, post(cloudflare), "admin privileges required", "the forged group is dropped")
	assert.NotContains(t, post(appuser.AuthentikProvider{}), "admin privileges required",
		"control: on an authentik instance the same header grants admin")
}
