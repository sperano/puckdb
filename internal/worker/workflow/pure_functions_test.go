package workflow

import (
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
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

// --- WorkflowID helpers (the format-only variants — Edge/Season/etc.) ---

func TestWorkflowIDFormatters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"FetchSeason", WorkflowIDFetchSeason(2023), "fetch-season-2023"},
		{"FetchEdge", WorkflowIDFetchEdge(2024), "fetch-edge-2024"},
		{"ImportSeason", WorkflowIDImportSeason(2019), "import-season-2019"},
		{"ImportEdge", WorkflowIDImportEdge(2025), "import-edge-2025"},
		{"ImportSeasonPlayerLogs", WorkflowIDImportSeasonPlayerLogs(2024), "import-season-player-logs-2024"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.got)
		})
	}
}

// --- Edge counters ---

// countEdgeActivities and countEdgeImportActivities ignore both their
// arguments. They return the constant 192 (32 teams × 3 activities × 2
// game types). Pin the value — if someone bumps an iota or adds a game
// type without updating the arithmetic, this test trips.
const expectedEdgeActivityTotal = 192

func TestCountEdgeActivities(t *testing.T) {
	t.Parallel()

	got, err := countEdgeActivities(nil, nhl.SeasonInfo{})
	require.NoError(t, err)
	assert.Equal(t, expectedEdgeActivityTotal, got)
}

func TestCountEdgeImportActivities(t *testing.T) {
	t.Parallel()

	got, err := countEdgeImportActivities(nil, nhl.SeasonInfo{})
	require.NoError(t, err)
	assert.Equal(t, expectedEdgeActivityTotal, got)
}

// --- filterEdgeSeasons ---

// filterEdgeSeasons returns only seasons whose ID >= MinEdgeStatsSeasonID
// (20212022). Boundary cases must be pinned: 2020-2021 (just before)
// excluded, 2021-2022 (the boundary) included.
func TestFilterEdgeSeasons(t *testing.T) {
	t.Parallel()

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2019)}, // 20192020 - before
		{ID: nhl.NewSeason(2020)}, // 20202021 - just before, excluded
		{ID: nhl.NewSeason(2021)}, // 20212022 - boundary, included
		{ID: nhl.NewSeason(2022)}, // 20222023 - included
		{ID: nhl.NewSeason(2024)}, // 20242025 - included
	}

	got := filterEdgeSeasons(seasons)

	require.Len(t, got, 3)
	assert.Equal(t, 2021, got[0].ID.StartYear())
	assert.Equal(t, 2022, got[1].ID.StartYear())
	assert.Equal(t, 2024, got[2].ID.StartYear())
}

func TestFilterEdgeSeasons_AllExcluded(t *testing.T) {
	t.Parallel()

	seasons := []nhl.SeasonInfo{
		{ID: nhl.NewSeason(2018)},
		{ID: nhl.NewSeason(2019)},
	}
	assert.Empty(t, filterEdgeSeasons(seasons))
}

func TestFilterEdgeSeasons_Empty(t *testing.T) {
	t.Parallel()
	assert.Empty(t, filterEdgeSeasons(nil))
}

// --- ProgressReport constructors (the seven still uncovered) ---

// Each report shape pins a contract: the number of groups, the headers
// (first word matters — UI surface), and where applicable the bar
// totals. Headers are matched by Contains rather than Equal so that
// punctuation tweaks don't trip these tests, but the meaningful word
// stays pinned.

func TestNewFetchPlayerLandingsProgressReport(t *testing.T) {
	t.Parallel()

	const total = 137
	r := NewFetchPlayerLandingsProgressReport(total)

	require.NotNil(t, r)
	assert.Equal(t, total, r.Total)
	require.Len(t, r.Groups, 1)
	assert.Contains(t, r.Groups[0].Header, "landing")
	require.Len(t, r.Groups[0].Bars, 1)
	assert.Equal(t, total, r.Groups[0].Bars[0].Total)
	assert.Equal(t, "Players", r.Groups[0].Bars[0].Label)
}

func TestNewFetchSeasonsProgressReport(t *testing.T) {
	t.Parallel()

	r := NewFetchSeasonsProgressReport()

	require.NotNil(t, r)
	require.Len(t, r.Groups, 1)
	assert.Contains(t, r.Groups[0].Header, "Fetching")
	// Bars are added later as seasons are discovered — the constructor
	// returns an empty slice, and downstream code appends to it.
	assert.Empty(t, r.Groups[0].Bars)
}

