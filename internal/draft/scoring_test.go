package draft

import (
	"errors"
	"testing"

	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rotoScoringStatCount is the number of RotoLeague categories that actually
// score: the 11 stat categories minus the two display-only ones (SA, SV).
const rotoScoringStatCount = 9

func TestScoringFor_RotoLeague(t *testing.T) {
	t.Parallel()
	snapshot := Snapshot{Rules: FromLeague(parseRotoLeague(t)), Source: SourceYahooAPI}
	scoring, err := ScoringFor(snapshot)
	require.NoError(t, err)
	assert.Equal(t, FormatCategories, scoring.Format)
	assert.Equal(t, ObjectiveSeasonLong, scoring.Objective)
	assert.Len(t, scoring.Stats, rotoScoringStatCount)
	assert.False(t, scoring.Provisional)
}

func TestScoringFor_StandInSourceIsProvisional(t *testing.T) {
	t.Parallel()
	snapshot := Snapshot{Rules: FromLeague(parseRotoLeague(t)), Source: SourceTemporaryStandIn}
	scoring, err := ScoringFor(snapshot)
	require.NoError(t, err)
	assert.True(t, scoring.Provisional)
}

func TestScoringFor_PointsLeagueBonusUnsupported(t *testing.T) {
	t.Parallel()
	snapshot := Snapshot{Rules: FromLeague(parsePointsLeague(t, yahoofixtures.PointsLeague)), Source: SourceYahooAPI}
	_, err := ScoringFor(snapshot)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingScoringInputs)
	assert.Contains(t, err.Error(), "bonus")
}

func TestScoringFor_PointsLeagueMissingWeight(t *testing.T) {
	t.Parallel()
	snapshot := Snapshot{Rules: FromLeague(parsePointsLeague(t, yahoofixtures.PointsLeagueMissingWeight)), Source: SourceYahooAPI}
	_, err := ScoringFor(snapshot)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingScoringInputs)
	assert.Contains(t, err.Error(), "stat 32 (BLK) has no points weight")
}

func TestScoringFor_ValidPointsLeague(t *testing.T) {
	t.Parallel()
	league := parsePointsLeague(t, yahoofixtures.PointsLeague)
	clearBonus(&league, statGoals)
	snapshot := Snapshot{Rules: FromLeague(league), Source: SourceYahooAPI}

	scoring, err := ScoringFor(snapshot)
	require.NoError(t, err)
	assert.Equal(t, FormatPoints, scoring.Format)
	assert.Equal(t, ObjectiveHeadToHead, scoring.Objective)
	for _, stat := range scoring.Stats {
		assert.NotZero(t, stat.Weight, "stat %d (%s) must carry its points weight", stat.StatID, stat.Abbr)
	}
}

func TestScoringFor_UnknownScoringType(t *testing.T) {
	t.Parallel()
	rules := Rules{ScoringType: "bogus", LeagueKey: "465.l.1", Categories: []StatCategory{
		{StatID: statGoals, Enabled: true, Direction: HigherIsBetter},
	}}
	_, err := ScoringFor(Snapshot{Rules: rules, Source: SourceYahooAPI})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingScoringInputs)
	assert.Contains(t, err.Error(), `unsupported scoring type "bogus"`)
}

func TestScoringFor_NoScoringCategories(t *testing.T) {
	t.Parallel()
	rules := Rules{ScoringType: scoringTypeRoto, LeagueKey: "465.l.1"}
	_, err := ScoringFor(Snapshot{Rules: rules, Source: SourceYahooAPI})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingScoringInputs)
	assert.Contains(t, err.Error(), "no enabled, non-display-only stat categories")
}

func TestScoringFor_UnknownDirectionInRotoLeague(t *testing.T) {
	t.Parallel()
	rules := Rules{ScoringType: scoringTypeRoto, LeagueKey: "465.l.1", Categories: []StatCategory{
		{StatID: statGoals, Abbr: "G", Enabled: true, Direction: DirectionUnknown},
	}}
	_, err := ScoringFor(Snapshot{Rules: rules, Source: SourceYahooAPI})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrMissingScoringInputs))
	assert.Contains(t, err.Error(), "stat 1 (G) has no sort direction")
}

// clearBonus removes any points bonus on a stat modifier so the rest of the
// league's weights stay usable in a "valid points league" test.
func clearBonus(league *store.League, statID int) {
	for i := range league.Settings.StatModifiers.Stats {
		if league.Settings.StatModifiers.Stats[i].StatID == statID {
			league.Settings.StatModifiers.Stats[i].Bonuses = nil
		}
	}
}
