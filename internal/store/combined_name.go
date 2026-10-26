package store

import "strings"

// ParseCombinedName splits a combined name like "Connor McDavid" into first and last name.
// The name is trimmed, then split at its first space: the first token is the
// first name and the rest, inner spaces included, is the last name. A single
// token has an empty last name; an empty or blank name gives two empty strings.
// Boxscore players and Yahoo player pages both split their names here.
// Examples:
//   - "Connor McDavid" -> ("Connor", "McDavid")
//   - "Pierre-Luc Dubois" -> ("Pierre-Luc", "Dubois")
//   - "James van Riemsdyk" -> ("James", "van Riemsdyk")
func ParseCombinedName(fullName string) (firstName, lastName string) {
	parts := strings.SplitN(strings.TrimSpace(fullName), " ", 2)
	if len(parts) >= 1 {
		firstName = parts[0]
	}
	if len(parts) >= 2 {
		lastName = parts[1]
	}
	return firstName, lastName
}
