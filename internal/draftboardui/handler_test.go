package draftboardui

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandlerServesBoardAndAssets(t *testing.T) {
	server := httptest.NewServer(Handler())
	t.Cleanup(server.Close)

	for _, path := range []string{"/", "/app.js", "/style.css"} {
		response, err := http.Get(server.URL + path)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, response.StatusCode)
		assert.NoError(t, response.Body.Close())
	}
}

func TestHandlerDoesNotServeClientTests(t *testing.T) {
	server := httptest.NewServer(Handler())
	t.Cleanup(server.Close)

	for _, path := range []string{"/app_test.js", "/harness_test.js"} {
		response, err := http.Get(server.URL + path)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, response.StatusCode, path)
		assert.NoError(t, response.Body.Close())
	}
}

var (
	leagueSelectPattern = regexp.MustCompile(`(?s)<select id="league"[^>]*>(.*?)</select>`)
	optionValuePattern  = regexp.MustCompile(`<option[^>]*\bvalue="([^"]*)"`)
)

// TestLeagueSelectorShipsNoLeagues keeps league IDs out of the embedded page:
// the client fills the selector from draftLeagues, so the only shipped option
// is the empty placeholder.
func TestLeagueSelectorShipsNoLeagues(t *testing.T) {
	page, err := assets.ReadFile("web/index.html")
	require.NoError(t, err)
	selector := leagueSelectPattern.FindSubmatch(page)
	require.NotNil(t, selector, "index.html has no league selector")
	for _, option := range optionValuePattern.FindAllSubmatch(selector[1], -1) {
		assert.Empty(t, string(option[1]), "shipped league option")
	}
}
