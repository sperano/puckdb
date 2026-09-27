package workflow

import (
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/newsevent"
	"github.com/sperano/puckdb/internal/worker/newsfeed"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

const (
	extractTestProvider = "ollama"
	extractTestModel    = "qwen3:8b"
	extractTestEffort   = "none"
	extractTestCalls    = 5
	extractTestTokens   = 10_000
)

// withNewsExtraction turns extraction on with small caps for one test.
func withNewsExtraction(t *testing.T) {
	t.Helper()
	viper.Set(config.FlagNewsExtractEnabled, true)
	viper.Set(config.FlagNewsExtractProvider, extractTestProvider)
	viper.Set(config.FlagNewsExtractModel, extractTestModel)
	viper.Set(config.FlagNewsExtractReasoningEffort, extractTestEffort)
	viper.Set(config.FlagNewsExtractMaxCalls, extractTestCalls)
	viper.Set(config.FlagNewsExtractMaxTokens, extractTestTokens)
	t.Cleanup(viper.Reset)
}

// mockEmptyRefresh stubs a refresh with nothing due and nothing processed.
func mockEmptyRefresh(env *testsuite.TestWorkflowEnvironment) {
	var act *newsfeed.Activities
	env.OnActivity(act.PlanNewsRefresh, mock.Anything, mock.Anything).Return(newsfeed.Plan{}, nil).Once()
	mockTrivialProcessAndPrune(env)
}

func runRefresh(t *testing.T, env *testsuite.TestWorkflowEnvironment) RefreshNewsResult {
	t.Helper()
	env.SetStartTime(newsTestNow)
	env.ExecuteWorkflow(RefreshNewsWorkflow, &model.RefreshNewsInput{})
	require.NoError(t, env.GetWorkflowError())
	var result RefreshNewsResult
	require.NoError(t, env.GetWorkflowResult(&result))
	env.AssertExpectations(t)
	return result
}

func TestRefreshNewsWorkflow_ExtractionOffByDefault(t *testing.T) {
	withFixedNewsSources(t)
	env := newRefreshNewsEnv(t)
	mockEmptyRefresh(env)

	result := runRefresh(t, env)

	assert.Zero(t, result.Extracted)
	assert.Empty(t, result.ExtractError)
}

func TestRefreshNewsWorkflow_ExtractsInBatchesWithinTheBudget(t *testing.T) {
	withFixedNewsSources(t)
	withNewsExtraction(t)
	env := newRefreshNewsEnv(t)
	mockEmptyRefresh(env)
	first := newsfeed.ExtractResult{Versions: 2, Succeeded: 2, Calls: 2, PromptTokens: 3000, CompletionTokens: 500,
		Events: newsevent.Outcome{Created: 1}, Remaining: true}
	second := newsfeed.ExtractResult{Versions: 1, Succeeded: 1, Calls: 1, PromptTokens: 1000, Remaining: false}
	var act *newsfeed.Activities
	env.OnActivity(act.ExtractNewsEvents, mock.Anything, mock.MatchedBy(func(in newsfeed.ExtractInput) bool {
		return in.Provider == extractTestProvider && in.Model == extractTestModel && in.ReasoningEffort == extractTestEffort &&
			in.RemainingCalls == extractTestCalls && in.RemainingTokens == extractTestTokens
	})).Return(first, nil).Once()
	env.OnActivity(act.ExtractNewsEvents, mock.Anything, mock.MatchedBy(func(in newsfeed.ExtractInput) bool {
		return in.RemainingCalls == extractTestCalls-first.Calls && in.RemainingTokens == extractTestTokens-first.Tokens()
	})).Return(second, nil).Once()

	result := runRefresh(t, env)

	assert.Equal(t, 3, result.Extracted.Versions)
	assert.Equal(t, 3, result.Extracted.Calls)
	assert.Equal(t, 1, result.Extracted.Events.Created)
}

func TestRefreshNewsWorkflow_ExtractionStopsWhenTheCallsAreSpent(t *testing.T) {
	withFixedNewsSources(t)
	withNewsExtraction(t)
	env := newRefreshNewsEnv(t)
	mockEmptyRefresh(env)
	var act *newsfeed.Activities
	env.OnActivity(act.ExtractNewsEvents, mock.Anything, mock.Anything).
		Return(newsfeed.ExtractResult{Versions: extractTestCalls, Calls: extractTestCalls, Remaining: true}, nil).Once()

	result := runRefresh(t, env)

	assert.Equal(t, extractTestCalls, result.Extracted.Calls, "no second batch once every call is used")
}

func TestRefreshNewsWorkflow_ExtractionFailureKeepsTheRefresh(t *testing.T) {
	withFixedNewsSources(t)
	withNewsExtraction(t)
	env := newRefreshNewsEnv(t)
	mockEmptyRefresh(env)
	var act *newsfeed.Activities
	failure := temporal.NewNonRetryableApplicationError("unknown LLM provider", newsfeed.ErrTypeExtractConfig, nil)
	env.OnActivity(act.ExtractNewsEvents, mock.Anything, mock.Anything).Return(newsfeed.ExtractResult{}, failure).Once()

	result := runRefresh(t, env)

	assert.Equal(t, "unknown LLM provider", result.ExtractError)
}
