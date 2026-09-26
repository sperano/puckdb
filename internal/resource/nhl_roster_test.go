package resource_test

import (
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rosterEndpointBody is trimmed from GET /v1/roster/TOR/20242025: the endpoint
// names the position "positionCode" and abbreviates wings to L and R.
const rosterEndpointBody = `{
	"forwards": [
		{"id": 8477503, "firstName": {"default": "Max"}, "lastName": {"default": "Domi"}, "positionCode": "C", "shootsCatches": "L"},
		{"id": 8479318, "firstName": {"default": "Auston"}, "lastName": {"default": "Matthews"}, "positionCode": "C", "shootsCatches": "L"},
		{"id": 8478483, "firstName": {"default": "Mitch"}, "lastName": {"default": "Marner"}, "positionCode": "R", "shootsCatches": "R"},
		{"id": 8481720, "firstName": {"default": "Matthew"}, "lastName": {"default": "Knies"}, "positionCode": "L", "shootsCatches": "L"}
	],
	"defensemen": [
		{"id": 8476853, "firstName": {"default": "Morgan"}, "lastName": {"default": "Rielly"}, "positionCode": "D", "shootsCatches": "L"}
	],
	"goalies": [
		{"id": 8479361, "firstName": {"default": "Joseph"}, "lastName": {"default": "Woll"}, "positionCode": "G", "shootsCatches": "L"}
	]
}`

func rosterPositions(roster *nhl.Roster) map[nhl.PlayerID]nhl.Position {
	positions := make(map[nhl.PlayerID]nhl.Position)
	for _, p := range roster.AllPlayers() {
		positions[p.ID] = p.Position
	}
	return positions
}

func TestSeasonRoster_ParseReadsPositionCode(t *testing.T) {
	t.Parallel()
	res := resource.SeasonRoster{Season: 2024, TeamAbbrev: "TOR"}

	roster, err := res.Parse([]byte(rosterEndpointBody))
	require.NoError(t, err)

	assert.Equal(t, map[nhl.PlayerID]nhl.Position{
		8477503: nhl.PositionCenter,
		8479318: nhl.PositionCenter,
		8478483: nhl.PositionRightWing,
		8481720: nhl.PositionLeftWing,
		8476853: nhl.PositionDefense,
		8479361: nhl.PositionGoalie,
	}, rosterPositions(roster))
}

// TestSeasonRoster_FormatParseRoundTrip checks that a roster cached by Format
// keeps its positions.
func TestSeasonRoster_FormatParseRoundTrip(t *testing.T) {
	t.Parallel()
	res := resource.SeasonRoster{Season: 2024, TeamAbbrev: "TOR"}
	fetched, err := res.Parse([]byte(rosterEndpointBody))
	require.NoError(t, err)

	cached, err := res.Format(fetched)
	require.NoError(t, err)
	reread, err := res.Parse(cached)
	require.NoError(t, err)

	assert.Equal(t, fetched, reread)
}

func TestSeasonRoster_ParseMissingPositionStaysEmpty(t *testing.T) {
	t.Parallel()
	res := resource.SeasonRoster{Season: 2024, TeamAbbrev: "TOR"}

	roster, err := res.Parse([]byte(`{"forwards": [{"id": 1}], "defensemen": [], "goalies": []}`))
	require.NoError(t, err)

	require.Len(t, roster.Forwards, 1)
	assert.Empty(t, roster.Forwards[0].Position)
}

// TestSeasonRoster_ParseUnknownPositionCodeFails checks that an unknown code
// fails the whole roster, as nhl.RosterPlayer decodes positions strictly.
func TestSeasonRoster_ParseUnknownPositionCodeFails(t *testing.T) {
	t.Parallel()
	res := resource.SeasonRoster{Season: 2024, TeamAbbrev: "TOR"}

	_, err := res.Parse([]byte(`{"forwards": [{"id": 1, "positionCode": "X"}, {"id": 2, "positionCode": "C"}]}`))

	var enumErr *nhl.UnknownEnumValueError
	require.ErrorAs(t, err, &enumErr)
	assert.Equal(t, "X", enumErr.Value)
}
