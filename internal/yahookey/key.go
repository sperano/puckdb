// Package yahookey parses and formats canonical Yahoo Fantasy Sports keys.
package yahookey

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	idBitSize       = 32
	leaguePartCount = 3
	teamPartCount   = 5
	playerPartCount = 3
	leagueMarker    = "l"
	teamMarker      = "t"
	playerMarker    = "p"
)

// LeagueKey identifies one Yahoo league in one game (season).
type LeagueKey struct {
	GameKey  int
	LeagueID int
}

// String returns the canonical Yahoo league key.
func (k LeagueKey) String() string {
	return strconv.Itoa(k.GameKey) + ".l." + strconv.Itoa(k.LeagueID)
}

// TeamKey identifies one team in a Yahoo league.
type TeamKey struct {
	League LeagueKey
	TeamID int
}

// String returns the canonical Yahoo team key.
func (k TeamKey) String() string {
	return k.League.String() + ".t." + strconv.Itoa(k.TeamID)
}

// PlayerKey identifies one player in a Yahoo game (season).
type PlayerKey struct {
	GameKey  int
	PlayerID int
}

// String returns the canonical Yahoo player key.
func (k PlayerKey) String() string {
	return strconv.Itoa(k.GameKey) + ".p." + strconv.Itoa(k.PlayerID)
}

// ParseLeagueKey parses a canonical "<game-key>.l.<league-id>" key.
func ParseLeagueKey(value string) (LeagueKey, error) {
	parts := strings.Split(value, ".")
	if len(parts) != leaguePartCount || parts[1] != leagueMarker {
		return LeagueKey{}, invalid("league", value)
	}
	gameKey, gameErr := ParseID(parts[0])
	leagueID, leagueErr := ParseID(parts[2])
	if gameErr != nil || leagueErr != nil {
		return LeagueKey{}, invalid("league", value)
	}
	return LeagueKey{GameKey: gameKey, LeagueID: leagueID}, nil
}

// ParseTeamKey parses a canonical "<game-key>.l.<league-id>.t.<team-id>" key.
func ParseTeamKey(value string) (TeamKey, error) {
	parts := strings.Split(value, ".")
	if len(parts) != teamPartCount || parts[1] != leagueMarker || parts[3] != teamMarker {
		return TeamKey{}, invalid("team", value)
	}
	gameKey, gameErr := ParseID(parts[0])
	leagueID, leagueErr := ParseID(parts[2])
	teamID, teamErr := ParseID(parts[4])
	if gameErr != nil || leagueErr != nil || teamErr != nil {
		return TeamKey{}, invalid("team", value)
	}
	return TeamKey{League: LeagueKey{GameKey: gameKey, LeagueID: leagueID}, TeamID: teamID}, nil
}

// ParsePlayerKey parses a canonical "<game-key>.p.<player-id>" key.
func ParsePlayerKey(value string) (PlayerKey, error) {
	parts := strings.Split(value, ".")
	if len(parts) != playerPartCount || parts[1] != playerMarker {
		return PlayerKey{}, invalid("player", value)
	}
	gameKey, gameErr := ParseID(parts[0])
	playerID, playerErr := ParseID(parts[2])
	if gameErr != nil || playerErr != nil {
		return PlayerKey{}, invalid("player", value)
	}
	return PlayerKey{GameKey: gameKey, PlayerID: playerID}, nil
}

// ParseID parses a canonical positive Yahoo identifier that fits the database
// integer columns used for game, league, team, and player IDs.
func ParseID(value string) (int, error) {
	if value == "" || value[0] == '0' || strings.TrimLeft(value, "0123456789") != "" {
		return 0, errors.New("not a canonical positive integer")
	}
	parsed, err := strconv.ParseInt(value, 10, idBitSize)
	if err != nil {
		return 0, errors.New("not a canonical positive integer")
	}
	return int(parsed), nil
}

func invalid(kind, value string) error {
	return fmt.Errorf("invalid Yahoo %s key %q", kind, value)
}
