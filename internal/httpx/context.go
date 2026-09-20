package httpx

import (
	"context"
	"net/http"
)

// HeaderAdminToken carries the shared admin secret. The GraphQL @admin
// directive accepts it as an alternative to Authentik group membership, and
// the CLI's GraphQL client sends it when admin-token is configured.
const HeaderAdminToken = "X-Admin-Token"

// headerContextKey is an unexported type to avoid collisions with context
// keys defined in other packages.
type headerContextKey struct{}

// HeaderContext is a chi-compatible middleware that stashes the incoming
// request's http.Header in the request context, so downstream code (e.g.
// GraphQL directives) can inspect headers without threading *http.Request
// through unrelated call chains.
func HeaderContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), headerContextKey{}, r.Header)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// HeadersFromContext returns the http.Header stored in ctx by HeaderContext,
// or nil if none is present.
func HeadersFromContext(ctx context.Context) http.Header {
	headers, _ := ctx.Value(headerContextKey{}).(http.Header)
	return headers
}
