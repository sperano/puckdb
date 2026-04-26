package workflow

import (
	"testing"

	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- WorkflowIDExtractSeason ---

func TestWorkflowIDExtractSeason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		startYear int
		expected  string
	}{
		{2023, "extract-season-2023"},
		{2024, "extract-season-2024"},
		{2000, "extract-season-2000"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, WorkflowIDExtractSeason(tt.startYear))
		})
	}
}

// --- WorkflowIDFetchSeasonPlayerLogs ---

func TestWorkflowIDFetchSeasonPlayerLogs(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "fetch-season-player-logs-2024", WorkflowIDFetchSeasonPlayerLogs(2024))
	assert.Equal(t, "fetch-season-player-logs-2019", WorkflowIDFetchSeasonPlayerLogs(2019))
}

// --- NewExtractBoxscorePlayersProgressReport ---

func TestNewExtractBoxscorePlayersProgressReport(t *testing.T) {
	t.Parallel()

	report := NewExtractBoxscorePlayersProgressReport()

	require.NotNil(t, report)
	require.Len(t, report.Groups, 2)

	extractGroup := report.Groups[GroupExtractBoxscorePlayers]
	assert.Contains(t, extractGroup.Header, "Extracting")
	assert.Empty(t, extractGroup.Bars, "bars are added dynamically after seasons are known")

	consolidateGroup := report.Groups[GroupConsolidatePlayers]
	assert.Contains(t, consolidateGroup.Header, "Consolidating")
	require.Len(t, consolidateGroup.Bars, 1)
	assert.Equal(t, 1, consolidateGroup.Bars[0].Total)
	assert.Equal(t, "Merging", consolidateGroup.Bars[0].Label)
}

// --- extractPlayerIDs ---

func TestExtractPlayerIDs_Empty(t *testing.T) {
	t.Parallel()

	ids := extractPlayerIDs(nil)

	assert.Empty(t, ids)
}

func TestExtractPlayerIDs_PreservesOrder(t *testing.T) {
	t.Parallel()

	players := []store.BoxscorePlayer{
		{ID: 100},
		{ID: 200},
		{ID: 300},
	}

	ids := extractPlayerIDs(players)

	require.Len(t, ids, 3)
	assert.Equal(t, int64(100), ids[0])
	assert.Equal(t, int64(200), ids[1])
	assert.Equal(t, int64(300), ids[2])
}

func TestExtractPlayerIDs_LengthMatchesInput(t *testing.T) {
	t.Parallel()

	players := make([]store.BoxscorePlayer, 50)
	for i := range players {
		players[i].ID = int64(i + 1)
	}

	ids := extractPlayerIDs(players)

	assert.Len(t, ids, 50)
	for i, id := range ids {
		assert.Equal(t, int64(i+1), id)
	}
}

// --- NewFetchSeasonPlayerLogsReport ---

func TestNewFetchSeasonPlayerLogsReport(t *testing.T) {
	t.Parallel()

	report := NewFetchSeasonPlayerLogsReport(42)

	require.NotNil(t, report)
	assert.Equal(t, 42, report.Total)
	require.Len(t, report.Groups, 1)

	group := report.Groups[GroupFetchSeasonPlayerLogs]
	assert.Contains(t, group.Header, "Fetching")
	require.Len(t, group.Bars, 1)
	assert.Equal(t, 42, group.Bars[0].Total)
	assert.Equal(t, "Players", group.Bars[0].Label)
}

func TestNewFetchSeasonPlayerLogsReport_ZeroPlayers(t *testing.T) {
	t.Parallel()

	report := NewFetchSeasonPlayerLogsReport(0)

	require.NotNil(t, report)
	assert.Equal(t, 0, report.Total)
	assert.Equal(t, 0, report.Groups[0].Bars[0].Total)
}

// --- shared.NewNHLClient (basic construction, no network) ---

func TestNewNHLClient_NotNil(t *testing.T) {
	t.Parallel()

	client := shared.NewNHLClient()

	assert.NotNil(t, client)
}

// --- processPlayersPhase.String ---

func TestProcessPlayersPhase_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		phase processPlayersPhase
		want  string
	}{
		{"loadYahoo", phaseLoadYahoo, "loadYahoo"},
		{"processPlayers", phaseProcessPlayers, "processPlayers"},
		{"verifyUnmatched", phaseVerifyUnmatched, "verifyUnmatched"},
		{"zero value falls through to unknown", processPlayersPhase(0), "unknown(0)"},
		{"out-of-range falls through to unknown", processPlayersPhase(99), "unknown(99)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.phase.String())
		})
	}
}
