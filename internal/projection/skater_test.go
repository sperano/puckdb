package projection

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// skaterFaceoffSeason builds a history row with enough games to derive a TOI
// total and an explicit faceoff win/loss count, leaving the other counting
// stats at zero.
func skaterFaceoffSeason(playerID int64, season int, position string, games, faceoffsWon, faceoffsLost int) SkaterSeason {
	return SkaterSeason{
		PlayerID: playerID, Season: season, Position: position,
		GamesPlayed: games, TOISeconds: games * typicalSkaterTOIPerGame,
		FaceoffsWon: faceoffsWon, FaceoffsLost: faceoffsLost,
	}
}

func TestFaceoffPositionGroupSeparatesCentresFromEveryoneElse(t *testing.T) {
	t.Parallel()

	require.Equal(t, faceoffGroupCentre, faceoffPositionGroup("C"))
	require.Equal(t, faceoffGroupNonCentre, faceoffPositionGroup("LW"))
	require.Equal(t, faceoffGroupNonCentre, faceoffPositionGroup("RW"))
	require.Equal(t, faceoffGroupNonCentre, faceoffPositionGroup("D"))

	// positionGroup, used by every other skater stat, still only splits by
	// defense vs. forward: this change must not touch that grouping.
	require.Equal(t, positionGroupForward, positionGroup("C"))
	require.Equal(t, positionGroupForward, positionGroup("LW"))
	require.Equal(t, positionGroupDefense, positionGroup("D"))
}

// TestFaceoffProjectionRegressesTowardPositionSpecificPeers is the core
// regression-shape test: a small-sample centre must regress toward the
// centre peer rate (dominated by centres taking most draws), not toward a
// forward/defense-wide average that a near-zero winger rate would drag down,
// and vice versa for a small-sample winger.
func TestFaceoffProjectionRegressesTowardPositionSpecificPeers(t *testing.T) {
	t.Parallel()

	const (
		centreStarID    = 60
		centreRookieID  = 61
		wingStarID      = 62
		wingRookieID    = 63
		fullSeasonGames = 82
		smallSample     = 3
	)
	input := Input{
		TargetSeason: 20262027,
		AsOf:         time.Now(),
		Skaters: []SkaterSeason{
			skaterFaceoffSeason(centreStarID, 20252026, "C", fullSeasonGames, 900, 800),
			skaterFaceoffSeason(centreRookieID, 20252026, "C", smallSample, 5, 3),
			skaterFaceoffSeason(wingStarID, 20252026, "LW", fullSeasonGames, 20, 15),
			skaterFaceoffSeason(wingRookieID, 20252026, "RW", smallSample, 0, 0),
		},
	}

	snapshot, err := Generate(DefaultConfig(), input)
	require.NoError(t, err)

	centreRookie := findProjection(t, snapshot, playerKey(centreRookieID))
	wingRookie := findProjection(t, snapshot, playerKey(wingRookieID))

	require.Greater(t, centreRookie.Value(StatFaceoffsWon).Mean, wingRookie.Value(StatFaceoffsWon).Mean,
		"a small-sample centre should regress toward the centre peer rate, not the near-zero winger rate")
	require.Greater(t, centreRookie.Value(StatFaceoffsLost).Mean, wingRookie.Value(StatFaceoffsLost).Mean)
	require.GreaterOrEqual(t, wingRookie.Value(StatFaceoffsWon).Mean, 0.0,
		"a winger who has never taken a draw still regresses toward a small but non-negative winger peer rate")
}

// TestFaceoffProjectionsStayNonNegative checks both faceoff stats are
// projected the same, direction-neutral way regardless of which one a
// league's scoring calls "higher is better": both are plain non-negative
// counting stats, like hits or blocked shots, not a rate with a sign.
func TestFaceoffProjectionsStayNonNegative(t *testing.T) {
	t.Parallel()

	row := skaterFaceoffSeason(70, 20252026, "C", 1, 0, 0)
	snapshot, err := Generate(DefaultConfig(), Input{
		TargetSeason: 20262027, AsOf: time.Now(), Skaters: []SkaterSeason{row},
	})
	require.NoError(t, err)
	require.Len(t, snapshot.Players, 1)
	projection := snapshot.Players[0]
	require.GreaterOrEqual(t, projection.Value(StatFaceoffsWon).Low, 0.0)
	require.GreaterOrEqual(t, projection.Value(StatFaceoffsLost).Low, 0.0)
}
