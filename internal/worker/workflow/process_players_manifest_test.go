package workflow

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/store"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// maxProcessPlayersStateBytes bounds the encoded continuation state,
// whatever the number of players and errors.
const maxProcessPlayersStateBytes = 16 << 10

// The payload test's worst case: today's player count doubled, every
// player failing with an error the length of the FK-violation incident's.
const (
	payloadTestPlayers  = 20_000
	payloadTestErrorLen = 220
	temporalBlobLimit   = 2 << 20
)

// chainListSeasons and chainRebuiltSeasons are the game list seasons phase 1
// and the rebuild report.
var (
	chainListSeasons    = []int{2026, 2025}
	chainRebuiltSeasons = []int{2026, 2025, 2024}
)

// The chain test: five manifest ordinals, two per execution, one per batch,
// and the player at ordinal chainFailingOrdinal fails.
const (
	chainPlayers        = 5
	chainPlayersPerExec = 2
	chainBatchSize      = 1
	chainConcurrency    = 2
	chainFailingOrdinal = 2
	chainPoolSize       = 4
	chainRebuiltPool    = 3
	chainNonPageEntries = 1
	chainRebuiltNonPage = 2
	chainManifestID     = "chain-run"
	chainMatchedPerItem = 1
	chainUnmatched      = 2
)

func encodedSize(t *testing.T, value any) int {
	t.Helper()
	payloads, err := converter.GetDefaultDataConverter().ToPayloads(value)
	require.NoError(t, err)
	size := 0
	for _, p := range payloads.GetPayloads() {
		size += p.Size()
	}
	return size
}

// TestProcessPlayersStateSizeIsBounded reproduces WRK-U3: the legacy input
// carries every player and error past Temporal's 2 MB blob limit, the
// manifest state stays bounded.
func TestProcessPlayersStateSizeIsBounded(t *testing.T) {
	message := strings.Repeat("e", payloadTestErrorLen)
	legacy := &processPlayersInternalInput{Players: make([]store.BoxscorePlayer, payloadTestPlayers)}
	state := &processPlayersState{
		Config:    processPlayersConfig{BatchSize: 50, Concurrency: 10, PlayersPerExec: 2000},
		Manifest:  workplayer.ManifestRef{ID: strings.Repeat("r", 36), Count: payloadTestPlayers, Hash: strings.Repeat("h", 64)},
		Phase:     phaseVerifyUnmatched,
		Cursor:    payloadTestPlayers,
		YahooPool: workplayer.SaveYahooIDPoolResult{TotalPlayers: payloadTestPlayers, PoolSize: payloadTestPlayers},
		Progress:  NewProcessPlayersProgressReport(payloadTestPlayers),
		Totals:    processPlayersTotals{Origins: core.OriginCounts{core.OriginFileSystem: payloadTestPlayers}},
	}
	errs := make([]workplayer.PlayerError, payloadTestPlayers)
	for i := range legacy.Players {
		legacy.Players[i] = store.BoxscorePlayer{ID: int64(8_400_000 + i), FirstName: "Firstn", LastName: "Lastname", Position: "C"}
		legacy.AllErrors = append(legacy.AllErrors, message)
		errs[i] = workplayer.PlayerError{PlayerID: legacy.Players[i].ID, Message: strings.Repeat("x", 2*maxCarriedErrorRunes)}
	}
	state.countErrors(errs)

	legacySize, stateSize := encodedSize(t, legacy), encodedSize(t, state)
	t.Logf("encoded continuation input: legacy %d bytes, manifest state %d bytes", legacySize, stateSize)
	assert.Greater(t, legacySize, temporalBlobLimit, "the legacy input breaks the chain")
	assert.LessOrEqual(t, stateSize, maxProcessPlayersStateBytes)
	assert.Equal(t, payloadTestPlayers, state.Totals.Errors)
	assert.Len(t, state.ErrorSample, maxCarriedErrorSample)
}

func TestManifestBatches(t *testing.T) {
	assert.Equal(t, []ordinalRange{{4, 7}, {7, 10}, {10, 11}}, manifestBatches(4, 11, 3))
	assert.Empty(t, manifestBatches(5, 5, 3))
}

func TestNewProcessPlayersProgressReportSetsTotal(t *testing.T) {
	report := NewProcessPlayersProgressReport(chainPlayers)
	assert.Equal(t, 1+chainPlayers+1, report.Total)
}

