package newsadjust

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSeason_Position(t *testing.T) {
	season := testSeason()
	assert.Zero(t, season.position(testSeasonStart.Add(-time.Hour)))
	assert.InDelta(t, testSeasonGames/2, season.position(dateAtGame(testSeasonGames/2)), 1e-6)
	assert.Equal(t, float64(testSeasonGames), season.position(testSeasonEnd.Add(time.Hour)))
	assert.True(t, season.priorSeason(testSeasonStart.AddDate(0, 0, -91), defaultOffseasonDays))
	assert.False(t, season.priorSeason(testSeasonStart.AddDate(0, 0, -89), defaultOffseasonDays))
	assert.Error(t, Season{Start: testSeasonEnd, End: testSeasonStart, Games: testSeasonGames}.validate())
}

func TestUnionLength(t *testing.T) {
	assert.Zero(t, unionLength(nil))
	assert.Equal(t, 15.0, unionLength([]interval{{0, 10}, {0, 15}}), "overlap counts once")
	assert.Equal(t, 20.0, unionLength([]interval{{40, 45}, {0, 10}, {5, 15}}))
	assert.Equal(t, 10.0, unionLength([]interval{{0, 5}, {5, 10}, {3, 3}}))
}
