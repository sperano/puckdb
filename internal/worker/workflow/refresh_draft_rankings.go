package workflow

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/news"
	"github.com/sperano/puckdb/internal/worker/draftranking"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	// draftRankingHeartbeatTimeout bounds the gap between a refresh's
	// heartbeats (one per step); cancellation is delivered on a heartbeat.
	draftRankingHeartbeatTimeout = 5 * time.Minute
	// draftRankingMaxAttempts bounds retries of an internal failure (a
	// known failure such as missing rules is not retried).
	draftRankingMaxAttempts = 3
	// draftRankingCancelTimeout bounds marking a canceled run's attempts.
	draftRankingCancelTimeout = time.Minute
	errTypeInvalidInput       = "InvalidInput"
)

var (
	draftBenchPolicies = map[model.DraftBenchPolicy]draft.BenchPolicy{
		model.DraftBenchPolicyIncluded: draft.BenchIncluded,
		model.DraftBenchPolicyExcluded: draft.BenchExcluded,
	}
	draftWorkloadPolicies = map[model.DraftWorkloadCapPolicy]draft.WorkloadCapPolicy{
		model.DraftWorkloadCapPolicyPerPlayer: draft.WorkloadCapsPerPlayer,
	}
)

// draftRankingConfig is what RefreshDraftRankingsWorkflow snapshots at
// start: the configured leagues of every season, the enabled news sources
// whose coverage the snapshots report, and how many snapshots to keep.
type draftRankingConfig struct {
	Leagues map[int][]int `json:"leagues"`
	Sources []news.Source `json:"sources"`
	Keep    int           `json:"keep"`
	Err     string        `json:"err,omitempty"`
}

func loadDraftRankingConfig() draftRankingConfig {
	cfg := draftRankingConfig{
		Leagues: make(map[int][]int),
		Keep:    shared.ViperIntOrDefault(config.FlagDraftKeepSnapshots, config.DefaultDraftKeepSnapshots),
	}
	seasons := loadYahooSeasons()
	errs := []error{seasons.LoadError()}
	for season, leagues := range seasons.Seasons {
		for _, league := range leagues.Leagues {
			cfg.Leagues[season] = append(cfg.Leagues[season], league.LeagueID)
		}
	}
	sources, err := loadNewsSources(viper.GetString(config.FlagNewsSourcesFile))
	errs = append(errs, err)
	if err == nil {
		cfg.Sources, err = news.EnabledSources(sources, nil)
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		cfg.Err = err.Error()
	}
	return cfg
}

// DraftRankingOutcome is one league's refresh outcome.
type DraftRankingOutcome struct {
	LeagueID int               `json:"leagueId"`
	Refresh  draftrank.Refresh `json:"refresh"`
	// Error is set when the activity itself failed after its retries.
	Error string `json:"error,omitempty"`
}

// RefreshDraftRankingsResult summarizes one draft ranking refresh.
type RefreshDraftRankingsResult struct {
	Season  int                   `json:"season"`
	Leagues []DraftRankingOutcome `json:"leagues"`
}

// RefreshDraftRankingsWorkflow recomputes the ranking snapshot of each
// league, one league at a time. Each league's attempt is recorded; a league
// that fails keeps its last good snapshot and the others still run. On
// cancellation the running refresh stops at its next step, nothing partial
// is stored, and the run's unfinished attempts are marked canceled.
func RefreshDraftRankingsWorkflow(ctx workflow.Context, input *model.RefreshDraftRankingsInput) (RefreshDraftRankingsResult, error) {
	cfg, err := shared.SnapshotConfig(ctx, loadDraftRankingConfig)
	if err != nil {
		return RefreshDraftRankingsResult{}, err
	}
	if cfg.Err != "" {
		return RefreshDraftRankingsResult{}, fmt.Errorf("load draft ranking configuration: %s", cfg.Err)
	}
	if input == nil {
		input = &model.RefreshDraftRankingsInput{}
	}
	result := RefreshDraftRankingsResult{Season: currentSeasonStart(ctx)}
	if input.Season != nil && *input.Season > 0 {
		result.Season = *input.Season
	}
	leagues, options, err := draftRefreshPlan(input, cfg, result.Season)
	if err != nil {
		return result, temporal.NewNonRetryableApplicationError(err.Error(), errTypeInvalidInput, err)
	}
	tracker, err := shared.InitTracker(ctx, &shared.ProgressReport{
		Total:  len(leagues),
		Groups: []shared.ProgressGroup{{Header: fmt.Sprintf("Refreshing draft rankings for %d...", result.Season), Bars: []shared.ProgressBar{{Total: len(leagues)}}}},
	})
	if err != nil {
		return result, err
	}
	tracker.StartGroup(ctx, 0)
	runID := workflow.GetInfo(ctx).WorkflowExecution.RunID
	for _, leagueID := range leagues {
		outcome, err := refreshDraftLeague(ctx, draftranking.RefreshInput{
			RunID: runID, Season: result.Season, LeagueID: leagueID, Options: options, Keep: cfg.Keep, Sources: cfg.Sources,
		})
		if temporal.IsCanceledError(err) || ctx.Err() != nil {
			markDraftRefreshesCanceled(ctx, runID)
			if err == nil {
				// The activity finished as the cancel landed; the run was
				// still canceled.
				err = temporal.NewCanceledError()
			}
			return result, err
		}
		result.Leagues = append(result.Leagues, outcome)
		tracker.IncrementBar(ctx, 0, 0)
	}
	if len(leagues) == 0 {
		workflow.GetLogger(ctx).Warn("No draft leagues to refresh: name leagueIds or configure the season in seasons.yaml", "season", result.Season)
	}
	tracker.CompleteGroup(ctx, 0, draftRankingSummary(result, tracker.GetElapsed(ctx, 0)))
	return result, nil
}