func TestTruncateRunes(t *testing.T) {
	assert.Equal(t, "abc", truncateRunes("abc", 3))
	assert.Equal(t, "éé…", truncateRunes("ééé", 2))
}

// manifestChain records what the activities of a manifest chain saw.
type manifestChain struct {
	mu      sync.Mutex
	batches []ordinalRange
	events  []string
	ensured []int
	pools   []int
	// ensuredNonPage and poolsNonPage are the non-page entry counts beside
	// ensured and pools.
	ensuredNonPage []int
	poolsNonPage   []int
	// ensuredSeasons and poolsSeasons are the game list seasons, and
	// ensuredSources and poolsSources the pool sources, beside them.
	ensuredSeasons [][]int
	poolsSeasons   [][]int
	ensuredSources []workplayer.YahooIDPoolSource
	poolsSources   []workplayer.YahooIDPoolSource
	// rebuiltSeasons are the game list seasons ensure reports after its
	// rebuild.
	rebuiltSeasons []int
	// finishErr fails FinishProcessPlayersManifest when set.
	finishErr error
	recorded  []workplayer.PlayerError
}

func (c *manifestChain) event(e string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

// newEnv returns a test environment with the chain's activities mocked.
func (c *manifestChain) newEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ProcessPlayersWorkflow)
	env.RegisterWorkflow(ProcessPlayersWorkflowContinueV2)
	var act *workplayer.Activities
	var progress *shared.ProgressActivities
	env.OnActivity(progress.Load, mock.Anything, mock.Anything).Return(([]byte)(nil), nil).Never()
	env.OnActivity(progress.Save, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).
		Run(func(args mock.Arguments) {
			var report shared.ProgressReport
			require.NoError(t, gob.NewDecoder(bytes.NewReader(args.Get(3).([]byte))).Decode(&report))
			c.event(fmt.Sprintf("save total=%d bars=%d unmatched=%d", report.Total, progressBarTotal(&report),
				report.Groups[GroupVerifyUnmatched].Bars[0].Total))
		})
	env.OnActivity(act.CreateProcessPlayersManifest, mock.Anything, mock.Anything).
		Return(workplayer.ManifestRef{ID: chainManifestID, Count: chainPlayers, Hash: "h"}, nil)
	env.OnActivity(act.BuildYahooIDPool, mock.Anything).
		Return(&workplayer.SaveYahooIDPoolResult{
			TotalPlayers: 1, AvailablePlayers: 1, PoolSize: chainPoolSize, NonPageEntries: chainNonPageEntries,
			Source: workplayer.YahooIDPoolSourceGameLists, ListSeasons: chainListSeasons, ListEntries: 1,
		}, nil)
	env.OnActivity(act.EnsureYahooIDPool, mock.Anything, mock.Anything).Return(c.ensure)
	env.OnActivity(act.ProcessPlayerManifestBatch, mock.Anything, mock.Anything).Return(c.batch)
	env.OnActivity(act.RecordProcessPlayersErrors, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input workplayer.RecordErrorsInput) error {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.recorded = append(c.recorded, input.Errors...)
			return nil
		})
	env.OnActivity(act.LoadUnmatchedYahooPlayers, mock.Anything).
		Return([]workplayer.UnmatchedYahooPlayer{{YahooID: 1}, {YahooID: 2}}, nil)
	env.OnActivity(act.VerifyUnmatchedBatch, mock.Anything, mock.Anything).Return(&workplayer.VerifyUnmatchedResult{}, nil).
		Run(func(mock.Arguments) { c.event("verify") })
	env.OnActivity(act.CleanupYahooIDPoolData, mock.Anything).Return(nil)
	env.OnActivity(act.FinishProcessPlayersManifest, mock.Anything, chainManifestID).Return(
		func(context.Context, string) (int, error) {
			if c.finishErr != nil {
				return 0, c.finishErr
			}
			return 1, nil
		})
	return env
}

// ensure reports a lost pool on its second call, rebuilt at a new size.
func (c *manifestChain) ensure(_ context.Context, input workplayer.EnsureYahooIDPoolInput) (workplayer.EnsureYahooIDPoolResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensured = append(c.ensured, input.PoolSize)
	c.ensuredNonPage = append(c.ensuredNonPage, input.NonPageEntries)
	c.ensuredSeasons = append(c.ensuredSeasons, input.ListSeasons)
	c.ensuredSources = append(c.ensuredSources, input.Source)
	if len(c.ensured) == 2 {
		return workplayer.EnsureYahooIDPoolResult{
			Rebuilt: true, PoolSize: chainRebuiltPool, NonPageEntries: chainRebuiltNonPage, ListSeasons: c.rebuiltSeasons,
		}, nil
	}
	return workplayer.EnsureYahooIDPoolResult{
		FoundSize: input.PoolSize, PoolSize: input.PoolSize, NonPageEntries: input.NonPageEntries, ListSeasons: input.ListSeasons,
	}, nil
}

