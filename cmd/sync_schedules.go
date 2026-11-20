package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	puckdbtemporal "github.com/sperano/puckdb/internal/temporal"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/workflow"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

// syncScheduleCatchupWindow is how late a sync run may still start after a
// Temporal outage; a run missed entirely is harmless, since the next one
// fetches every day not cached yet.
const syncScheduleCatchupWindow = 12 * time.Hour

// errEmptySyncScheduleTimeZone refuses a sync schedule with no time zone,
// which would silently run in UTC.
var errEmptySyncScheduleTimeZone = errors.New("sync schedule time zone is empty")

// syncSchedulesConfig is the worker's sync schedule settings: a cron
// expression per sync (empty: no schedule) and their time zone.
type syncSchedulesConfig struct {
	Seasons  string
	Players  string
	TimeZone string
}

// scheduleSpec is one Temporal schedule ensureSchedule creates, updates or,
// with a nil Spec, removes.
type scheduleSpec struct {
	// Name is the schedule's name in errors and logs.
	Name          string
	ID            string
	Spec          *client.ScheduleSpec
	Action        *client.ScheduleWorkflowAction
	CatchupWindow time.Duration
}

// ensureSyncSchedules makes the seasons and players sync schedules match
// cfg. A run that would overlap the previous one of its schedule is skipped;
// one that finds a manual sync running skips itself (see
// workflow.ScheduledSeasonsSyncWorkflow).
func ensureSyncSchedules(ctx context.Context, schedules client.ScheduleClient, cfg syncSchedulesConfig) error {
	if cfg.Seasons != "" || cfg.Players != "" {
		// time.LoadLocation and Temporal both read "" as UTC.
		if cfg.TimeZone == "" {
			return errEmptySyncScheduleTimeZone
		}
		if _, err := time.LoadLocation(cfg.TimeZone); err != nil {
			return fmt.Errorf("sync schedule time zone %q: %w", cfg.TimeZone, err)
		}
	}
	specs := []scheduleSpec{
		syncScheduleSpec("seasons sync", shared.ScheduleIDSeasonsSync, cfg.Seasons, cfg.TimeZone,
			syncScheduleAction(shared.WorkflowIDScheduledSeasonsSync, workflow.ScheduledSeasonsSyncWorkflow)),
		syncScheduleSpec("players sync", shared.ScheduleIDPlayersSync, cfg.Players, cfg.TimeZone,
			syncScheduleAction(shared.WorkflowIDScheduledPlayersSync, workflow.ScheduledPlayersSyncWorkflow)),
	}
	for _, spec := range specs {
		if err := ensureSchedule(ctx, schedules, spec); err != nil {
			return err
		}
	}
	return nil
}

// syncScheduleSpec is the schedule running action at cron in timeZone, or
// its removal when cron is empty.
func syncScheduleSpec(name, id, cron, timeZone string, action *client.ScheduleWorkflowAction) scheduleSpec {
	spec := scheduleSpec{Name: name, ID: id, Action: action, CatchupWindow: syncScheduleCatchupWindow}
	if cron != "" {
		spec.Spec = &client.ScheduleSpec{CronExpressions: []string{cron}, TimeZoneName: timeZone}
	}
	return spec
}

func syncScheduleAction(id string, fn any) *client.ScheduleWorkflowAction {
	return &client.ScheduleWorkflowAction{ID: id, Workflow: fn, TaskQueue: puckdbtemporal.QueueTasks}
}

// ensureSchedule creates the schedule, updates it when it exists (another
// worker replica may have created it) or, with no spec, removes it. Every
// schedule skips a run that would overlap the previous one.
func ensureSchedule(ctx context.Context, schedules client.ScheduleClient, s scheduleSpec) error {
	handle := schedules.GetHandle(ctx, s.ID)
	if s.Spec == nil {
		return removeSchedule(ctx, handle, s)
	}
	_, err := schedules.Create(ctx, client.ScheduleOptions{
		ID:            s.ID,
		Spec:          *s.Spec,
		Action:        s.Action,
		Overlap:       enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		CatchupWindow: s.CatchupWindow,
	})
	if errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
		err = handle.Update(ctx, client.ScheduleUpdateOptions{
			DoUpdate: func(in client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
				schedule := in.Description.Schedule
				schedule.Spec = s.Spec
				schedule.Action = s.Action
				schedule.Policy = updatedPolicy(schedule.Policy, s.CatchupWindow)
				return &client.ScheduleUpdate{Schedule: &schedule}, nil
			},
		})
	}
	if err != nil {
		return fmt.Errorf("set %s schedule: %w", s.Name, err)
	}
	logScheduleSpec(log.Info().Str("schedule", s.Name).Str("schedule_id", s.ID), s.Spec).Msg("Schedule set")
	return nil
}

// removeSchedule deletes the schedule behind handle. An absent schedule is
// not an error and is not logged as removed: only a debug line says so.
func removeSchedule(ctx context.Context, handle client.ScheduleHandle, s scheduleSpec) error {
	err := handle.Delete(ctx)
	var notFound *serviceerror.NotFound
	if errors.As(err, &notFound) {
		log.Debug().Str("schedule", s.Name).Str("schedule_id", s.ID).Msg("Schedule absent, nothing to remove")
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove %s schedule: %w", s.Name, err)
	}
	log.Info().Str("schedule", s.Name).Str("schedule_id", s.ID).Msg("Schedule removed")
	return nil
}

// logScheduleSpec adds what spec runs on to e: its cron expressions and time
// zone, or its intervals.
func logScheduleSpec(e *zerolog.Event, spec *client.ScheduleSpec) *zerolog.Event {
	if len(spec.CronExpressions) > 0 {
		e = e.Strs("cron", spec.CronExpressions).Str("time_zone", spec.TimeZoneName)
	}
	if len(spec.Intervals) > 0 {
		every := make([]string, len(spec.Intervals))
		for i, interval := range spec.Intervals {
			every[i] = interval.Every.String()
		}
		e = e.Strs("every", every)
	}
	return e
}

// updatedPolicy is policy with the overlap and catch-up settings Create
// gives a new schedule; a zero catchupWindow keeps the existing one.
func updatedPolicy(policy *client.SchedulePolicies, catchupWindow time.Duration) *client.SchedulePolicies {
	updated := client.SchedulePolicies{}
	if policy != nil {
		updated = *policy
	}
	updated.Overlap = enumspb.SCHEDULE_OVERLAP_POLICY_SKIP
	if catchupWindow > 0 {
		updated.CatchupWindow = catchupWindow
	}
	return &updated
}
