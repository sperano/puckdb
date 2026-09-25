package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/sqlcdb"
	puckdbtemporal "github.com/sperano/puckdb/internal/temporal"
	"github.com/sperano/puckdb/internal/worker/newsfeed"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/workflow"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
)

// ensureNewsSchedule makes the Temporal schedule that starts periodic news
// refreshes match minutes: created or updated when positive, removed when
// zero. A run that would overlap a running refresh is skipped; each run only
// fetches the sources whose own refresh interval has passed.
func ensureNewsSchedule(ctx context.Context, schedules client.ScheduleClient, minutes int) error {
	handle := schedules.GetHandle(ctx, shared.ScheduleIDRefreshNews)
	if minutes <= 0 {
		err := handle.Delete(ctx)
		var notFound *serviceerror.NotFound
		if err != nil && !errors.As(err, &notFound) {
			return fmt.Errorf("remove news refresh schedule: %w", err)
		}
		return nil
	}
	spec := client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: time.Duration(minutes) * time.Minute}}}
	_, err := schedules.Create(ctx, client.ScheduleOptions{
		ID:      shared.ScheduleIDRefreshNews,
		Spec:    spec,
		Action:  newsScheduleAction(),
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	})
	if errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
		err = handle.Update(ctx, client.ScheduleUpdateOptions{
			DoUpdate: func(in client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
				schedule := in.Description.Schedule
				schedule.Spec = &spec
				schedule.Action = newsScheduleAction()
				return &client.ScheduleUpdate{Schedule: &schedule}, nil
			},
		})
	}
	if err != nil {
		return fmt.Errorf("set news refresh schedule: %w", err)
	}
	log.Info().Int("minutes", minutes).Msg("News refresh scheduled")
	return nil
}

func newsScheduleAction() *client.ScheduleWorkflowAction {
	return &client.ScheduleWorkflowAction{
		ID:        shared.WorkflowIDRefreshNews,
		Workflow:  workflow.RefreshNewsWorkflow,
		Args:      []any{&model.RefreshNewsInput{}},
		TaskQueue: puckdbtemporal.QueueTasks,
	}
}

// newsHTTPTimeout bounds one news source request.
const newsHTTPTimeout = 30 * time.Second

// registerNewsActivities registers the player news refresh activities.
func registerNewsActivities(w worker.Worker, pool *pgxpool.Pool, queries *sqlcdb.Queries) {
	newsActivities := &newsfeed.Activities{
		Pool:       pool,
		Queries:    queries,
		HTTPClient: &http.Client{Timeout: newsHTTPTimeout},
	}
	w.RegisterActivity(newsActivities.PlanNewsRefresh)
	w.RegisterActivity(newsActivities.FetchNewsSource)
	w.RegisterActivity(newsActivities.RecordNewsFetchFailure)
	w.RegisterActivity(newsActivities.ProcessNewsVersions)
	w.RegisterActivity(newsActivities.PruneNews)
}