func (c *manifestChain) batch(_ context.Context, input workplayer.ManifestBatchInput) (workplayer.ManifestBatchResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.batches = append(c.batches, ordinalRange{From: input.From, To: input.To})
	c.pools = append(c.pools, input.YahooPoolSize)
	c.poolsNonPage = append(c.poolsNonPage, input.YahooNonPageEntries)
	c.poolsSeasons = append(c.poolsSeasons, input.YahooListSeasons)
	c.poolsSources = append(c.poolsSources, input.YahooPoolSource)
	result := workplayer.ManifestBatchResult{
		ProcessPlayerBatchResult: workplayer.ProcessPlayerBatchResult{Origins: core.OriginCounts{core.OriginFileSystem: 1}},
	}
	if input.From == chainFailingOrdinal {
		result.PlayerErrors = []workplayer.PlayerError{{PlayerID: 2, Stage: workplayer.StageRead, Message: "player 2: read error"}}
	} else {
		result.Imported = 1
		result.Matched = chainMatchedPerItem
		// One imported landing has exactly one match reason; alternate it so
		// the totals check summing per reason.
		reason := "name+jersey"
		if input.From%2 == 1 {
			reason = "team-tiebreaker"
		}
		result.MatchReasons = map[string]int{reason: result.Imported}
		result.BirthDatePagesDownloaded = 2
		result.BirthDatePagesFailed = 1
	}
	return result, nil
}

// continued returns the state an execution continued with, after checking
// that its report's Total matches its bars.
func continued(t *testing.T, env *testsuite.TestWorkflowEnvironment) *processPlayersState {
	t.Helper()
	require.True(t, env.IsWorkflowCompleted())
	var can *workflow.ContinueAsNewError
	require.True(t, errors.As(env.GetWorkflowError(), &can), "expected ContinueAsNew, got %v", env.GetWorkflowError())
	require.Equal(t, "ProcessPlayersWorkflowContinueV2", can.WorkflowType.Name)
	var state *processPlayersState
	require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(can.Input, &state))
	require.Equal(t, progressBarTotal(state.Progress), state.Progress.Total)
	return state
}

