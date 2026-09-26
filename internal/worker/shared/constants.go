package shared

import "fmt"

const (
	WorkflowIDImportSeasons    = "import-seasons"
	WorkflowIDImportPlayerLogs = "import-player-logs"
	WorkflowIDFetchSeasons     = "fetch-seasons"
	WorkflowIDFetchPlayerLogs  = "fetch-player-logs"
	WorkflowIDFetchEdgeStats   = "fetch-edge-stats"
	WorkflowIDImportEdgeStats  = "import-edge-stats"

	// WorkflowIDFetchAssets is the stable workflow ID for the parent asset fetch.
	WorkflowIDFetchAssets = "fetch-assets"

	// WorkflowIDRefreshNews is the stable workflow ID of an on-demand player
	// news refresh; scheduled refreshes use ScheduleIDRefreshNews.
	WorkflowIDRefreshNews = "refresh-news"
	// ScheduleIDRefreshNews is the Temporal schedule that starts periodic
	// player news refreshes.
	ScheduleIDRefreshNews = "refresh-news-schedule"

	// WorkflowIDRefreshDraftRankings is the stable workflow ID of the draft
	// ranking refresh. Starting it while it runs is rejected, which is how
	// concurrent refreshes are prevented.
	WorkflowIDRefreshDraftRankings = "refresh-draft-rankings"

	// TaskQueueAssets is the Temporal task queue for asset download activities.
	// It is separate from the main puckdb-tasks queue so asset workloads cannot
	// starve other workflows (architectural delta 4).
	TaskQueueAssets = "puckdb-asset-tasks"
)

// WorkflowIDFetchAssetsClass returns the stable workflow ID for a single asset
// class child workflow. The class label is embedded so the Temporal UI shows
// distinct workflow types without parameterisation.
func WorkflowIDFetchAssetsClass(class string) string {
	return fmt.Sprintf("fetch-assets-%s", class)
}
