package shared

import (
	"context"
	"testing"

	"github.com/sperano/puckdb/internal/httpx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewNHLClient(t *testing.T) {
	t.Parallel()

	client := NewNHLClient()
	require.NotNil(t, client)
}

func TestNewYahooDownloader_ReturnsNonNilFunc(t *testing.T) {
	t.Parallel()

	// Pass nil for the *redis.Client: NewYahooDownloader only captures it in a
	// closure; the network call is not made here so nil is safe for this test.
	downloader := NewYahooDownloader(nil, httpx.YahooAuth{})
	assert.NotNil(t, downloader)
}

func TestNewYahooDownloader_UsesGivenAuth(t *testing.T) {
	t.Parallel()

	// Empty credentials fail before Redis is touched, so nil is safe: the
	// error proves the downloader checks the auth it was built with.
	downloader := NewYahooDownloader(nil, httpx.YahooAuth{})
	_, err := downloader(context.Background(), "https://fantasysports.yahooapis.com/fantasy/v2/game/nhl")
	require.ErrorContains(t, err, "yahoo-oauth2-client-id is empty")
}
