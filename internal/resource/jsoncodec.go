package resource

import (
	"encoding/json"
	"fmt"
)

// parsePointer decodes data into a new T and returns its address, wrapping
// a decode error as "<errFormat, filled with args>: <cause>" (via %w, so the
// cause stays unwrappable). errFormat is only formatted into the error
// message on the error path; args are still evaluated by the caller before
// the call, so a caller should not do its own expensive formatting there.
// It is the shared byte-level mechanics behind the pointer-returning
// resource Parse methods in this package that decode a single JSON value
// with no extra handling of their own. SeasonRoster.Parse (nhl_roster.go)
// still decodes independently (left out of this refactor's scope, not for a
// behavioral reason). LeaguePlayers.Parse (yahoo_league_players.go) also
// decodes independently, because it decodes XML, not JSON, and validates
// the response league ID/key against the request before returning. Path,
// URL and any custom Format stay on the resource type, since those carry
// the actual domain information (and some resources need a Format that is
// not plain json.Marshal).
//
// On error it returns nil, not a partially decoded value: json.Unmarshal
// can populate fields before hitting a later decode error, and a caller
// must not mistake that partial value for a parsed result.
func parsePointer[T any](data []byte, errFormat string, args ...any) (*T, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf(errFormat+": %w", append(args, err)...)
	}
	return &v, nil
}

// parseValue is parsePointer for the resource types whose Parse returns a
// value (a slice, or an API envelope struct) rather than a pointer.
// LeaguePlayerPool.Parse (yahoo_league_players.go) is the one value-returning
// JSON Parse left out: it deliberately returns the partially decoded
// manifest on error instead of the zero value, which parseValue's
// zero-on-error contract would change.
func parseValue[T any](data []byte, errFormat string, args ...any) (T, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		var zero T
		return zero, fmt.Errorf(errFormat+": %w", append(args, err)...)
	}
	return v, nil
}