func TestProcessPlayersManifestChain(t *testing.T) {
	setViperInt(t, config.FlagPlayerLandingPlayersPerExec, chainPlayersPerExec)
	chain := &manifestChain{rebuiltSeasons: chainRebuiltSeasons}

	env := chain.newEnv(t)
	env.ExecuteWorkflow(ProcessPlayersWorkflow, &ProcessPlayersInput{BatchSize: intPtr(chainBatchSize), Concurrency: intPtr(chainConcurrency)})
	state := continued(t, env)
	require.Equal(t, phaseProcessPlayers, state.Phase)
	require.Equal(t, chainPoolSize, state.YahooPool.PoolSize)

	var phases []processPlayersPhase
	for state.Phase == phaseProcessPlayers {
		env = chain.newEnv(t)
		env.ExecuteWorkflow(ProcessPlayersWorkflowContinueV2, state)
		state = continued(t, env)
		phases = append(phases, state.Phase)
	}
	assert.Equal(t, []processPlayersPhase{phaseProcessPlayers, phaseProcessPlayers, phaseVerifyUnmatched}, phases,
		"three phase-2 executions, then phase 3")
	assert.ElementsMatch(t, []ordinalRange{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 5}}, chain.batches, "each ordinal once")
	assert.ElementsMatch(t, []int{chainPoolSize, chainPoolSize, chainRebuiltPool, chainRebuiltPool, chainRebuiltPool},
		chain.pools, "batches expect the pool size of their execution's check")
	assert.ElementsMatch(t, []int{chainNonPageEntries, chainNonPageEntries, chainRebuiltNonPage, chainRebuiltNonPage, chainRebuiltNonPage},
		chain.poolsNonPage, "batches get the non-page entry count of their execution's check")
	assert.ElementsMatch(t, [][]int{chainListSeasons, chainListSeasons, chainRebuiltSeasons, chainRebuiltSeasons, chainRebuiltSeasons},
		chain.poolsSeasons, "batches get the game list seasons of their execution's check")
	gameLists := workplayer.YahooIDPoolSourceGameLists
	assert.Equal(t, []workplayer.YahooIDPoolSource{gameLists, gameLists, gameLists, gameLists, gameLists}, chain.poolsSources)
	assert.Equal(t, 1, state.Totals.Errors)
	assert.Equal(t, 4, state.Totals.Imported)
	assert.Equal(t, map[string]int{"name+jersey": 2, "team-tiebreaker": 2}, state.Totals.MatchReasons,
		"match reasons are summed across batches and executions")
	assert.Equal(t, 8, state.Totals.BirthDatePagesDownloaded, "birth date page downloads are summed across batches and executions")
	assert.Equal(t, 4, state.Totals.BirthDatePagesFailed, "birth date page failures are summed across batches and executions")
	assert.Equal(t, []string{"player 2: read error"}, state.ErrorSample)
	assert.Equal(t, []workplayer.PlayerError{{PlayerID: 2, Stage: workplayer.StageRead, Message: "player 2: read error"}},
		chain.recorded, "errors of completed batches reach the error table")

	chain.events = nil
	env = chain.newEnv(t)
	env.ExecuteWorkflow(ProcessPlayersWorkflowContinueV2, state)
	require.True(t, env.IsWorkflowCompleted())
	require.ErrorContains(t, env.GetWorkflowError(), "finished with 1 per-player error(s)")
	require.ErrorContains(t, env.GetWorkflowError(), "manifest_id "+chainManifestID)

	assert.Equal(t, []int{chainPoolSize, chainPoolSize, chainRebuiltPool, chainRebuiltPool}, chain.ensured,
		"every continuation checks the pool, against the rebuilt size after a rebuild")
	assert.Equal(t, []int{chainNonPageEntries, chainNonPageEntries, chainRebuiltNonPage, chainRebuiltNonPage}, chain.ensuredNonPage,
		"the non-page entry count crosses ContinueAsNew and follows a rebuild")
	assert.Equal(t, [][]int{chainListSeasons, chainListSeasons, chainRebuiltSeasons, chainRebuiltSeasons}, chain.ensuredSeasons,
		"the game list seasons cross ContinueAsNew and follow a rebuild")
	assert.Equal(t, []workplayer.YahooIDPoolSource{gameLists, gameLists, gameLists, gameLists}, chain.ensuredSources)
	wantResize := fmt.Sprintf("save total=%d bars=%d unmatched=%d", 1+chainPlayers+chainUnmatched,
		1+chainPlayers+chainUnmatched, chainUnmatched)
	require.GreaterOrEqual(t, len(chain.events), 3)
	assert.Equal(t, wantResize, chain.events[1], "phase 3 saves its resized bar with a recomputed Total")
	assert.Equal(t, "verify", chain.events[2], "before the first verify batch")
}

// TestProcessPlayersManifestChain_PagePoolSource: with a page-source pool
// (no seasons) injected into the state phase 1 carries over (the page phase
// 1 itself is covered by the process_players_page_pool_* replay histories),
// every pool check and batch of the continuations gets the page source,
// before and after a rebuild.
func TestProcessPlayersManifestChain_PagePoolSource(t *testing.T) {
	setViperInt(t, config.FlagPlayerLandingPlayersPerExec, chainPlayersPerExec)
	chain := &manifestChain{}

	env := chain.newEnv(t)
	env.ExecuteWorkflow(ProcessPlayersWorkflow, &ProcessPlayersInput{BatchSize: intPtr(chainBatchSize), Concurrency: intPtr(chainConcurrency)})
	state := continued(t, env)
	state.YahooPool.Source = workplayer.YahooIDPoolSourcePages
	state.YahooPool.ListSeasons = nil

	for state.Phase == phaseProcessPlayers {
		env = chain.newEnv(t)
		env.ExecuteWorkflow(ProcessPlayersWorkflowContinueV2, state)
		state = continued(t, env)
	}
	require.Equal(t, phaseVerifyUnmatched, state.Phase)
	pages := workplayer.YahooIDPoolSourcePages
	assert.Equal(t, []workplayer.YahooIDPoolSource{pages, pages, pages}, chain.ensuredSources)
	assert.Equal(t, [][]int{nil, nil, nil}, chain.ensuredSeasons)
	assert.Equal(t, []workplayer.YahooIDPoolSource{pages, pages, pages, pages, pages}, chain.poolsSources)
	assert.Equal(t, [][]int{nil, nil, nil, nil, nil}, chain.poolsSeasons)
	assert.Equal(t, pages, state.YahooPool.Source)
	assert.Nil(t, state.YahooPool.ListSeasons)
}

