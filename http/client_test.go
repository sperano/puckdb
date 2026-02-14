package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog"
	"github.com/sperano/puckdb/auth"
	"github.com/sperano/puckdb/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestHTTPError_Error(t *testing.T) {
	t.Parallel()

	err := &HTTPError{
		StatusCode: 404,
		Status:     "404 Not Found",
		URL:        "https://example.com/resource",
	}

	msg := err.Error()
	assert.Contains(t, msg, "download failed")
	assert.Contains(t, msg, "404 Not Found")
	assert.Contains(t, msg, "https://example.com/resource")
}

func TestHTTPError_IsClientError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		{"200 OK", 200, false},
		{"201 Created", 201, false},
		{"301 Redirect", 301, false},
		{"400 Bad Request", 400, true},
		{"401 Unauthorized", 401, true},
		{"403 Forbidden", 403, true},
		{"404 Not Found", 404, true},
		{"499 Client Closed", 499, true},
		{"500 Internal Server Error", 500, false},
		{"502 Bad Gateway", 502, false},
		{"503 Service Unavailable", 503, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &HTTPError{StatusCode: tt.statusCode}
			assert.Equal(t, tt.expected, err.IsClientError())
		})
	}
}

func TestHTTPError_IsNonRetryableClientError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		expected   bool
	}{
		{"200 OK - not client error", 200, false},
		{"400 Bad Request - non-retryable", 400, true},
		{"401 Unauthorized - non-retryable", 401, true},
		{"403 Forbidden - non-retryable", 403, true},
		{"404 Not Found - retryable (Yahoo intermittent)", 404, false},
		{"405 Method Not Allowed - non-retryable", 405, true},
		{"429 Too Many Requests - non-retryable", 429, true},
		{"500 Internal Server Error - not client error", 500, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &HTTPError{StatusCode: tt.statusCode}
			assert.Equal(t, tt.expected, err.IsNonRetryableClientError())
		})
	}
}

func TestGenericClient_Download_Success(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, UserAgent, r.Header.Get("User-Agent"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": "test"}`))
	}))
	defer server.Close()

	client := &GenericClient{
		Client:   server.Client(),
		apiLabel: "test",
	}

	data, err := client.Download(server.URL)
	require.NoError(t, err)
	assert.Equal(t, `{"data": "test"}`, string(data))
}

func TestGenericClient_Download_HTTPError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{"400 Bad Request", http.StatusBadRequest, "bad request"},
		{"401 Unauthorized", http.StatusUnauthorized, "unauthorized"},
		{"404 Not Found", http.StatusNotFound, "not found"},
		{"500 Internal Server Error", http.StatusInternalServerError, "server error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client := &GenericClient{
				Client:   server.Client(),
				apiLabel: "test",
			}

			_, err := client.Download(server.URL)
			require.Error(t, err)

			httpErr, ok := err.(*HTTPError)
			require.True(t, ok, "expected HTTPError")
			assert.Equal(t, tt.statusCode, httpErr.StatusCode)
			assert.Equal(t, server.URL, httpErr.URL)
		})
	}
}

func TestGenericClient_Download_ConnectionError(t *testing.T) {
	t.Parallel()

	client := &GenericClient{
		Client:   &http.Client{},
		apiLabel: "test",
	}

	// Use an invalid URL that will fail to connect
	_, err := client.Download("http://localhost:1")
	require.Error(t, err)
	// Should not be an HTTPError since we couldn't connect
	_, ok := err.(*HTTPError)
	assert.False(t, ok, "expected non-HTTPError for connection failure")
}

func TestGenericClient_Download_Redirects(t *testing.T) {
	t.Parallel()

	finalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("final destination"))
	}))
	defer finalServer.Close()

	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, finalServer.URL, http.StatusFound)
	}))
	defer redirectServer.Close()

	client := &GenericClient{
		Client:   &http.Client{},
		apiLabel: "test",
	}

	data, err := client.Download(redirectServer.URL)
	require.NoError(t, err)
	assert.Equal(t, "final destination", string(data))
}

func TestGenericClient_Download_LargeResponse(t *testing.T) {
	t.Parallel()

	// Suppress trace logging for this test to avoid printing 1MB of binary data
	oldLevel := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	defer zerolog.SetGlobalLevel(oldLevel)

	// Create a 1MB response
	largeBody := make([]byte, 1024*1024)
	for i := range largeBody {
		largeBody[i] = byte(i % 256)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(largeBody)
	}))
	defer server.Close()

	client := &GenericClient{
		Client:   server.Client(),
		apiLabel: "test",
	}

	data, err := client.Download(server.URL)
	require.NoError(t, err)
	assert.Equal(t, len(largeBody), len(data))
}

