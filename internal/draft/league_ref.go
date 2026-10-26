package draft

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sperano/puckdb/internal/yahookey"
)

// AnySeason, as the season of a league lookup, matches the league's rules in
// any season and picks the most recently seen one.
const AnySeason = 0

// ErrMalformedLeagueRef means a league reference is neither a full Yahoo
// league key nor a positive numeric league ID.
var ErrMalformedLeagueRef = errors.New("malformed league reference")

// IsLeagueKey reports whether a league reference is written as a full key
// ("465.l.1001") rather than a numeric league ID. It does not validate it:
// ParseLeagueKey does.
func IsLeagueKey(ref string) bool {
	return strings.Contains(ref, ".")
}

// ParseLeagueKey parses a full Yahoo league key "<game key>.l.<league ID>"
// into its positive game key and league ID. A key names one league in one
// season by itself; a numeric ID is unique only within a season.
func ParseLeagueKey(key string) (gameKey, leagueID int, err error) {
	parsed, parseErr := yahookey.ParseLeagueKey(key)
	if parseErr != nil {
		return 0, 0, fmt.Errorf("%w: invalid Yahoo league key %q", ErrMalformedLeagueRef, key)
	}
	return parsed.GameKey, parsed.LeagueID, nil
}

// ParseLeagueID parses a positive numeric Yahoo league ID.
func ParseLeagueID(value string) (int, error) {
	id, err := yahookey.ParseID(value)
	if err != nil {
		return 0, fmt.Errorf("%w: %q is neither a full Yahoo league key nor a positive numeric league ID",
			ErrMalformedLeagueRef, value)
	}
	return id, nil
}
