package workflow

import (
	"encoding/json"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	nhlSeasonFetchHistory  = "testdata/nhl_season_fetch.json"
	nhlSeasonImportHistory = "testdata/nhl_season_import.json"
	// nhlSeasonHistoryTeamIDs is the Yahoo team count of the recorded
	// season config (one league, teams 1 and 2), absent from the no-teams
	// history.
	nhlSeasonHistoryTeamIDs = 2
	// nhlSeasonHistoryPlayoffTeams is the ListSeasonTeams result (TOR, MTL)
	// of the histories with teams.
	nhlSeasonHistoryPlayoffTeams = 2
	// nondeterminismErrorCode prefixes the SDK's replay mismatch errors.
	nondeterminismErrorCode = "TMPRL1100"
)

// nhlSeasonHistory is one NHL season child history recorded before the
// shared runner, with what it must contain: the activities it scheduled, in
// order, its version markers, whether the day group's StartGroup save (a
// LocalActivity marker) precedes the season activities, and the inputs that
// make it its case.
type nhlSeasonHistory struct {
	path           string
	workflow       any
	activities     []string
	versions       map[string]int
	startDaysFirst bool
	teamIDs        int  // Yahoo team IDs in every day input
	force          bool // ImportDayIfChanged's Force (import histories)
}

// nhlSeasonHistories were recorded on a Temporal dev server with the
// FetchNHLSeasonWorkflow and ImportNHLSeasonWorkflow of commit fb7533a
// (before runSeasonDays), sticky execution disabled, for season 2023-2024
// with standings 2023-10-10 to 2023-10-12 (3 days) and stub activities
// registered under their production names: Save and the season activities
// return nil; FetchDay returns one FileSystem read; ImportDayIfChanged
// reports 2023-10-10 unchanged, 2023-10-11 with 2 reads and 1 skipped row,
// other days (and every forced day) 3 reads; ListSeasonTeams returns TOR and
// MTL (none in the no-teams history); FetchTeamPlayoffGames returns 1 game
// found and fetched (the result had no origins then); ImportTeamPlayoffGames
// 1 game, 2 skaters, 1 goalie and 4 reads. The Yahoo config holds league
// 4321 with teams 1 and 2 for 2023, except in the no-teams history (none).
func nhlSeasonHistories() []nhlSeasonHistory {
	fetchVersions := map[string]int{"config-snapshot": 1, dayLoopBatchedProgressChangeID: dayLoopBatchedProgressVersion}
	importVersions := map[string]int{"config-snapshot": 1, importDaySkipChangeID: importDaySkipVersion,
		dayLoopBatchedProgressChangeID: dayLoopBatchedProgressVersion}
	fetchDays := []string{"FetchSeasonRosters", "FetchClubStats", "FetchDay", "FetchDay", "FetchDay", "ListSeasonTeams"}
	importActivities := []string{"ImportSeasonRosters", "ImportClubStats",
		"ImportDayIfChanged", "ImportDayIfChanged", "ImportDayIfChanged",
		"ListSeasonTeams", "ImportTeamPlayoffGames", "ImportTeamPlayoffGames"}
	return []nhlSeasonHistory{
		{path: nhlSeasonFetchHistory, workflow: FetchNHLSeasonWorkflow,
			activities: append(append([]string{}, fetchDays...), "FetchTeamPlayoffGames", "FetchTeamPlayoffGames"),
			versions:   fetchVersions, startDaysFirst: true, teamIDs: nhlSeasonHistoryTeamIDs},
		{path: "testdata/nhl_season_fetch_no_teams.json", workflow: FetchNHLSeasonWorkflow,
			activities: fetchDays, versions: fetchVersions, startDaysFirst: true},
		{path: nhlSeasonImportHistory, workflow: ImportNHLSeasonWorkflow,
			activities: importActivities, versions: importVersions, teamIDs: nhlSeasonHistoryTeamIDs},
		{path: "testdata/nhl_season_import_reimport.json", workflow: ImportNHLSeasonWorkflow,
			activities: importActivities, versions: importVersions, teamIDs: nhlSeasonHistoryTeamIDs, force: true},
	}
}

// TestNHLSeasonWorkflowsReplayRecordedHistories replays every history
// recorded before the shared runner with the current workflows: a changed
// command sequence (order, type, StartGroup placement, version markers)
// fails here with a nondeterminism error.
func TestNHLSeasonWorkflowsReplayRecordedHistories(t *testing.T) {
	for _, tc := range nhlSeasonHistories() {
		t.Run(tc.path, func(t *testing.T) {
			replayer := worker.NewWorkflowReplayer()
			replayer.RegisterWorkflow(tc.workflow)
			require.NoError(t, replayer.ReplayWorkflowHistoryFromJSONFile(nil, tc.path))
		})
	}
}