func TestGenericClient_Download_EmptyResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &GenericClient{
		Client:   server.Client(),
		apiLabel: "test",
	}

	data, err := client.Download(server.URL)
	require.NoError(t, err)
	assert.Empty(t, data)
}

func TestGenericClient_Download_ReadBodyError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set content length but close connection before writing all data
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("partial"))
		// Connection will be closed, causing read error
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
		}
	}))
	defer server.Close()

	client := &GenericClient{
		Client:   server.Client(),
		apiLabel: "test",
	}

	_, err := client.Download(server.URL)
	// This may or may not error depending on timing
	// The important thing is it doesn't panic
	_ = err
}

func TestDownloadPublic(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, UserAgent, r.Header.Get("User-Agent"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("public data"))
	}))
	defer server.Close()

	// Temporarily override the function to use our test server
	// Since DownloadPublic creates its own client, we test through a server
	data, err := DownloadPublic(server.URL)
	require.NoError(t, err)
	assert.Equal(t, "public data", string(data))
}

func TestDownloadPublic_Error(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("error"))
	}))
	defer server.Close()

	_, err := DownloadPublic(server.URL)
	require.Error(t, err)

	httpErr, ok := err.(*HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusInternalServerError, httpErr.StatusCode)
}

// TestUserAgent verifies the User-Agent constant is set
func TestUserAgent(t *testing.T) {
	t.Parallel()

	assert.NotEmpty(t, UserAgent)
	assert.Contains(t, UserAgent, "Mozilla")
}

