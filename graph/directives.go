package graph

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/httpx"
	"github.com/spf13/viper"
)

// Authentik forward-auth headers consulted by AdminDirective. Authentik
// separates multiple group values with "|"; "," is also tolerated for
// callers that put the header together by hand (e.g. local testing).
const (
	headerAuthentikGroups   = "X-authentik-groups"
	headerAuthentikUsername = "X-authentik-username"
)

// errAdminAccessDenied is returned by AdminDirective when neither the
// Authentik group nor the admin token grant access.
var errAdminAccessDenied = errors.New("access denied: admin privileges required")

// AdminDirective implements the @admin GraphQL directive. It authorizes the
// request if either:
//   - the X-authentik-groups header (set by an Authentik forward-auth proxy)
//     contains the configured admin group, or
//   - the configured admin token is non-empty and the X-Admin-Token header
//     matches it (compared in constant time).
//
// Otherwise it denies the request and logs a warning naming the requesting
// user, when known.
func AdminDirective(ctx context.Context, obj interface{}, next graphql.Resolver) (interface{}, error) {
	headers := httpx.HeadersFromContext(ctx)

	if hasAdminGroup(headers) || hasValidAdminToken(headers) {
		return next(ctx)
	}

	log.Warn().
		Str("username", headers.Get(headerAuthentikUsername)).
		Msg("admin directive: access denied")
	return nil, errAdminAccessDenied
}

// hasAdminGroup reports whether headers carries the configured admin group
// in its Authentik groups header.
func hasAdminGroup(headers http.Header) bool {
	adminGroup := viper.GetString(config.FlagAdminGroup)
	if adminGroup == "" {
		return false
	}
	for _, group := range splitGroups(headers.Get(headerAuthentikGroups)) {
		if group == adminGroup {
			return true
		}
	}
	return false
}

// hasValidAdminToken reports whether the admin token feature is enabled
// (i.e. configured with a non-empty value) and headers carries a matching
// X-Admin-Token value.
func hasValidAdminToken(headers http.Header) bool {
	adminToken := viper.GetString(config.FlagAdminToken)
	if adminToken == "" {
		return false
	}
	provided := headers.Get(httpx.HeaderAdminToken)
	return subtle.ConstantTimeCompare([]byte(provided), []byte(adminToken)) == 1
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
