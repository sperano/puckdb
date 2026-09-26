package resource

import (
	"encoding/json"
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
)

// SeasonRoster represents the full roster for a team in a specific season.
// Fetched via RosterSeason and includes players who never appeared in a game.
type SeasonRoster struct {
	Season     int
	TeamAbbrev string
}

func (r SeasonRoster) Path() string {
	return fmt.Sprintf("seasons/%d/rosters/roster-%s.json", r.Season, r.TeamAbbrev)
}

func (r SeasonRoster) Type() core.FileType { return core.SeasonRoster }

// Parse decodes both the NHL roster endpoint body and a cached copy written
// by Format: nhl.RosterPlayer reads and writes the position as
// "positionCode", as the endpoint sends it. An unknown code fails the whole
// roster, as it does in nhl.Client.
func (r SeasonRoster) Parse(data []byte) (*nhl.Roster, error) {
	var roster nhl.Roster
	if err := json.Unmarshal(data, &roster); err != nil {
		return nil, fmt.Errorf("parse season roster %s/%d: %w", r.TeamAbbrev, r.Season, err)
	}
	return &roster, nil
}

func (r SeasonRoster) Format(obj *nhl.Roster) ([]byte, error) {
	return json.Marshal(obj)
}
