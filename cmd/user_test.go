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
	out := buf.String()
	for _, want := range []string{"LEAGUE_ID", "29249", "8", "Les Peter Stastny", "email", "2026-01-02"} {
		assert.Contains(t, out, want)
	}
}

func TestPrintInstanceTeamLinks(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, printInstanceTeamLinks(&buf, nil))
	assert.Contains(t, buf.String(), "No Yahoo team links")

	buf.Reset()
	links := []yahoolink.InstanceLink{
		{Username: "alice", Link: yahoolink.Link{LeagueID: 29249, TeamID: 8, TeamName: "Les Peter Stastny", Source: yahoolink.SourceManual, LinkedAt: time.Now()}},
	}
	require.NoError(t, printInstanceTeamLinks(&buf, links))
	out := buf.String()
	for _, want := range []string{"USERNAME", "alice", "29249", "8", "Les Peter Stastny", "manual"} {
		assert.True(t, strings.Contains(out, want), "missing %q in %q", want, out)
	}
}
