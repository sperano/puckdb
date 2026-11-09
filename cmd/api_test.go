package cmd

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sperano/puckdb/internal/appuser"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/graph"
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

// A valid configuration builds the provider's filter and user resolution;
// neither touches the database until a request resolves a user.
func TestNewAppSessionsBuildsProvider(t *testing.T) {
	for name, settings := range map[string]map[string]string{
		"authentik, the flag default": {},
		"cloudflare": {
			config.FlagAuthProvider:               appuser.ProviderCloudflare,
			config.FlagCloudflareAccessTeamDomain: "team.cloudflareaccess.com",
			config.FlagCloudflareAccessAUD:        "aud",
		},
	} {
		t.Run(name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			viper.SetDefault(config.FlagAuthProvider, config.DefaultAuthProvider)
			viper.Set(config.FlagInstance, "maurice-crapettes")
			for k, v := range settings {
				viper.Set(k, v)
			}
			sessions, err := newAppSessions(nil)
			require.NoError(t, err)
			assert.NotNil(t, sessions.filter)
			assert.NotNil(t, sessions.resolve)
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
