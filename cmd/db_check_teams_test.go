package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sperano/puckdb/internal/sqlcdb"
)

func TestReportOrphanGameTeamsNone(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := reportOrphanGameTeams(&buf, nil); err != nil {
		t.Fatalf("reportOrphanGameTeams(nil) err = %v, want nil", err)
	}
	if !strings.Contains(buf.String(), "have season_teams rows") {
		t.Errorf("output = %q, want the all-clear message", buf.String())
	}
}

func TestReportOrphanGameTeamsFound(t *testing.T) {
	t.Parallel()

	rows := []sqlcdb.ListGameTeamsWithoutSeasonTeamRow{
		{Season: 20252026, TeamID: 68, GameType: sqlcdb.GameTypeRegularSeason, Games: 82},
		{Season: 20252026, TeamID: 68, GameType: sqlcdb.GameTypePlayoffs, Games: 6},
	}
	var buf bytes.Buffer
	err := reportOrphanGameTeams(&buf, rows)
	if err == nil {
		t.Fatal("reportOrphanGameTeams err = nil, want an error when orphans exist")
	}
	if !strings.Contains(err.Error(), "88 game-team references across 2") {
		t.Errorf("err = %q, want totals of 88 references in 2 groups", err)
	}
	out := buf.String()
	for _, want := range []string{"SEASON", "20252026", "regular_season", "playoffs", "82"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
