package draftrank

import (
	"testing"

	"github.com/sperano/puckdb/internal/projection"
	"github.com/stretchr/testify/require"
)

func TestTeamEnvironmentNote(t *testing.T) {
	t.Parallel()

	moved := projection.PlayerProjection{PlayerKey: "moved", TeamEnvironment: &projection.TeamEnvironment{FromTeamID: 3, ToTeamID: 26}}
	stayed := projection.PlayerProjection{PlayerKey: "stayed", TeamEnvironment: &projection.TeamEnvironment{FromTeamID: 26, ToTeamID: 26}}
	baseline := projection.Snapshot{TargetSeason: 20262027, Config: projection.DefaultConfig(), Players: []projection.PlayerProjection{moved, stayed}}

	require.Equal(t, "team environment: 1 skater(s) changed clubs for 2026-27; their goals, assists, shots and power-play "+
		"points scale with the new club's scoring, shot and power-play-opportunity rates, regressed toward league "+
		"average by 328 games and capped at ±5%", teamEnvironmentNote(baseline))

	baseline.Players = []projection.PlayerProjection{stayed}
	require.Contains(t, teamEnvironmentNote(baseline), "no skater has a 2026-27 club other than their most recent one")

	baseline.Config.TeamEnvironmentMaxChange = 0
	require.Empty(t, teamEnvironmentNote(baseline))
}
