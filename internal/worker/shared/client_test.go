package shared

import (
	"testing"

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
	downloader := NewYahooDownloader(nil)
	assert.NotNil(t, downloader)
}
