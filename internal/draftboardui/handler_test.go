package draftboardui

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
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

// shippedLeagueSelector is the whole body of the league <select> in
// index.html: one empty placeholder, since the client fills the selector from
// draftLeagues.
const shippedLeagueSelector = `<option value="">Loading leagues…</option>`

// TestLeagueSelectorShipsNoLeagues keeps league IDs out of the embedded page
// by requiring the selector to hold exactly the placeholder: no other option,
// and no league ID in its value or its text.
func TestLeagueSelectorShipsNoLeagues(t *testing.T) {
	leagueSelect := regexp.MustCompile(`(?s)<select id="league"[^>]*>(.*?)</select>`)
	page, err := assets.ReadFile("web/index.html")
	require.NoError(t, err)
	selector := leagueSelect.FindSubmatch(page)
	require.NotNil(t, selector, "index.html has no league selector")
	assert.Equal(t, shippedLeagueSelector, strings.TrimSpace(string(selector[1])))
}
