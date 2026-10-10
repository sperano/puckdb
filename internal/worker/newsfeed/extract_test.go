package newsfeed

import (
	"sync"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/newsevent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	budgetCalls  = 3
	budgetTokens = 1000
	callTokens   = 600
	// edtOffset puts the injected clock outside UTC, so the tests see the
	// activities' clock normalize it.
	edtOffset = -4 * time.Hour
)

func TestRunBudgetStopsAtTheCallCap(t *testing.T) {
	b := &runBudget{calls: budgetCalls, tokens: budgetTokens * budgetCalls}
	for range budgetCalls {
		assert.True(t, b.Reserve())
	}
	assert.False(t, b.Reserve())
}

func TestRunBudgetStopsOnceTheTokensAreSpent(t *testing.T) {
	b := &runBudget{calls: budgetCalls, tokens: budgetTokens}
	assert.True(t, b.Reserve())
	b.Spend(llm.Usage{PromptTokens: callTokens, CompletionTokens: callTokens})
	assert.False(t, b.Reserve(), "a call may overshoot the token cap, the next one is refused")
}

func TestRunBudgetIsSafeForConcurrentCalls(t *testing.T) {
	b := &runBudget{calls: budgetCalls, tokens: budgetTokens}
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	for range 2 * budgetCalls {
		wg.Go(func() {
			if b.Reserve() {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	assert.Equal(t, budgetCalls, granted)
}

func TestExtractResultCountsVersionOutcomes(t *testing.T) {
	var r ExtractResult
	r.count(newsevent.VersionResult{Status: newsevent.ExtractionSucceeded, Called: true,
		Usage: llm.Usage{PromptTokens: callTokens, CompletionTokens: 1}, Claims: 4, Events: newsevent.Outcome{Created: 1}})
	r.count(newsevent.VersionResult{Status: newsevent.ExtractionSucceeded, Cached: true, Events: newsevent.Outcome{Supported: 1}})
	r.count(newsevent.VersionResult{Status: newsevent.ExtractionInvalid, Called: true, Issues: 2})
	r.count(newsevent.VersionResult{Status: newsevent.ExtractionFailed, Called: true})
	r.count(newsevent.VersionResult{Status: newsevent.ExtractionDeferred})

	want := ExtractResult{Versions: 5, Succeeded: 2, Invalid: 1, Failed: 1, Deferred: 1, Cached: 1, Calls: 3,
		PromptTokens: callTokens, CompletionTokens: 1, Claims: 4, Issues: 2, Events: newsevent.Outcome{Created: 1, Supported: 1}}
	assert.Equal(t, want, r)
	assert.Equal(t, callTokens+1, r.Tokens())

	var total ExtractResult
	total.Add(r)
	total.Add(r)
	assert.Equal(t, 2*r.Versions, total.Versions)
	assert.Equal(t, 2*r.Events.Created, total.Events.Created)
}

func TestExtractRunnerRejectsUnknownReasoningEffort(t *testing.T) {
	acts := &Activities{LLM: func(llm.Provider, string, time.Duration) llm.Client { return nil }}

	_, err := acts.extractRunner(ExtractInput{Provider: "ollama", Model: "qwen", ReasoningEffort: "off"})

	assertApplicationErrorType(t, err, ErrTypeExtractConfig, true)
}

func TestExtractRunnerNormalizesReasoningEffort(t *testing.T) {
	acts := &Activities{LLM: func(llm.Provider, string, time.Duration) llm.Client { return nil }}

	runner, err := acts.extractRunner(ExtractInput{Provider: "ollama", Model: "qwen", ReasoningEffort: " None "})

	require.NoError(t, err)
	assert.Equal(t, "none", runner.Extractor.ReasoningEffort)
}

func TestExtractRunnerUsesTheActivitiesClock(t *testing.T) {
	fixed := time.Date(2026, time.October, 1, 8, 0, 0, 0, time.FixedZone("EDT", int(edtOffset.Seconds())))
	acts := &Activities{
		LLM: func(llm.Provider, string, time.Duration) llm.Client { return nil },
		Now: func() time.Time { return fixed },
	}

	runner, err := acts.extractRunner(ExtractInput{Provider: "ollama", Model: "qwen"})

	require.NoError(t, err)
	require.NotNil(t, runner.Now, "the constructor always sets the runner's clock")
	assert.Equal(t, fixed.UTC(), runner.Now())
}

func TestExtractRunnerDefaultsToTheWallClock(t *testing.T) {
	acts := &Activities{LLM: func(llm.Provider, string, time.Duration) llm.Client { return nil }}

	runner, err := acts.extractRunner(ExtractInput{Provider: "ollama", Model: "qwen"})
	require.NoError(t, err)
	require.NotNil(t, runner.Now, "the constructor always sets the runner's clock")

	before := time.Now()
	now := runner.Now()
	after := time.Now()
	assert.False(t, now.Before(before) || now.After(after), "%s is outside [%s, %s]", now, before, after)
	assert.Equal(t, time.UTC, now.Location())
}