func TestProcessPlayersManifestChain_NoPlayers(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var act *workplayer.Activities
	env.OnActivity(act.CreateProcessPlayersManifest, mock.Anything, mock.Anything).Return(workplayer.ManifestRef{ID: "run"}, nil)

	env.ExecuteWorkflow(ProcessPlayersWorkflow, (*ProcessPlayersInput)(nil))

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var result *ProcessPlayersResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, &ProcessPlayersResult{}, result)
}

func TestProcessPlayersErrorSampleIsCapped(t *testing.T) {
	state := &processPlayersState{}
	errs := make([]workplayer.PlayerError, maxCarriedErrorSample+5)
	for i := range errs {
		errs[i] = workplayer.PlayerError{Message: fmt.Sprintf("error %d", i)}
	}
	state.countErrors(errs[:3])
	state.countErrors(errs[3:])

	assert.Equal(t, len(errs), state.Totals.Errors)
	require.Len(t, state.ErrorSample, maxCarriedErrorSample)
	assert.Equal(t, "error 0", state.ErrorSample[0])
	assert.Equal(t, fmt.Sprintf("error %d", maxCarriedErrorSample-1), state.ErrorSample[maxCarriedErrorSample-1])
}

// verifyState is a valid phase-3 state of the chain test.
func verifyState() *processPlayersState {
	return &processPlayersState{
		Config:   processPlayersConfig{BatchSize: chainBatchSize, Concurrency: chainConcurrency, PlayersPerExec: chainPlayersPerExec},
		Manifest: workplayer.ManifestRef{ID: chainManifestID, Count: chainPlayers, Hash: "h"},
		Phase:    phaseVerifyUnmatched,
		Cursor:   chainPlayers,
		Totals:   processPlayersTotals{Imported: chainPlayers},
		Progress: NewProcessPlayersProgressReport(chainPlayers),
	}
}

func TestProcessPlayersStateValidate(t *testing.T) {
	require.NoError(t, verifyState().validate())
	invalid := map[string]func(*processPlayersState){
		"no progress":    func(s *processPlayersState) { s.Progress = nil },
		"zero batch":     func(s *processPlayersState) { s.Config.BatchSize = 0 },
		"zero window":    func(s *processPlayersState) { s.Config.PlayersPerExec = 0 },
		"no players":     func(s *processPlayersState) { s.Manifest.Count = 0 },
		"cursor past":    func(s *processPlayersState) { s.Cursor = chainPlayers + 1 },
		"phase 2 at end": func(s *processPlayersState) { s.Phase = phaseProcessPlayers },
	}
	for name, mutate := range invalid {
		state := verifyState()
		mutate(state)
		assert.Error(t, state.validate(), name)
	}
	var nilState *processPlayersState
	assert.Error(t, nilState.validate())
}

func TestProcessPlayersManifestVerify_FinishFailureStillReports(t *testing.T) {
	chain := &manifestChain{finishErr: temporal.NewNonRetryableApplicationError("db down", "test", nil)}
	env := chain.newEnv(t)

	env.ExecuteWorkflow(ProcessPlayersWorkflowContinueV2, verifyState())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var result *ProcessPlayersResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, chainPlayers, result.ImportedPlayers)
}

func TestManifestResultCarriesMatchReasons(t *testing.T) {
	reasons := map[string]int{"name+jersey": 3, "ambiguous": 1}
	state := &processPlayersState{Totals: processPlayersTotals{Matched: 3, MatchReasons: reasons}}

	result := manifestResult(state, &workplayer.UnmatchedReport{})

	assert.Equal(t, 3, result.MatchedWithYahoo)
	assert.Equal(t, reasons, result.MatchReasons)
}

func TestManifestResultCarriesBirthDatePageCounts(t *testing.T) {
	state := &processPlayersState{Totals: processPlayersTotals{BirthDatePagesDownloaded: 4, BirthDatePagesFailed: 1}}

	result := manifestResult(state, &workplayer.UnmatchedReport{})

	assert.Equal(t, 4, result.BirthDatePagesDownloaded)
	assert.Equal(t, 1, result.BirthDatePagesFailed)
}
