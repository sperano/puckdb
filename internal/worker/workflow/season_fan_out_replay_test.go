package workflow

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
)

// seasonFanOutThreeSeasonsHistory is the one history with more seasons than
// its concurrency.
const seasonFanOutThreeSeasonsHistory = "testdata/season_fan_out_fetch_player_logs_three_seasons.json"

// Version marker detail keys, as the SDK records them.
const (
	versionMarkerName       = "Version"
	localActivityMarkerName = "LocalActivity"
	versionMarkerChangeKey  = "change-id"
	versionMarkerValueKey   = "version"
)

// seasonFanOutHistory is one parent history recorded before the season
// fan-out runner, with what it must contain: the tasks it scheduled after the
// manifest (child workflows and activities, by type, in order), the version
// markers it recorded and whether it touched saved progress (stale report
// cleanup and saves are local activities), which an empty selection must not.
type seasonFanOutHistory struct {
	path          string
	workflow      any
	commands      []string
	versions      map[string]int
	savesProgress bool
}

// seasonFanOutHistories were recorded on a Temporal dev server with the
// parents of commit 9fcda0f, sticky execution disabled, with stub activities
// and child workflows registered under their production names:
// FetchSeasonsManifest returns the seasons below; Save and DeleteBatch
// return nil; LoadSeasonBoxscorePlayers and CollectSeasonSkaters return
// startYear-2020 players (CollectSeasonSkaters also reports startYear-2022
// missing boxscores); ExtractAndSaveBoxscorePlayers returns startYear-2020
// FileSystem reads; ConsolidateBoxscorePlayersActivity returns 5 total and 4
// unique players; FetchEdgeWorkflow and ImportEdgeWorkflow return 192
// RemoteNHLAPI and FileSystem reads; ImportSeasonPlayerLogsV2Workflow
// returns one FileSystem read per player; FetchSeasonPlayerLogsWorkflow
// returns nil.
//
// The non-empty histories run seasons 2022-2023 and 2023-2024 with input
// {seasonConcurrency: 2, refreshCurrentEdge: true, refreshCurrentPlayerLogs:
// true} and no range, so only the last season's child refreshes. The empty
// manifest history runs ExtractBoxscorePlayersWorkflow with an omitted (nil)
// input; the Edge history without an eligible season lists 2019-2020 and
// 2020-2021 with the non-empty input. The three-season player-log fetch
// history adds 2024-2025 (standings 2024-10-04 to 2024-10-06) to the
// non-empty run, so its third child waits for a free concurrency slot.
func seasonFanOutHistories() []seasonFanOutHistory {
	configSnapshot := map[string]int{"config-snapshot": 1}
	return []seasonFanOutHistory{
		{"testdata/season_fan_out_fetch_player_logs.json", FetchPlayerLogsWorkflow,
			[]string{"LoadSeasonBoxscorePlayers", "LoadSeasonBoxscorePlayers",
				"FetchSeasonPlayerLogsWorkflow", "FetchSeasonPlayerLogsWorkflow"},
			configSnapshot, true},
		{seasonFanOutThreeSeasonsHistory, FetchPlayerLogsWorkflow,
			[]string{"LoadSeasonBoxscorePlayers", "LoadSeasonBoxscorePlayers", "LoadSeasonBoxscorePlayers",
				"FetchSeasonPlayerLogsWorkflow", "FetchSeasonPlayerLogsWorkflow", "FetchSeasonPlayerLogsWorkflow"},
			configSnapshot, true},
		{"testdata/season_fan_out_fetch_edge.json", FetchEdgeSeasonsWorkflow,
			[]string{"FetchEdgeWorkflow", "FetchEdgeWorkflow"}, configSnapshot, true},
		{"testdata/season_fan_out_import_edge.json", ImportEdgeSeasonsWorkflow,
			[]string{"ImportEdgeWorkflow", "ImportEdgeWorkflow"}, configSnapshot, true},
		{"testdata/season_fan_out_import_player_logs.json", ImportPlayerLogsWorkflow,
			[]string{"CollectSeasonSkaters", "CollectSeasonSkaters",
				"ImportSeasonPlayerLogsV2Workflow", "ImportSeasonPlayerLogsV2Workflow"},
			map[string]int{"config-snapshot": 1, importPlayerLogsManifestChangeID: importPlayerLogsManifestVersion}, true},
		{"testdata/season_fan_out_extract_boxscore_players.json", ExtractBoxscorePlayersWorkflow,
			[]string{"ExtractAndSaveBoxscorePlayers", "ExtractAndSaveBoxscorePlayers",
				"ConsolidateBoxscorePlayersActivity"},
			configSnapshot, true},
		{"testdata/season_fan_out_extract_boxscore_players_empty_manifest.json", ExtractBoxscorePlayersWorkflow,
			[]string{}, configSnapshot, false},
		{"testdata/season_fan_out_fetch_edge_no_eligible_season.json", FetchEdgeSeasonsWorkflow,
			[]string{}, configSnapshot, false},
	}
}