// draftRefreshPlan validates the input and returns the leagues to refresh
// (the season's configured leagues when none are named, possibly none, so a
// default sync without draft leagues does not fail) and the options.
func draftRefreshPlan(input *model.RefreshDraftRankingsInput, cfg draftRankingConfig, season int) ([]int, draftrank.Options, error) {
	leagues := slices.Clone(input.LeagueIds)
	if len(leagues) == 0 {
		leagues = slices.Clone(cfg.Leagues[season])
	}
	slices.Sort(leagues)
	leagues = slices.Compact(leagues)
	for _, id := range leagues {
		if id <= 0 {
			return nil, draftrank.Options{}, fmt.Errorf("invalid league ID %d", id)
		}
	}
	options := draftrank.Options{BenchPolicy: string(draft.BenchIncluded)}
	if input.BenchPolicy != nil {
		bench, known := draftBenchPolicies[*input.BenchPolicy]
		if !known {
			return nil, draftrank.Options{}, fmt.Errorf("unknown bench policy %q", *input.BenchPolicy)
		}
		options.BenchPolicy = string(bench)
	}
	if input.WorkloadCapPolicy != nil {
		workload, known := draftWorkloadPolicies[*input.WorkloadCapPolicy]
		if !known {
			return nil, draftrank.Options{}, fmt.Errorf("unknown workload cap policy %q", *input.WorkloadCapPolicy)
		}
		options.WorkloadCapPolicy = string(workload)
	}
	if input.UncertaintyPenalty != nil {
		penalty := *input.UncertaintyPenalty
		if math.IsNaN(penalty) || math.IsInf(penalty, 0) || penalty < 0 {
			return nil, draftrank.Options{}, fmt.Errorf("uncertainty penalty must be finite and nonnegative")
		}
		options.UncertaintyPenalty = penalty
	}
	return leagues, options, nil
}

func refreshDraftLeague(ctx workflow.Context, input draftranking.RefreshInput) (DraftRankingOutcome, error) {
	var act *draftranking.Activities
	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: draftrank.RefreshTimeout,
		HeartbeatTimeout:    draftRankingHeartbeatTimeout,
		WaitForCancellation: true,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts:    draftRankingMaxAttempts,
			BackoffCoefficient: config.DefaultBackoffCoefficient,
		},
	})
	outcome := DraftRankingOutcome{LeagueID: input.LeagueID}
	err := workflow.ExecuteActivity(actCtx, act.RefreshDraftRanking, input).Get(ctx, &outcome.Refresh)
	if err != nil && !temporal.IsCanceledError(err) {
		workflow.GetLogger(ctx).Error("Draft ranking refresh failed; the league keeps its last snapshot", "league", input.LeagueID, "error", err)
		outcome.Error = activityMessage(err)
		return outcome, nil
	}
	return outcome, err
}

// markDraftRefreshesCanceled records the cancellation of attempts whose
// activity stopped before recording it; it runs after the workflow's own
// context was canceled.
func markDraftRefreshesCanceled(ctx workflow.Context, runID string) {
	var act *draftranking.Activities
	cleanup, _ := workflow.NewDisconnectedContext(ctx)
	cleanup = workflow.WithActivityOptions(cleanup, workflow.ActivityOptions{StartToCloseTimeout: draftRankingCancelTimeout})
	if err := workflow.ExecuteActivity(cleanup, act.CancelDraftRankingRefreshes, draftranking.CancelInput{RunID: runID}).Get(cleanup, nil); err != nil {
		workflow.GetLogger(ctx).Error("Could not mark canceled draft ranking refreshes", "error", err)
	}
}

func draftRankingSummary(r RefreshDraftRankingsResult, elapsed string) string {
	succeeded := 0
	for _, league := range r.Leagues {
		if league.Error == "" && league.Refresh.State == draftrank.RefreshSucceeded {
			succeeded++
		}
	}
	return fmt.Sprintf("Refreshed %d of %d leagues in %s.", succeeded, len(r.Leagues), elapsed)
}
