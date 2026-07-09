package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeaderContext_RoundTrip(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/graphql/query", nil)
	req.Header.Set("X-Authentik-Groups", "puckdb-admins")
	req.Header.Set("X-Admin-Token", "s3cr3t")

	var gotHeaders http.Header
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = HeadersFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	HeaderContext(next).ServeHTTP(httptest.NewRecorder(), req)

	require.NotNil(t, gotHeaders)
	assert.Equal(t, "puckdb-admins", gotHeaders.Get("X-Authentik-Groups"))
	assert.Equal(t, "s3cr3t", gotHeaders.Get("X-Admin-Token"))
}

func TestHeadersFromContext_Absent(t *testing.T) {
	t.Parallel()

	got := HeadersFromContext(t.Context())
	assert.Nil(t, got)
}
