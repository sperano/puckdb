package store

import "strings"

// BoxscorePlayer holds minimal player info extracted from boxscore appearances.
// This is used to carry player data from extraction to download phases.
type BoxscorePlayer struct {
	ID        int64  `json:"id"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Position  string `json:"position"`
}

// ParseCombinedName splits a combined name like "Connor McDavid" into first and last name.
// For multi-part names, the first token is the first name and the rest is the last name.
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