// TestNHLSeasonHistoriesCoverTheirCase pins what each recorded history
// holds, so a re-recorded fixture cannot silently stop covering its case.
func TestNHLSeasonHistoriesCoverTheirCase(t *testing.T) {
	for _, tc := range nhlSeasonHistories() {
		t.Run(tc.path, func(t *testing.T) {
			history := readHistoryFile(t, tc.path)
			assert.Equal(t, tc.activities, scheduledTaskTypes(history))
			assert.Equal(t, tc.versions, versionMarkers(t, history))
			assert.Equal(t, tc.startDaysFirst, savesBeforeFirstActivity(history),
				"StartGroup save before the season activities")
			for _, input := range scheduledInputs[worknhl.FetchDayInput](t, history, "FetchDay") {
				assert.Len(t, input.TeamIDs, tc.teamIDs, "FetchDay team IDs")
			}
			for _, input := range scheduledInputs[worknhl.ImportDayIfChangedInput](t, history, "ImportDayIfChanged") {
				assert.Len(t, input.TeamIDs, tc.teamIDs, "ImportDayIfChanged team IDs")
				assert.Equal(t, tc.force, input.Force, "ImportDayIfChanged force")
			}
		})
	}
}

// TestNHLSeasonReplayDetectsMovedStartGroup is the negative control of the
// recorded histories: each one fails replay against a runner whose day
// group starts on the other side of the season activities, so the
// histories do detect the placement they exist to protect.
func TestNHLSeasonReplayDetectsMovedStartGroup(t *testing.T) {
	for _, tc := range []struct {
		path     string
		name     string
		workflow any
	}{
		{nhlSeasonFetchHistory, "FetchNHLSeasonWorkflow",
			func(ctx workflow.Context, season nhl.SeasonInfo) (core.OriginCounts, error) {
				mode := fetchSeasonDaysMode()
				mode.startDaysFirst = false
				return runSeasonDays(ctx, season, mode)
			}},
		{nhlSeasonImportHistory, "ImportNHLSeasonWorkflow",
			func(ctx workflow.Context, season nhl.SeasonInfo, opts ImportNHLSeasonOptions) (core.OriginCounts, error) {
				mode := importSeasonDaysMode(opts)
				mode.startDaysFirst = true
				return runSeasonDays(ctx, season, mode)
			}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			replayer := worker.NewWorkflowReplayer()
			replayer.RegisterWorkflowWithOptions(tc.workflow, workflow.RegisterOptions{Name: tc.name})
			err := replayer.ReplayWorkflowHistoryFromJSONFile(nil, tc.path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), nondeterminismErrorCode)
		})
	}
}

// savesBeforeFirstActivity reports whether a LocalActivity marker (a
// progress save) precedes the first scheduled activity.
func savesBeforeFirstActivity(history *historypb.History) bool {
	for _, event := range history.Events {
		if event.GetActivityTaskScheduledEventAttributes() != nil {
			return false
		}
		if event.GetMarkerRecordedEventAttributes().GetMarkerName() == localActivityMarkerName {
			return true
		}
	}
	return false
}

// scheduledInputs decodes the single input of every activity of the given
// type a history scheduled, in order.
func scheduledInputs[T any](t *testing.T, history *historypb.History, activityType string) []T {
	t.Helper()
	dc := converter.GetDefaultDataConverter()
	var inputs []T
	for _, event := range history.Events {
		attrs := event.GetActivityTaskScheduledEventAttributes()
		if attrs.GetActivityType().GetName() != activityType {
			continue
		}
		var input T
		require.NoError(t, dc.FromPayloads(attrs.GetInput(), &input))
		inputs = append(inputs, input)
	}
	return inputs
}

// Recorded playoff results are what the replayed fetch decodes: they carry
// no origins, which decodeFetchPlayoffs must read as none.
func TestNHLSeasonFetchHistoryPlayoffResultsHaveNoOrigins(t *testing.T) {
	history := readHistoryFile(t, nhlSeasonFetchHistory)
	playoffEvents := map[int64]bool{}
	for _, event := range history.Events {
		if event.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName() == "FetchTeamPlayoffGames" {
			playoffEvents[event.GetEventId()] = true
		}
	}
	dc := converter.GetDefaultDataConverter()
	var results int
	for _, event := range history.Events {
		attrs := event.GetActivityTaskCompletedEventAttributes()
		if !playoffEvents[attrs.GetScheduledEventId()] {
			continue
		}
		var raw map[string]json.RawMessage
		require.NoError(t, dc.FromPayloads(attrs.GetResult(), &raw))
		assert.Contains(t, raw, "gamesFetched")
		assert.NotContains(t, raw, "origins")
		results++
	}
	assert.Equal(t, nhlSeasonHistoryPlayoffTeams, results)
}
