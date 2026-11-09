package yahoolink

import (
	"fmt"
	"strconv"
	"strings"
)

// leagueIDListSeparator separates league IDs in --maurice-yahoo-leagues.
const leagueIDListSeparator = ","

// splitNonEmpty splits list on leagueIDListSeparator, trims blanks around
// each field and drops empty ones (so "" and trailing commas parse to no
// IDs instead of one blank field).
func splitNonEmpty(list string) []string {
	var fields []string
	for _, field := range strings.Split(list, leagueIDListSeparator) {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			fields = append(fields, trimmed)
		}
	}
	return fields
}

// parseLeagueID parses one field of --maurice-yahoo-leagues: a positive
// Yahoo league ID such as "29249", not a full league_key (yahoo_teams and
// yahoo_team_managers key by the numeric ID alone).
func parseLeagueID(field string) (int32, error) {
	id, err := strconv.ParseInt(field, 10, 32)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid Yahoo league ID %q: want a positive number, e.g. 29249", field)
	}
	return int32(id), nil
}
