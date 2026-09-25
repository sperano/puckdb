package news

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchSendsUserAgentAndValidators(t *testing.T) {
	var gotUA, gotINM, gotIMS string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotINM = r.Header.Get("If-None-Match")
		gotIMS = r.Header.Get("If-Modified-Since")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	last := Validators{ETag: `"abc"`, LastModified: "Tue, 22 Sep 2026 00:00:00 GMT"}
	_, err := Fetch(context.Background(), server.Client(), server.URL, last)
	require.NoError(t, err)
	assert.Equal(t, UserAgent, gotUA)
	assert.Equal(t, last.ETag, gotINM)
	assert.Equal(t, last.LastModified, gotIMS)
}

func TestFetchNotModified304KeepsPreviousValidators(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	last := Validators{ETag: `"abc"`, LastModified: "Tue, 22 Sep 2026 00:00:00 GMT", BodyHash: "old-hash"}
	resp, err := Fetch(context.Background(), server.Client(), server.URL, last)
	require.NoError(t, err)
	assert.True(t, resp.NotModified)
	assert.Equal(t, last, resp.Validators)
}

func TestFetch200ReturnsBodyAndNewValidators(t *testing.T) {
	const body = "feed body"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"new-etag"`)
		w.Header().Set("Last-Modified", "Wed, 23 Sep 2026 00:00:00 GMT")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	resp, err := Fetch(context.Background(), server.Client(), server.URL, Validators{})
	require.NoError(t, err)
	assert.False(t, resp.NotModified)
	assert.Equal(t, []byte(body), resp.Body)
	assert.Equal(t, `"new-etag"`, resp.Validators.ETag)
	assert.Equal(t, "Wed, 23 Sep 2026 00:00:00 GMT", resp.Validators.LastModified)
	sum := sha256.Sum256([]byte(body))
	assert.Equal(t, hex.EncodeToString(sum[:]), resp.Validators.BodyHash)
}

func TestFetch200WithSameBodyHashIsTreatedAsNotModified(t *testing.T) {
	const body = "unchanged body"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A server that ignores conditional headers but serves the same body.
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	sum := sha256.Sum256([]byte(body))
	last := Validators{BodyHash: hex.EncodeToString(sum[:])}
	resp, err := Fetch(context.Background(), server.Client(), server.URL, last)
	require.NoError(t, err)
	assert.True(t, resp.NotModified)
}

func TestFetch429WithRetryAfterIsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	_, err := Fetch(context.Background(), server.Client(), server.URL, Validators{})
	require.Error(t, err)
	var httpErr *HTTPError
	require.True(t, errors.As(err, &httpErr))
	assert.True(t, httpErr.Retryable())
	assert.Equal(t, 120*time.Second, httpErr.RetryAfter)
}

func TestFetchStatusRetryability(t *testing.T) {
	tests := []struct {
		status    int
		retryable bool
	}{
		{http.StatusServiceUnavailable, true},
		{http.StatusNotFound, false},
		{http.StatusForbidden, false},
	}
	for _, tt := range tests {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tt.status)
		}))
		_, err := Fetch(context.Background(), server.Client(), server.URL, Validators{})
		server.Close()
		require.Error(t, err)
		var httpErr *HTTPError
		require.True(t, errors.As(err, &httpErr))
		assert.Equal(t, tt.status, httpErr.StatusCode)
		assert.Equal(t, tt.retryable, httpErr.Retryable(), "status %d", tt.status)
	}
}

func TestFetchBodyTooLarge(t *testing.T) {
	oversized := bytes.Repeat([]byte("x"), maxFeedBytes+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(oversized)
	}))
	defer server.Close()

	_, err := Fetch(context.Background(), server.Client(), server.URL, Validators{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrFeedTooLarge))
}

func TestFetchContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Fetch(ctx, server.Client(), server.URL, Validators{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
}