// TestSeasonFanOutParentsReplayRecordedHistories replays every recorded
// parent history with the current code: a changed command sequence (order,
// type) fails here with a nondeterminism error. Child and activity inputs
// are not compared; TestSeasonFanOutWorkflowsInput guards them.
func TestSeasonFanOutParentsReplayRecordedHistories(t *testing.T) {
	for _, tc := range seasonFanOutHistories() {
		t.Run(tc.path, func(t *testing.T) {
			replayer := worker.NewWorkflowReplayer()
			replayer.RegisterWorkflow(tc.workflow)
			require.NoError(t, replayer.ReplayWorkflowHistoryFromJSONFile(nil, tc.path))
		})
	}
}

// TestSeasonFanOutHistoriesCoverTheirCase pins what each recorded history
// holds, so a re-recorded fixture cannot silently stop covering its case:
// the empty ones stop after the manifest, the player-log import records the
// skater-manifest version 1.
func TestSeasonFanOutHistoriesCoverTheirCase(t *testing.T) {
	for _, tc := range seasonFanOutHistories() {
		t.Run(tc.path, func(t *testing.T) {
			history := readHistoryFile(t, tc.path)
			commands := scheduledTaskTypes(history)
			require.NotEmpty(t, commands)
			assert.Equal(t, "FetchSeasonsManifest", commands[0], "the manifest is the first task")
			assert.Equal(t, tc.commands, commands[1:], "tasks after the manifest")
			assert.Equal(t, tc.versions, versionMarkers(t, history))
			assert.Equal(t, tc.savesProgress, hasMarker(history, localActivityMarkerName), "progress local activities")
		})
	}
}

// The three-season history runs at the recorded concurrency of 2: the
// first two children start together and the third only after one of them
// completed, so a parent that ignores the snapshotted concurrency (any other
// value) dispatches a different number of children per task and fails replay.
func TestSeasonFanOutThreeSeasonsHistoryHoldsBackTheThirdChild(t *testing.T) {
	history := readHistoryFile(t, seasonFanOutThreeSeasonsHistory)
	var started []int
	firstCompleted := -1
	for i, event := range history.Events {
		if event.GetStartChildWorkflowExecutionInitiatedEventAttributes() != nil {
			started = append(started, i)
		}
		if event.GetChildWorkflowExecutionCompletedEventAttributes() != nil && firstCompleted < 0 {
			firstCompleted = i
		}
	}
	require.Len(t, started, 3)
	assert.Less(t, started[1], firstCompleted, "the first two children start before any completes")
	assert.Greater(t, started[2], firstCompleted, "the third child waits for a completion")
}

func readHistoryFile(t *testing.T, path string) *historypb.History {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	history, err := client.HistoryFromJSON(f, client.HistoryJSONOptions{})
	require.NoError(t, err)
	return history
}

// scheduledTaskTypes lists the activity and child workflow types a history
// scheduled, in order; local activities (progress persistence) are markers
// and are not listed.
func scheduledTaskTypes(history *historypb.History) []string {
	var types []string
	for _, event := range history.Events {
		if attrs := event.GetActivityTaskScheduledEventAttributes(); attrs != nil {
			types = append(types, attrs.GetActivityType().GetName())
		}
		if attrs := event.GetStartChildWorkflowExecutionInitiatedEventAttributes(); attrs != nil {
			types = append(types, attrs.GetWorkflowType().GetName())
		}
	}
	return types
}

func hasMarker(history *historypb.History, name string) bool {
	for _, event := range history.Events {
		if event.GetMarkerRecordedEventAttributes().GetMarkerName() == name {
			return true
		}
	}
	return false
}

// versionMarkers returns the change IDs and versions a history recorded.
func versionMarkers(t *testing.T, history *historypb.History) map[string]int {
	t.Helper()
	dc := converter.GetDefaultDataConverter()
	versions := make(map[string]int)
	for _, event := range history.Events {
		attrs := event.GetMarkerRecordedEventAttributes()
		if attrs.GetMarkerName() != versionMarkerName {
			continue
		}
		var changeID string
		var version int
		details := attrs.GetDetails()
		require.NoError(t, dc.FromPayloads(details[versionMarkerChangeKey], &changeID))
		require.NoError(t, dc.FromPayloads(details[versionMarkerValueKey], &version))
		versions[changeID] = version
	}
	return versions
}
