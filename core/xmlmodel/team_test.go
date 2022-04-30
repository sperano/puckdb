package xmlmodel

import (
	"encoding/xml"
	"io/ioutil"
	"testing"
	"time"

	"github.com/ericsperano/yfh/core/model"
	"github.com/stretchr/testify/assert"
)

func TestToTeamModel(t *testing.T) {
	t.Parallel()
	data, err := ioutil.ReadFile("../../test-data/roster-7.xml")
	if err != nil {
		t.Fatal(err)
	}
	var fantasy FantasyContent
	if err := xml.Unmarshal(data, &fantasy); err != nil {
		t.Fatal(err)
	}
	team := fantasy.Team.ToTeamModel()
	assert.Equal(t, uint(7), team.ID)
	assert.Equal(t, "411.l.1005.t.7", team.Key)
	assert.Equal(t, "Les Peter Stastny", team.Name)
	assert.True(t, team.IsOwnedByCurrentLogin)
	assert.Equal(t, "https://hockey.fantasysports.yahoo.com/hockey/22030/7", team.URL)
	assert.Equal(t, 5, team.DraftPosition)
	assert.Equal(t, false, team.HasDraftGrade)
	assert.Equal(t, uint(7), team.Manager.ManagerID)
	assert.Equal(t, "Éric", team.Manager.Nickname)
	assert.Equal(t, "BAXS7V667SSTAO2YFA4K5SO2WY", team.Manager.GUID)
	assert.Equal(t, "owner@example.com", team.Manager.EMail)
	assert.Equal(t, "https://s.yimg.com/ag/images/9cdf6123-35bb-42bd-ac8d-b5445b89728b_64sq.jpg", team.Manager.ImageURL)
}

func assertRosterPlayer(t *testing.T, p *model.RosterPlayer, playerID uint, nhlTeamID uint, date time.Time, elig string, pos string) {
	assert.Equal(t, uint(7), p.TeamID)
	assert.Equal(t, playerID, p.PlayerID)
	assert.Equal(t, nhlTeamID, p.NHLTeamID)
	assert.Equal(t, date, p.Date)
	assert.Equal(t, elig, p.EligiblePositions)
	assert.Equal(t, pos, p.SelectedPosition)
}

func TestToRosterPlayersModel(t *testing.T) {
	t.Parallel()
	data, err := ioutil.ReadFile("../../test-data/roster-7.xml")
	if err != nil {
		t.Fatal(err)
	}
	var fantasy FantasyContent
	if err := xml.Unmarshal(data, &fantasy); err != nil {
		t.Fatal(err)
	}
	players, err := fantasy.Team.ToRosterPlayersModel()
	if err != nil {
		t.Fatal(err)
	}
	date := time.Date(2022, time.April, 10, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, 17, len(players))
	assertRosterPlayer(t, players[0], 6777, 7, date, "C Util", "C")
	assertRosterPlayer(t, players[1], 3737, 16, date, "C Util", "C")
	assertRosterPlayer(t, players[2], 5470, 3, date, "LW RW Util", "LW")
	assertRosterPlayer(t, players[3], 6057, 16, date, "LW RW Util", "LW")
	assertRosterPlayer(t, players[4], 4684, 6, date, "LW RW Util", "RW")
	assertRosterPlayer(t, players[5], 6752, 17, date, "C RW Util", "RW")
	assertRosterPlayer(t, players[6], 4503, 23, date, "D Util", "D")
	assertRosterPlayer(t, players[7], 4064, 16, date, "D Util", "D")
	assertRosterPlayer(t, players[8], 5986, 6, date, "D Util", "D")
	assertRosterPlayer(t, players[9], 5689, 13, date, "D Util", "BN")
	assertRosterPlayer(t, players[10], 8293, 10, date, "LW RW Util", "BN")
	assertRosterPlayer(t, players[11], 5161, 7, date, "G", "G")
	assertRosterPlayer(t, players[12], 6408, 23, date, "G", "G")
	assertRosterPlayer(t, players[13], 4740, 6, date, "G", "BN")
	assertRosterPlayer(t, players[14], 4003, 12, date, "G", "BN")
	assertRosterPlayer(t, players[15], 7429, 16, date, "G", "BN")
	assertRosterPlayer(t, players[16], 6818, 18, date, "G", "BN")
}
