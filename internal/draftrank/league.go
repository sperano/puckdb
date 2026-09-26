package draftrank

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrUnknownLeague means a league reference cannot be resolved.
var ErrUnknownLeague = errors.New("unknown league")

// leagueKeyMarker separates the game key and league ID of a Yahoo league
// key ("465.l.1001").
const leagueKeyMarker = ".l."

// LeagueRef names a league either by key or by season and numeric ID.
type LeagueRef struct {
	Season    int
	LeagueID  int
	LeagueKey string
}

// ParseLeague reads a league key ("465.l.1001") or a numeric league ID. A
// numeric ID needs the season, since Yahoo league IDs are only unique within
// one season; a key carries its own.
func ParseLeague(value string, season int) (LeagueRef, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return LeagueRef{}, fmt.Errorf("%w: a league key or ID is required", ErrUnknownLeague)
	}
	if strings.Contains(value, leagueKeyMarker) {
		_, id, _ := strings.Cut(value, leagueKeyMarker)
		if n, err := strconv.Atoi(id); err != nil || n <= 0 {
			return LeagueRef{}, fmt.Errorf("%w: malformed league key %q", ErrUnknownLeague, value)
		}
		return LeagueRef{LeagueKey: value, Season: season}, nil
	}
	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
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
