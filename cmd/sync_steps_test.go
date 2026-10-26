package cmd

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSeasonsInput_ReimportDays(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	assert.Nil(t, buildSeasonsInput().ReimportDays, "omitted unless the flag is set")

	viper.Set(config.FlagReimportDays, true)
	input := buildSeasonsInput()
	if assert.NotNil(t, input.ReimportDays) {
		assert.True(t, *input.ReimportDays)
	}
}

func allSyncStepsEnabled() map[string]bool {
	enabled := make(map[string]bool, len(config.AllSyncSteps))
	for _, step := range config.AllSyncSteps {
		enabled[step] = true
	}
	return enabled
}

// The two phases together must run every sync step once, in the order
// config.AllSyncSteps documents.
func TestSyncPhasesCoverEveryStepInOrder(t *testing.T) {
	t.Parallel()
	client := NewGraphQLClient("http://localhost:8787")
	ctx := context.Background()

	parallel := parallelSyncRunners(ctx, client, allSyncStepsEnabled())
	var parallelTypes []workflowType
	for _, r := range parallel {
		parallelTypes = append(parallelTypes, r.workflowType)
	}
	assert.Equal(t, []workflowType{workflowInitialize, workflowYahooPlayers, workflowFetchSeasons}, parallelTypes)

	var names []string
	for _, step := range sequentialSyncSteps(ctx, client) {
		names = append(names, step.name)
	}
	assert.Equal(t, config.AllSyncSteps[len(parallel):], names)
}

func TestParallelSyncRunners_OnlyEnabled(t *testing.T) {
	t.Parallel()
	runners := parallelSyncRunners(context.Background(), NewGraphQLClient("http://localhost:8787"),
		map[string]bool{config.StepFetchSeasons: true, config.StepImportSeasons: true})
	require.Len(t, runners, 1)
	assert.Equal(t, workflowFetchSeasons, runners[0].workflowType)
}

func TestRunParallelPhase_NoRunners(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	require.NoError(t, runParallelPhase(context.Background(), &out, &syncState{}, nil))
	assert.Empty(t, out.String(), "no spinner without a runner")
}

// A failed step stops the phase with its label; disabled steps never trigger.
func TestRunSequentialPhase_SkipsDisabledAndStopsAtFailure(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	var triggered []string
	step := func(name, label string, err error) syncStep {
		return syncStep{name: name, wt: workflowImportSeasons, label: label,
			trigger: func() (bool, error) {
				triggered = append(triggered, name)
				return false, err
			}}
	}
	steps := []syncStep{
		step("disabled", "disabled step", nil),
		step("failing", "failing step", errors.New("boom")),
		step("after", "later step", nil),
	}
	enabled := map[string]bool{"failing": true, "after": true}

	var out bytes.Buffer
	err := runSequentialPhase(context.Background(), &out, &syncState{}, steps, enabled)
	require.EqualError(t, err, "failing step failed: trigger workflow: boom")
	assert.Equal(t, []string{"failing"}, triggered)
}
