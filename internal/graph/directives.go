package graph

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/httpx"
)

// Authentik forward-auth headers consulted by the @admin directive. Authentik
// separates multiple group values with "|"; "," is also tolerated for
// callers that put the header together by hand (e.g. local testing).
const (
	headerAuthentikGroups   = "X-authentik-groups"
	headerAuthentikUsername = "X-authentik-username"
)

// errAdminAccessDenied is returned by the @admin directive when neither the
// Authentik group nor the admin token grant access.
var errAdminAccessDenied = errors.New("access denied: admin privileges required")

// AdminAuth configures the @admin directive. The command layer reads it
// from --admin-group and --admin-token.
type AdminAuth struct {
	// Group is the Authentik group whose members are admins; empty
	// disables group authorization.
	Group string
	// Token is the shared secret accepted in X-Admin-Token; empty disables
	// token authorization.
	Token string
}

// NewAdminDirective returns the @admin GraphQL directive. It authorizes the
// request if either:
//   - the X-authentik-groups header (set by an Authentik forward-auth proxy)
//     contains auth.Group, or
//   - auth.Token is non-empty and the X-Admin-Token header matches it
//     (compared in constant time).
//
// Otherwise it denies the request and logs a warning naming the requesting
// user, when known.
func NewAdminDirective(auth AdminAuth) func(ctx context.Context, obj any, next graphql.Resolver) (any, error) {
	return func(ctx context.Context, _ any, next graphql.Resolver) (any, error) {
		headers := httpx.HeadersFromContext(ctx)

		if auth.hasAdminGroup(headers) || auth.hasValidAdminToken(headers) {
			return next(ctx)
		}

		log.Warn().
			Str("username", headers.Get(headerAuthentikUsername)).
			Msg("admin directive: access denied")
		return nil, errAdminAccessDenied
	}
}

// hasAdminGroup reports whether headers carries the admin group in its
// Authentik groups header.
func (auth AdminAuth) hasAdminGroup(headers http.Header) bool {
	if auth.Group == "" {
		return false
	}
	for _, group := range splitGroups(headers.Get(headerAuthentikGroups)) {
		if group == auth.Group {
			return true
		}
	}
	return false
}

// hasValidAdminToken reports whether the admin token feature is enabled
// (i.e. configured with a non-empty value) and headers carries a matching
// X-Admin-Token value.
func (auth AdminAuth) hasValidAdminToken(headers http.Header) bool {
	if auth.Token == "" {
		return false
	}
	provided := headers.Get(httpx.HeaderAdminToken)
	return subtle.ConstantTimeCompare([]byte(provided), []byte(auth.Token)) == 1
}

// splitGroups splits an Authentik groups header value on "|" (Authentik's
// separator), also tolerating ",". Empty and whitespace-only entries are
// dropped.
func splitGroups(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '|' || r == ','
	})
	groups := make([]string, 0, len(fields))
	for _, f := range fields {
		if trimmed := strings.TrimSpace(f); trimmed != "" {
			groups = append(groups, trimmed)
		}
	}
	return groups
}