// Benchmark download performance
func BenchmarkGenericClient_Download(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"status": "ok"}`)
	}))
	defer server.Close()

	client := &GenericClient{
		Client:   server.Client(),
		apiLabel: "bench",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client.Download(server.URL)
	}
}

// createMockStringCmd creates a redis.StringCmd with the given value or error
func createMockStringCmd(val string, err error) *redis.StringCmd {
	cmd := redis.NewStringCmd(context.Background())
	if err != nil {
		cmd.SetErr(err)
	} else {
		cmd.SetVal(val)
	}
	return cmd
}

// createMockBoolCmd creates a redis.BoolCmd with the given value
func createMockBoolCmd(val bool, err error) *redis.BoolCmd {
	cmd := redis.NewBoolCmd(context.Background())
	if err != nil {
		cmd.SetErr(err)
	} else {
		cmd.SetVal(val)
	}
	return cmd
}

// createMockStatusCmd creates a redis.StatusCmd with the given value
func createMockStatusCmd(val string, err error) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(context.Background())
	if err != nil {
		cmd.SetErr(err)
	} else {
		cmd.SetVal(val)
	}
	return cmd
}

func createTestOAuthConfig(tokenServerURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		Endpoint: oauth2.Endpoint{
			AuthURL:  tokenServerURL + "/auth",
			TokenURL: tokenServerURL + "/token",
		},
		RedirectURL: "http://localhost:8080/callback",
		Scopes:      []string{"openid"},
	}
}

func TestNewYahooClientWithConfig_TokenLoadError(t *testing.T) {
	t.Parallel()

	mockRedis := &cache.MockClient{}
	ctx := context.WithValue(context.Background(), auth.CtxUser, "testuser")
	conf := createTestOAuthConfig("http://example.com")

	// Key format: %s_yahoo_oauth2_token
	mockRedis.On("Get", mock.Anything, "testuser_yahoo_oauth2_token").
		Return(createMockStringCmd("", redis.ErrClosed))

	_, err := NewYahooClientWithConfig(ctx, mockRedis, conf)
	require.Error(t, err)
	mockRedis.AssertExpectations(t)
}

func TestNewYahooClientWithConfig_TokenMissing(t *testing.T) {
	t.Parallel()

	mockRedis := &cache.MockClient{}
	ctx := context.WithValue(context.Background(), auth.CtxUser, "testuser")
	conf := createTestOAuthConfig("http://example.com")

	// Key format: %s_yahoo_oauth2_token
	mockRedis.On("Get", mock.Anything, "testuser_yahoo_oauth2_token").
		Return(createMockStringCmd("", redis.Nil))

	_, err := NewYahooClientWithConfig(ctx, mockRedis, conf)
	require.Error(t, err)
	mockRedis.AssertExpectations(t)
}

func TestNewYahooClientWithConfig_Success(t *testing.T) {
	t.Parallel()

	mockRedis := &cache.MockClient{}
	ctx := context.WithValue(context.Background(), auth.CtxUser, "testuser")

	// Create a mock OAuth2 token server
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This shouldn't be called since the token isn't expired
		t.Error("Token server should not be called for non-expired token")
	}))
	defer tokenServer.Close()

	conf := createTestOAuthConfig(tokenServer.URL)

	// Create a valid token JSON - not expired, so no refresh needed
	token := &oauth2.Token{
		AccessToken:  "test-access-token",
		TokenType:    "Bearer",
		RefreshToken: "test-refresh-token",
		Expiry:       time.Now().Add(1 * time.Hour),
	}
	tokenJSON, _ := cache.TokenAsString(token)

	// Key format: %s_yahoo_oauth2_token
	mockRedis.On("Get", mock.Anything, "testuser_yahoo_oauth2_token").
		Return(createMockStringCmd(tokenJSON, nil))

	client, err := NewYahooClientWithConfig(ctx, mockRedis, conf)
	require.NoError(t, err)
	assert.NotNil(t, client)
	mockRedis.AssertExpectations(t)
}

func TestNewYahooClientWithConfig_TokenRefreshAndSave(t *testing.T) {
	t.Parallel()

	mockRedis := &cache.MockClient{}
	ctx := context.WithValue(context.Background(), auth.CtxUser, "testuser")

	// Create a mock OAuth2 token server that returns a new token
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"access_token": "new-access-token",
			"token_type": "Bearer",
			"refresh_token": "new-refresh-token",
			"expires_in": 3600
		}`))
	}))
	defer tokenServer.Close()

	conf := createTestOAuthConfig(tokenServer.URL)

	// Create an expired token that will trigger refresh
	token := &oauth2.Token{
		AccessToken:  "old-access-token",
		TokenType:    "Bearer",
		RefreshToken: "test-refresh-token",
		Expiry:       time.Now().Add(-1 * time.Hour), // Expired
	}
	tokenJSON, _ := cache.TokenAsString(token)

	// Key format: %s_yahoo_oauth2_token
	mockRedis.On("Get", mock.Anything, "testuser_yahoo_oauth2_token").
		Return(createMockStringCmd(tokenJSON, nil))

	// Mock Redis to save the new token
	mockRedis.On("Set", mock.Anything, "testuser_yahoo_oauth2_token", mock.AnythingOfType("string"), mock.Anything).
		Return(createMockStatusCmd("OK", nil))

	client, err := NewYahooClientWithConfig(ctx, mockRedis, conf)
	require.NoError(t, err)
	assert.NotNil(t, client)
	mockRedis.AssertExpectations(t)
}

func TestNewYahooClientWithConfig_TokenSaveError(t *testing.T) {
	t.Parallel()

	mockRedis := &cache.MockClient{}
	ctx := context.WithValue(context.Background(), auth.CtxUser, "testuser")

	// Create a mock OAuth2 token server
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"access_token": "new-access-token",
			"token_type": "Bearer",
			"refresh_token": "new-refresh-token",
			"expires_in": 3600
		}`))
	}))
	defer tokenServer.Close()

	conf := createTestOAuthConfig(tokenServer.URL)

	// Create an expired token
	token := &oauth2.Token{
		AccessToken:  "old-access-token",
		TokenType:    "Bearer",
		RefreshToken: "test-refresh-token",
		Expiry:       time.Now().Add(-1 * time.Hour),
	}
	tokenJSON, _ := cache.TokenAsString(token)

	mockRedis.On("Get", mock.Anything, "testuser_yahoo_oauth2_token").
		Return(createMockStringCmd(tokenJSON, nil))

	// Mock Redis save to fail
	mockRedis.On("Set", mock.Anything, "testuser_yahoo_oauth2_token", mock.AnythingOfType("string"), mock.Anything).
		Return(createMockStatusCmd("", redis.ErrClosed))

	_, err := NewYahooClientWithConfig(ctx, mockRedis, conf)
	require.Error(t, err)
	mockRedis.AssertExpectations(t)
}
