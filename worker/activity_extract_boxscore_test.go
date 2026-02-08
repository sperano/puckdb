package worker

import (
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/stretchr/testify/assert"
)

func TestParseLocalizedName(t *testing.T) {
	tests := []struct {
		name          string
		input         nhl.LocalizedString
		expectedFirst string
		expectedLast  string
	}{
		{
			name:          "normal two-part name",
			input:         nhl.LocalizedString{Default: "Connor McDavid"},
			expectedFirst: "Connor",
			expectedLast:  "McDavid",
		},
		{
			name:          "hyphenated last name",
			input:         nhl.LocalizedString{Default: "Pierre-Luc Dubois"},
			expectedFirst: "Pierre-Luc",
			expectedLast:  "Dubois",
		},
		{
			name:          "three-part name",
			input:         nhl.LocalizedString{Default: "Jean-Gabriel Pageau"},
			expectedFirst: "Jean-Gabriel",
			expectedLast:  "Pageau",
		},
		{
			name:          "name with suffix",
			input:         nhl.LocalizedString{Default: "Martin St. Louis"},
			expectedFirst: "Martin",
			expectedLast:  "St. Louis",
		},
		{
			name:          "single name only",
			input:         nhl.LocalizedString{Default: "Pele"},
			expectedFirst: "Pele",
			expectedLast:  "",
		},
		{
			name:          "empty name",
			input:         nhl.LocalizedString{Default: ""},
			expectedFirst: "",
			expectedLast:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first, last := parseLocalizedName(tt.input)
			assert.Equal(t, tt.expectedFirst, first)
			assert.Equal(t, tt.expectedLast, last)
		})
	}
}

func TestAddSkaterPlayer(t *testing.T) {
	players := make(map[int64]PartialPlayer)
	teamID := int64(10) // EDM

	skater := nhl.SkaterStats{
		PlayerID:      nhl.PlayerID(8476453),
		Name:          nhl.LocalizedString{Default: "Connor McDavid"},
		Position:      nhl.Position("C"),
		SweaterNumber: 97,
	}

	addSkaterPlayer(players, skater, teamID)

	assert.Len(t, players, 1)

	player := players[8476453]
	assert.Equal(t, int64(8476453), player.ID)
	assert.Equal(t, "Connor", player.FirstName)
	assert.Equal(t, "McDavid", player.LastName)
	assert.Equal(t, "C", player.Position)
	assert.Equal(t, 97, player.SweaterNumber)
	assert.Equal(t, teamID, player.TeamID)
	assert.True(t, player.HasBoxscoreData)
}

func TestAddGoaliePlayer(t *testing.T) {
	players := make(map[int64]PartialPlayer)
	teamID := int64(10) // EDM

	goalie := nhl.GoalieStats{
		PlayerID:      nhl.PlayerID(8476883),
		Name:          nhl.LocalizedString{Default: "Stuart Skinner"},
		Position:      nhl.Position("G"),
		SweaterNumber: 74,
	}

	addGoaliePlayer(players, goalie, teamID)

	assert.Len(t, players, 1)

	player := players[8476883]
	assert.Equal(t, int64(8476883), player.ID)
	assert.Equal(t, "Stuart", player.FirstName)
	assert.Equal(t, "Skinner", player.LastName)
	assert.Equal(t, "G", player.Position)
	assert.Equal(t, 74, player.SweaterNumber)
	assert.Equal(t, teamID, player.TeamID)
	assert.True(t, player.HasBoxscoreData)
}
