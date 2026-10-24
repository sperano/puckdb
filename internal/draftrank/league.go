package draftrank

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sperano/puckdb/internal/draft"
)

// ErrUnknownLeague means a league reference cannot be resolved.
var ErrUnknownLeague = errors.New("unknown league")

// LeagueRef names a league either by key or by season and numeric ID.
type LeagueRef struct {
	Season    int
	LeagueID  int
	LeagueKey string
}

// ParseLeague reads a full league key ("465.l.1001") or a numeric league ID.
// A key names its league and season by itself: season does not restrict it,
// the key's imported rules give its season. A numeric ID needs the season, since
// Yahoo league IDs are only unique within one season: callers pass the one
// they default to (the CLI and GraphQL use the current season). Unlike
// draftwatch.ResolveIdentity, a numeric ID never falls back to the newest
// imported season. Keys and IDs are parsed by draft.ParseLeagueKey and
// draft.ParseLeagueID, as in draftwatch.
func ParseLeague(value string, season int) (LeagueRef, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return LeagueRef{}, fmt.Errorf("%w: a league key or ID is required", ErrUnknownLeague)
	}
	if draft.IsLeagueKey(value) {
		if _, _, err := draft.ParseLeagueKey(value); err != nil {
			return LeagueRef{}, fmt.Errorf("%w: malformed league key %q", ErrUnknownLeague, value)
		}
		return LeagueRef{LeagueKey: value, Season: season}, nil
	}
	id, err := draft.ParseLeagueID(value)
	if err != nil {
		return LeagueRef{}, fmt.Errorf("%w: %q is neither a league key nor a league ID", ErrUnknownLeague, value)
	}
	if season <= 0 {
		return LeagueRef{}, fmt.Errorf("%w: league ID %d needs a season", ErrUnknownLeague, id)
	}
	return LeagueRef{Season: season, LeagueID: id}, nil
}

// resolved reports whether the reference has its season and ID.
func (r LeagueRef) resolved() bool {
	return r.Season > 0 && r.LeagueID > 0
}
