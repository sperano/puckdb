package graph

import (
	"strings"

	"github.com/sperano/puckdb/sqlcdb"
)

// ============================================================================
// Data-query resolver helpers — shared utilities for data.resolvers.go.
//
// Lives in a non-gqlgen-managed file (like simulation_helpers.go) so the
// resolver scaffolding stays pristine across regens. gqlgen strips helper
// functions out of *.resolvers.go files on regeneration; keeping them here
// avoids that.
// ============================================================================

const (
	// maxListLimit bounds client-supplied list limits (e.g. the games
	// query) and mirrors the server-side LIMIT baked into the otherwise
	// unbounded player queries (ListPlayers and SearchPlayersByName both
	// LIMIT 500). Prevents a single query from streaming an unbounded
	// result set into memory.
	maxListLimit = 500
	// maxSearchQueryLen bounds searchPlayers input length so a pathological
	// multi-kilobyte term can't drive a huge LIKE scan.
	maxSearchQueryLen = 100
)

// db returns the resolver's sqlc query handle, or errDatabaseNotConfigured
// when the resolver was constructed without a live database connection.
func (r *Resolver) db() (*sqlcdb.Queries, error) {
	if r.Queries == nil {
		return nil, errDatabaseNotConfigured
	}
	return r.Queries, nil
}

// clampListLimit caps a client-supplied list limit at maxListLimit. A nil or
// non-positive limit is passed through unchanged so the underlying query's own
// default/unbounded semantics apply.
func clampListLimit(limit *int) *int {
	if limit == nil || *limit <= 0 {
		return limit
	}
	if *limit > maxListLimit {
		capped := maxListLimit
		return &capped
	}
	return limit
}

// escapeLikePattern escapes the LIKE metacharacters in user input so a term
// like "%" is matched literally instead of wildcard-matching everything.
// Backslash is escaped first so the escapes it introduces aren't re-escaped.
func escapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}
