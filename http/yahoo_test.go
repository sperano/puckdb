package http

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetNoCacheHeaders(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	setNoCacheHeaders(w)

	assert.Equal(t, "no-cache, no-store, must-revalidate", w.Header().Get("Cache-Control"))
	assert.Equal(t, "no-cache", w.Header().Get("Pragma"))
	assert.Equal(t, "0", w.Header().Get("Expires"))
}

func TestHandleError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		statusCode     int
		err            error
		expectedBody   string
		expectedStatus int
	}{
		{
			name:           "bad request",
			statusCode:     http.StatusBadRequest,
			err:            assert.AnError,
			expectedBody:   `{"error":"assert.AnError general error for testing"}`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "internal server error",
			statusCode:     http.StatusInternalServerError,
			err:            assert.AnError,
			expectedBody:   `{"error":"assert.AnError general error for testing"}`,
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:           "forbidden",
			statusCode:     http.StatusForbidden,
			err:            assert.AnError,
			expectedBody:   `{"error":"assert.AnError general error for testing"}`,
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handleError(w, tt.statusCode, tt.err)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.Contains(t, w.Body.String(), "error")
		})
	}
}

func TestYahooLandedHandler(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/yahoo/landed", nil)
	w := httptest.NewRecorder()

	YahooLandedHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), "Login Successful")
	assert.Contains(t, w.Body.String(), "/yahoo/login")
}

func TestYahooAuthenticatedHandler_MissingCode(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/yahoo/callback", nil)
	w := httptest.NewRecorder()

	handler := YahooAuthenticatedHandler(nil)
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "missing authorization code")
}

func TestYahooAuthenticatedHandler_OAuthError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		errorParam  string
		errorDesc   string
		wantContain string
	}{
		{
			name:        "access_denied",
			errorParam:  "access_denied",
			errorDesc:   "User denied access",
			wantContain: "access_denied",
		},
		{
			name:        "invalid_request",
			errorParam:  "invalid_request",
			errorDesc:   "Missing required parameter",
			wantContain: "invalid_request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "/yahoo/callback?error=" + url.QueryEscape(tt.errorParam) +
				"&error_description=" + url.QueryEscape(tt.errorDesc)
			req := httptest.NewRequest(http.MethodGet, reqURL, nil)
			w := httptest.NewRecorder()

			handler := YahooAuthenticatedHandler(nil)
			handler(w, req)

			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.Contains(t, w.Body.String(), tt.wantContain)
		})
	}
}

func TestYahooAuthenticatedHandler_SetsNoCacheHeaders(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/yahoo/callback", nil)
	w := httptest.NewRecorder()

	handler := YahooAuthenticatedHandler(nil)
	handler(w, req)

	// Should set no-cache headers even when returning an error
	assert.Equal(t, "no-cache, no-store, must-revalidate", w.Header().Get("Cache-Control"))
	assert.Equal(t, "no-cache", w.Header().Get("Pragma"))
	assert.Equal(t, "0", w.Header().Get("Expires"))
}
