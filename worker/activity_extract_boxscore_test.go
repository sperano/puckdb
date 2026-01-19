package worker

import (
	"context"
	"testing"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/redis"
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
	assert.Equal(t, teamID, player.NHLTeamID)
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
	assert.Equal(t, teamID, player.NHLTeamID)
	assert.True(t, player.HasBoxscoreData)
}

func TestExtractTeamPlayers(t *testing.T) {
	players := make(map[int64]PartialPlayer)
	teamID := int64(10) // EDM

	stats := &nhl.TeamPlayerStats{
		Forwards: []nhl.SkaterStats{
			{
				PlayerID:      nhl.PlayerID(8476453),
				Name:          nhl.LocalizedString{Default: "Connor McDavid"},
				Position:      nhl.Position("C"),
				SweaterNumber: 97,
			},
			{
				PlayerID:      nhl.PlayerID(8477934),
				Name:          nhl.LocalizedString{Default: "Leon Draisaitl"},
				Position:      nhl.Position("C"),
				SweaterNumber: 29,
			},
		},
		Defense: []nhl.SkaterStats{
			{
				PlayerID:      nhl.PlayerID(8478402),
				Name:          nhl.LocalizedString{Default: "Darnell Nurse"},
				Position:      nhl.Position("D"),
				SweaterNumber: 25,
			},
		},
		Goalies: []nhl.GoalieStats{
			{
				PlayerID:      nhl.PlayerID(8476883),
				Name:          nhl.LocalizedString{Default: "Stuart Skinner"},
				Position:      nhl.Position("G"),
				SweaterNumber: 74,
			},
		},
	}

	extractTeamPlayers(players, stats, teamID)

	// Should have 4 players: 2 forwards, 1 defense, 1 goalie
	assert.Len(t, players, 4)

	// Verify forwards
	mcdavid := players[8476453]
	assert.Equal(t, "Connor", mcdavid.FirstName)
	assert.Equal(t, "C", mcdavid.Position)

	drai := players[8477934]
	assert.Equal(t, "Leon", drai.FirstName)
	assert.Equal(t, "C", drai.Position)

	// Verify defense
	nurse := players[8478402]
	assert.Equal(t, "Darnell", nurse.FirstName)
	assert.Equal(t, "D", nurse.Position)

	// Verify goalie
	skinner := players[8476883]
	assert.Equal(t, "Stuart", skinner.FirstName)
	assert.Equal(t, "G", skinner.Position)

	// All should have boxscore flag set
	for _, p := range players {
		assert.True(t, p.HasBoxscoreData)
		assert.Equal(t, teamID, p.NHLTeamID)
	}
}

func TestExtractTeamPlayers_Empty(t *testing.T) {
	players := make(map[int64]PartialPlayer)
	stats := &nhl.TeamPlayerStats{
		Forwards: []nhl.SkaterStats{},
		Defense:  []nhl.SkaterStats{},
		Goalies:  []nhl.GoalieStats{},
	}

	extractTeamPlayers(players, stats, 10)

	assert.Len(t, players, 0)
}

func TestExtractTeamPlayers_OverwritesPreviousPlayer(t *testing.T) {
	players := make(map[int64]PartialPlayer)

	// Add same player twice with different data - second should win
	firstTeamStats := &nhl.TeamPlayerStats{
		Forwards: []nhl.SkaterStats{
			{
				PlayerID:      nhl.PlayerID(8476453),
				Name:          nhl.LocalizedString{Default: "Connor McDavid"},
				Position:      nhl.Position("C"),
				SweaterNumber: 97,
			},
		},
	}

	secondTeamStats := &nhl.TeamPlayerStats{
		Forwards: []nhl.SkaterStats{
			{
				PlayerID:      nhl.PlayerID(8476453),
				Name:          nhl.LocalizedString{Default: "Connor McDavid"},
				Position:      nhl.Position("C"),
				SweaterNumber: 97,
			},
		},
	}

	extractTeamPlayers(players, firstTeamStats, 10)
	extractTeamPlayers(players, secondTeamStats, 22) // Different team

	// Still only one player, but with second team ID
	assert.Len(t, players, 1)
	assert.Equal(t, int64(22), players[8476453].NHLTeamID)
}

// Tests for extractBoxscorePlayersForDayBatchImpl

func TestExtractBoxscorePlayersForDayBatchImpl_EmptyDays(t *testing.T) {
	ctx := context.Background()
	mockRedis := &redis.MockClient{}

	input := BatchInput{
		Days:       []time.Time{},
		Season:     2023,
		BatchIndex: 0,
	}

	result, err := extractBoxscorePlayersForDayBatchImpl(ctx, mockRedis, input)

	assert.NoError(t, err)
	assert.Equal(t, "", result.RedisKey)
	assert.Equal(t, 0, result.PlayerCount)
}

func TestExtractBoxscorePlayersForDayBatchImpl_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockRedis := &redis.MockClient{}

	// Use a day in the past that won't have any cache files
	day := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	input := BatchInput{
		Days:       []time.Time{day},
		Season:     2020,
		BatchIndex: 0,
	}

	_, err := extractBoxscorePlayersForDayBatchImpl(ctx, mockRedis, input)

	assert.ErrorIs(t, err, context.Canceled)
}