func TestNewImportSeasonsProgressReport(t *testing.T) {
	t.Parallel()

	r := NewImportSeasonsProgressReport()

	require.NotNil(t, r)
	require.Len(t, r.Groups, 1)
	assert.Contains(t, r.Groups[0].Header, "Importing")
	assert.Empty(t, r.Groups[0].Bars)
}

func TestNewFetchYahooPlayersProgressReport(t *testing.T) {
	t.Parallel()

	const total = 80000
	const completed = 12345
	r := NewFetchYahooPlayersProgressReport(total, completed)

	require.NotNil(t, r)
	assert.Equal(t, total, r.Total)
	assert.Equal(t, completed, r.Completed)
	require.Len(t, r.Groups, 1)
	require.Len(t, r.Groups[0].Bars, 1)
	assert.Equal(t, total, r.Groups[0].Bars[0].Total)
	assert.Equal(t, completed, r.Groups[0].Bars[0].Current)
}

func TestNewFetchEdgeProgressReport(t *testing.T) {
	t.Parallel()

	season := nhl.SeasonInfo{ID: nhl.NewSeason(2023)}
	r := NewFetchEdgeProgressReport(season)

	require.NotNil(t, r)
	assert.Equal(t, expectedEdgeActivityTotal, r.Total)
	require.Len(t, r.Groups, 1)
	assert.Contains(t, r.Groups[0].Header, "2023-24")
	require.Len(t, r.Groups[0].Bars, 1)
	assert.Equal(t, expectedEdgeActivityTotal, r.Groups[0].Bars[0].Total)
}

func TestNewImportEdgeProgressReport(t *testing.T) {
	t.Parallel()

	season := nhl.SeasonInfo{ID: nhl.NewSeason(2023)}
	r := NewImportEdgeProgressReport(season)

	require.NotNil(t, r)
	assert.Equal(t, expectedEdgeActivityTotal, r.Total)
	require.Len(t, r.Groups, 1)
	assert.Contains(t, r.Groups[0].Header, "Importing Edge")
	assert.Contains(t, r.Groups[0].Header, "2023-24")
}

func TestNewImportSeasonPlayerLogsReport(t *testing.T) {
	t.Parallel()

	const playerCount = 250
	season := nhl.SeasonInfo{ID: nhl.NewSeason(2024)}
	r := NewImportSeasonPlayerLogsReport(season, playerCount)

	require.NotNil(t, r)
	assert.Equal(t, playerCount, r.Total)
	require.Len(t, r.Groups, 1)
	assert.Contains(t, r.Groups[0].Header, "2024-25")
	require.Len(t, r.Groups[0].Bars, 1)
	assert.Equal(t, playerCount, r.Groups[0].Bars[0].Total)
}

func TestNewInitializeProgressReport(t *testing.T) {
	t.Parallel()

	r := NewInitializeProgressReport()

	require.NotNil(t, r)
	// Five sequential phases: fetch franchises, upsert franchises, fetch
	// seasons, upsert seasons, upsert season teams.
	require.Len(t, r.Groups, 5)
	for i, g := range r.Groups {
		assert.NotEmpty(t, g.Header, "group %d should have a header", i)
		require.Len(t, g.Bars, 1, "group %d should have one bar", i)
	}
}

func TestNewImportPlayerLogsProgressReport(t *testing.T) {
	t.Parallel()

	r := NewImportPlayerLogsProgressReport()

	require.NotNil(t, r)
	require.Len(t, r.Groups, 1)
	assert.Contains(t, r.Groups[0].Header, "Importing")
	assert.Empty(t, r.Groups[0].Bars)
}

func TestNewProcessPlayersProgressReport(t *testing.T) {
	t.Parallel()

	const totalPlayers = 9876
	r := NewProcessPlayersProgressReport(totalPlayers)

	require.NotNil(t, r)
	// Three phases: load Yahoo pool, process players, verify unmatched.
	require.Len(t, r.Groups, 3)

	// The middle group (Processing players) carries the totalPlayers bar.
	require.Len(t, r.Groups[1].Bars, 1)
	assert.Equal(t, totalPlayers, r.Groups[1].Bars[0].Total)
	assert.Equal(t, "Players", r.Groups[1].Bars[0].Label)
}

func TestNewFetchPlayerLogsProgressReport(t *testing.T) {
	t.Parallel()

	r := NewFetchPlayerLogsProgressReport()

	require.NotNil(t, r)
	require.Len(t, r.Groups, 1)
	assert.Contains(t, r.Groups[0].Header, "Fetching")
	assert.Empty(t, r.Groups[0].Bars)
}
