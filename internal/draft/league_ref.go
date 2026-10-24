package draft

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// leagueKeyParts is the number of dot-separated parts of a full Yahoo league
// key: game key, the "l" marker and the league ID.
const leagueKeyParts = 3

// leagueKeyMarker is the middle part of a full Yahoo league key.
const leagueKeyMarker = "l"

// leagueIDBits bounds parsed game keys and league IDs to the integer columns
// that store them.
const leagueIDBits = 32

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
	parts := strings.Split(key, ".")
	if len(parts) != leagueKeyParts || parts[1] != leagueKeyMarker {
		return 0, 0, fmt.Errorf("%w: invalid Yahoo league key %q", ErrMalformedLeagueRef, key)
	}
	gameKey, gameErr := positiveID(parts[0])
	leagueID, leagueErr := positiveID(parts[2])
	if gameErr != nil || leagueErr != nil {
		return 0, 0, fmt.Errorf("%w: invalid Yahoo league key %q", ErrMalformedLeagueRef, key)
	}
	return gameKey, leagueID, nil
}

// ParseLeagueID parses a positive numeric Yahoo league ID.
func ParseLeagueID(value string) (int, error) {
	id, err := positiveID(value)
	if err != nil {
		return 0, fmt.Errorf("%w: %q is neither a full Yahoo league key nor a positive numeric league ID",
			ErrMalformedLeagueRef, value)
	}
	return id, nil
}

func positiveID(value string) (int, error) {
	parsed, err := strconv.ParseInt(value, 10, leagueIDBits)
	if err != nil || parsed <= 0 {
		return 0, errors.New("not a positive integer")
	}
	return int(parsed), nil
}
