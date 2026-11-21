package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

// scheduleSpec is one Temporal schedule the worker keeps (the news refresh,
// ensureNewsSchedule) that ensureSchedule creates, updates or, with a nil
// Spec, removes.
type scheduleSpec struct {
	// Name is the schedule's name in errors and logs.
	Name   string
	ID     string
	Spec   *client.ScheduleSpec
	Action *client.ScheduleWorkflowAction
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
		ID:      s.ID,
		Spec:    *s.Spec,
		Action:  s.Action,
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	})
	if errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
		err = handle.Update(ctx, client.ScheduleUpdateOptions{
			DoUpdate: func(in client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
				schedule := in.Description.Schedule
				schedule.Spec = s.Spec
				schedule.Action = s.Action
				schedule.Policy = updatedPolicy(schedule.Policy)
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

// updatedPolicy is policy with the overlap setting Create gives a new
// schedule; its other policies are kept.
func updatedPolicy(policy *client.SchedulePolicies) *client.SchedulePolicies {
	updated := client.SchedulePolicies{}
	if policy != nil {
		updated = *policy
	}
	updated.Overlap = enumspb.SCHEDULE_OVERLAP_POLICY_SKIP
	return &updated
}
