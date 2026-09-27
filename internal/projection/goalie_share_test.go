package projection

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	shareClub        = 8
	shareOldClub     = 9
	shareNewClub     = 10
	shareLonerClub   = 11
	shareDobes       = 8_201
	shareMontembeau  = 8_202
	shareFowler      = 8_203
	shareTraded      = 8_204
	shareNoRecent    = 8_205
	sharePlayoffs    = 19
	shareLateRegular = 23
)

// montrealStarts is a club's last 60 started games, newest first: 19
// playoff starts all by Dobes, then the last 23 regular-season starts (15
// Dobes, 7 Fowler, 1 Montembeault) and 18 earlier ones Montembeault mostly
// took.
func montrealStarts() []GoalieStart {
	late := time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	var starts []GoalieStart
	add := func(playerID int64, playoff bool) {
		rank := len(starts) + 1
		start := GoalieStart{
			TeamID: shareClub, PlayerID: playerID, Season: benchLastSeason,
			GameDate: late.AddDate(0, 0, -2*rank), Playoff: playoff, RecencyRank: rank,
		}
		if !playoff {
			start.RegularSeasonRank = rank - sharePlayoffs
		}
		starts = append(starts, start)
	}
	for range sharePlayoffs {
		add(shareDobes, true)
	}
	for index := range shareLateRegular {
		switch {
		case index < 15:
			add(shareDobes, false)
		case index < 22:
			add(shareFowler, false)
		default:
			add(shareMontembeau, false)
		}
	}
	for index := range DefaultGoalieShareWindowGames - sharePlayoffs - shareLateRegular {
		if index%3 == 0 {
			add(shareDobes, false)
		} else {
			add(shareMontembeau, false)
		}
	}
	return starts
}

func montrealInput() Input {
	input := benchInput(
		pinGoalie(shareDobes, time.Time{}, benchLastSeason, 77, 43, 42),
		pinGoalie(shareMontembeau, time.Time{}, benchLastSeason, 53, 25, 23),
		pinGoalie(shareMontembeau, time.Time{}, 20242025, 70, 60, 58),
		pinGoalie(shareFowler, time.Time{}, benchLastSeason, 20, 17, 17),
		pinGoalie(shareTraded, time.Time{}, benchLastSeason, 60, 50, 48),
		pinGoalie(shareNoRecent, time.Time{}, benchLastSeason, 40, 30, 28),
	)
	input.TargetTeams = []PlayerTeam{
		{PlayerID: shareDobes, TeamID: shareClub}, {PlayerID: shareMontembeau, TeamID: shareClub},
		{PlayerID: shareFowler, TeamID: shareClub}, {PlayerID: shareTraded, TeamID: shareNewClub},
		{PlayerID: shareNoRecent, TeamID: shareLonerClub},
	}
	input.GoalieStarts = append(montrealStarts(), GoalieStart{
		TeamID: shareOldClub, PlayerID: shareTraded, Season: benchLastSeason,
		GameDate: time.Date(2026, time.April, 10, 0, 0, 0, 0, time.UTC), RecencyRank: 1, RegularSeasonRank: 1,
	})
	return input
}

func goalieStarts(t *testing.T, snapshot Snapshot, playerID int64) float64 {
	t.Helper()
	return findProjection(t, snapshot, playerKey(playerID)).Value(StatGamesStarted).Mean
}

// TestGoalieStartShareFollowsRecentStarter covers the Montreal 2025-26
// case: season averages split the net between Dobes and Montembeault, but
// Dobes took the late regular season and every playoff start.
func TestGoalieStartShareFollowsRecentStarter(t *testing.T) {
	t.Parallel()

	input := montrealInput()
	v6 := mustGenerate(t, benchConfig(GoalieAppearancesModelVersion), input)
	v7 := mustGenerate(t, benchConfig(ModelVersion), input)

	require.Greater(t, goalieStarts(t, v7, shareDobes), goalieStarts(t, v6, shareDobes)+5)
	require.Less(t, goalieStarts(t, v7, shareMontembeau), goalieStarts(t, v6, shareMontembeau)-5)
	require.Greater(t, goalieStarts(t, v7, shareDobes), goalieStarts(t, v7, shareMontembeau)+10)
	clubStarts := 0.0
	for _, id := range []int64{shareDobes, shareMontembeau, shareFowler} {
		clubStarts += goalieStarts(t, v7, id)
		projection := findProjection(t, v7, playerKey(id))
		require.GreaterOrEqual(t, projection.Value(StatGamesPlayed).Mean, projection.Value(StatGamesStarted).Mean)
	}
	require.LessOrEqual(t, clubStarts, DefaultMaxGames+1e-9)

	// A goalie whose recent starts came for another club, and one without
	// recent starts, keep the v6 starts.
	for _, id := range []int64{shareTraded, shareNoRecent} {
		require.InDelta(t, goalieStarts(t, v6, id), goalieStarts(t, v7, id), 1e-9)
	}

	// A zero playoff weight reads only the regular season, where Dobes's
	// share is smaller.
	noPlayoffs := benchConfig(ModelVersion)
	noPlayoffs.GoaliePlayoffWeight = 0
	regular := mustGenerate(t, noPlayoffs, input)
	require.Less(t, goalieStarts(t, regular, shareDobes), goalieStarts(t, v7, shareDobes))

	// v6 never reads the recent starts: identical output and source hash.
	without := input
	without.GoalieStarts = nil
	v6Without := mustGenerate(t, benchConfig(GoalieAppearancesModelVersion), without)
	require.Equal(t, hashValue(v6Without.Players), hashValue(v6.Players))
	require.Equal(t, v6Without.SourceDataHash, v6.SourceDataHash)
	require.NotEqual(t, sourceDataHash(benchConfig(ModelVersion), without), v7.SourceDataHash)
}
