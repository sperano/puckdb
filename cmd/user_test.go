package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/yahoolink"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLeagueTeamArgs(t *testing.T) {
	league, team, err := parseLeagueTeamArgs("29249", "8")
	require.NoError(t, err)
	assert.Equal(t, int32(29249), league)
	assert.Equal(t, int32(8), team)

	_, _, err = parseLeagueTeamArgs("not-a-number", "8")
	require.Error(t, err)
}

func TestPrintUserTeamLinks(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, printUserTeamLinks(&buf, nil, "alice"))
	assert.Contains(t, buf.String(), "alice has no linked Yahoo teams")

	buf.Reset()
	links := []yahoolink.Link{
		{LeagueID: 29249, TeamID: 8, TeamName: "Les Peter Stastny", Source: yahoolink.SourceEmail, LinkedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
	}
	require.NoError(t, printUserTeamLinks(&buf, links, "alice"))
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, []string{"LEAGUE_ID", "TEAM_ID", "TEAM_NAME", "SOURCE", "LINKED_AT"}, strings.Fields(lines[0]))
	assert.Regexp(t, `^29249\s+8\s+Les Peter Stastny\s+email\s+2026-01-02$`, lines[1])
}

func TestPrintInstanceTeamLinks(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, printInstanceTeamLinks(&buf, nil))
	assert.Contains(t, buf.String(), "No Yahoo team links")

	buf.Reset()
	links := []yahoolink.InstanceLink{
		{Username: "alice", Link: yahoolink.Link{LeagueID: 29249, TeamID: 8, TeamName: "Les Peter Stastny", Source: yahoolink.SourceManual, LinkedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}},
	}
	require.NoError(t, printInstanceTeamLinks(&buf, links))
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, []string{"USERNAME", "LEAGUE_ID", "TEAM_ID", "TEAM_NAME", "SOURCE", "LINKED_AT"}, strings.Fields(lines[0]))
	assert.Regexp(t, `^alice\s+29249\s+8\s+Les Peter Stastny\s+manual\s+2026-01-02$`, lines[1])
}
