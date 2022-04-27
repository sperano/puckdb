package xmlmodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlayerIDToInt(t *testing.T) {
	playerID := PlayerID("nhl.p.8590")
	id, err := playerID.Id()
	assert.Nil(t, err)
	assert.Equal(t, uint(8590), id)
}

func TestInvalidPlayerID(t *testing.T) {
	playerID := PlayerID("crap")
	id, err := playerID.Id()
	assert.Equal(t, "invalid PlayerID: crap", err.Error())
	assert.Equal(t, uint(0), id)
}

func TestNHLTeamIDToInt(t *testing.T) {
	teamID := NHLTeamID("nhl.t.16")
	id, err := teamID.Id()
	assert.Nil(t, err)
	assert.Equal(t, uint(16), id)
}

func TestInvalidNHLTeamID(t *testing.T) {
	teamID := NHLTeamID("crap")
	id, err := teamID.Id()
	assert.Equal(t, "invalid NHLTeamID: crap", err.Error())
	assert.Equal(t, uint(0), id)
}

func TestPositionIDToInt(t *testing.T) {
	positionID := PositionID("nhl.pos.2")
	id, err := positionID.Id()
	assert.Nil(t, err)
	assert.Equal(t, uint(2), id)
}

func TestInvalidPositionID(t *testing.T) {
	positionID := PositionID("crap")
	id, err := positionID.Id()
	assert.Equal(t, "invalid PositionID: crap", err.Error())
	assert.Equal(t, uint(0), id)
}

func TestGameIDToInt(t *testing.T) {
	gameID := GameID("nhl.g.2021101220")
	id, err := gameID.Id()
	assert.Nil(t, err)
	assert.Equal(t, uint(2021101220), id)
}

func TestInvalidGameID(t *testing.T) {
	gameID := GameID("crap")
	id, err := gameID.Id()
	assert.Equal(t, "invalid GameID: crap", err.Error())
	assert.Equal(t, uint(0), id)
}
