package draftboardui

import (
	"net/http"
	"net/http/httptest"
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
