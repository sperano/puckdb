package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSyncSteps_NoArgs(t *testing.T) {
	steps, err := ParseSyncSteps(nil)
	require.NoError(t, err)
	assert.Len(t, steps, len(AllSyncSteps))
	for _, s := range AllSyncSteps {
		assert.True(t, steps[s], "expected %q to be enabled", s)
	}
}

func TestParseSyncSteps_SingleStep(t *testing.T) {
	steps, err := ParseSyncSteps([]string{StepImportSeasons})
	require.NoError(t, err)
	assert.True(t, steps[StepImportSeasons])
	assert.False(t, steps[StepInit])
	assert.False(t, steps[StepFetchSeasons])
}

func TestParseSyncSteps_SeasonsGroup(t *testing.T) {
	steps, err := ParseSyncSteps([]string{"seasons"})
	require.NoError(t, err)
	assert.True(t, steps[StepFetchSeasons])
	assert.True(t, steps[StepImportSeasons])
	assert.Len(t, steps, 2)
}

func TestParseSyncSteps_PlayersGroup(t *testing.T) {
	steps, err := ParseSyncSteps([]string{"players"})
	require.NoError(t, err)
	assert.True(t, steps[StepYahooPlayers])
	assert.True(t, steps[StepExtractBoxscorePlayers])
	assert.True(t, steps[StepFetchPlayerLandings])
	assert.True(t, steps[StepFetchPlayerLogs])
	assert.True(t, steps[StepProcessPlayers])
	assert.True(t, steps[StepImportPlayerLogs])
	assert.False(t, steps[StepInit])
	assert.False(t, steps[StepFetchSeasons])
	assert.False(t, steps[StepImportSeasons])
}

func TestParseSyncSteps_MixedGroupAndStep(t *testing.T) {
	steps, err := ParseSyncSteps([]string{"seasons", StepInit})
	require.NoError(t, err)
	assert.True(t, steps[StepInit])
	assert.True(t, steps[StepFetchSeasons])
	assert.True(t, steps[StepImportSeasons])
	assert.Len(t, steps, 3)
}

func TestParseSyncSteps_UnknownStep(t *testing.T) {
	_, err := ParseSyncSteps([]string{"bogus"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus")
	assert.Contains(t, err.Error(), "valid steps")
}

func TestParseSyncSteps_MultipleUnknown(t *testing.T) {
	_, err := ParseSyncSteps([]string{"foo", "bar"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "foo")
	assert.Contains(t, err.Error(), "bar")
}

func TestParseSyncSteps_EdgeGroup(t *testing.T) {
	steps, err := ParseSyncSteps([]string{"edge"})
	require.NoError(t, err)
	assert.True(t, steps[StepFetchEdgeStats])
	assert.True(t, steps[StepImportEdgeStats])
	assert.Len(t, steps, 2)
	assert.False(t, steps[StepInit])
	assert.False(t, steps[StepFetchSeasons])
}

func TestParseSyncSteps_Deduplication(t *testing.T) {
	// "seasons" includes fetch-seasons; adding it explicitly shouldn't break
	steps, err := ParseSyncSteps([]string{"seasons", StepFetchSeasons})
	require.NoError(t, err)
	assert.Len(t, steps, 2)
}
