package store

import (
	"strconv"
)

// YahooPlayerID is the unique identifier Yahoo assigns to a player.
// This is distinct from nhl.PlayerID to prevent accidental mixing of ID namespaces.
type YahooPlayerID int

func (id YahooPlayerID) String() string {
	return strconv.Itoa(int(id))
}

// MissingPlayerLandingData holds the minimal player info stored when a 404 is cached.
// This data is extracted from boxscore appearances.
type MissingPlayerLandingData struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Position  string `json:"position"`
}
