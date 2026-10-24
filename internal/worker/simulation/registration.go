package simulation

import (
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// SimPoolWorkflowName and the activity names below are serialized into
// Temporal history. Keep their values stable even if the Go symbols move or
// are renamed; changing a value requires an explicit workflow version gate.
const SimPoolWorkflowName = "SimPoolWorkflow"

const (
	activityNameLoadPoolState            = "LoadPoolState"
	activityNameLoadDraftCandidates      = "LoadDraftCandidates"
	activityNameDraftPick                = "DraftPick"
	activityNameProcessWaivers           = "ProcessWaivers"
	activityNameBuildFreeAgentPool       = "BuildFreeAgentPool"
	activityNameBuildManageRosterContext = "BuildManageRosterContext"
	activityNameManageRoster             = "ManageRoster"
	activityNameCollectDayStats          = "CollectDayStats"
	activityNameUpdateStandings          = "UpdateStandings"
	activityNameSetPoolStatus            = "SetPoolStatus"
	activityNameRecordDraftOrder         = "RecordDraftOrder"
	activityNamePickTeamName             = "PickTeamName"
	activityNameRecordDayDuration        = "RecordDayDuration"
)

// RegisterSimPoolWorkflow pins the workflow's serialized name rather than
// deriving it from the current Go function name.
func RegisterSimPoolWorkflow(w worker.Worker) {
	w.RegisterWorkflowWithOptions(SimPoolWorkflow, workflow.RegisterOptions{Name: SimPoolWorkflowName})
}

// RegisterActivities pins every simulation activity name already present in
// stored histories. The method list is intentionally explicit: it is the
// workflow/activity compatibility boundary.
func RegisterActivities(w worker.Worker, acts *Activities) {
	registrations := []struct {
		name string
		fn   any
	}{
		{activityNameLoadPoolState, acts.LoadPoolState},
		{activityNameLoadDraftCandidates, acts.LoadDraftCandidates},
		{activityNameDraftPick, acts.DraftPick},
		{activityNameProcessWaivers, acts.ProcessWaivers},
		{activityNameBuildFreeAgentPool, acts.BuildFreeAgentPool},
		{activityNameBuildManageRosterContext, acts.BuildManageRosterContext},
		{activityNameManageRoster, acts.ManageRoster},
		{activityNameCollectDayStats, acts.CollectDayStats},
		{activityNameUpdateStandings, acts.UpdateStandings},
		{activityNameSetPoolStatus, acts.SetPoolStatus},
		{activityNameRecordDraftOrder, acts.RecordDraftOrder},
		{activityNamePickTeamName, acts.PickTeamName},
		{activityNameRecordDayDuration, acts.RecordDayDuration},
	}
	for _, registration := range registrations {
		w.RegisterActivityWithOptions(registration.fn, activity.RegisterOptions{Name: registration.name})
	}
}
