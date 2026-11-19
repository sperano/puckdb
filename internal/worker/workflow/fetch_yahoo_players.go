package workflow

import (
	"time"

	"go.temporal.io/sdk/workflow"
)

const WorkflowIDFetchYahooPlayers = "fetch-yahoo-players"

// fetchYahooPlayersGameListsChange versions the game player list download.
// An execution recorded before it carries no marker and replays the player
// ID scan (fetch_yahoo_players_legacy.go).
const (
	fetchYahooPlayersGameListsChange  = "fetch-yahoo-players-game-lists"
	fetchYahooPlayersGameListsVersion = 1
)

// GroupFetchYahooPlayers is the only progress group of
// FetchYahooPlayersWorkflow.
const GroupFetchYahooPlayers = 0

// FetchYahooPlayersInput is the input of the retired player ID scan, which
// continued as new with its position and totals. The game player list
// download takes no input (GraphQL passes nil) and ignores one a scan
// execution continued into it with.
type FetchYahooPlayersInput struct {
	StartPlayerID   int       // First player ID to process in this execution (1-based)
	TotalCompleted  int       // Cumulative completed count from previous executions
	TotalFound      int       // Cumulative found players (network downloads + cache hits, excludes 404s)
	TotalDownloaded int       // Cumulative pages downloaded from Yahoo (excludes cache hits)
	TotalMissing    int       // Cumulative ids without a page (fresh 404s + still-valid missing markers)
	StartedAt       time.Time // Original workflow start time (for elapsed calculation)

	// Config is the scan's settings, snapshotted on the first execution of a
	// run and carried across every ContinueAsNew; nil on the first execution
	// and on inputs produced before this field existed.
	Config *FetchYahooPlayersScanConfig
}

// FetchYahooPlayersWorkflow downloads the player list of every NHL game
// Yahoo lists, one per season (see fetchYahooGamePlayerLists). Executions
// recorded before the lists replay the player ID scan they ran.
func FetchYahooPlayersWorkflow(ctx workflow.Context, input *FetchYahooPlayersInput) error {
	version := workflow.GetVersion(ctx, fetchYahooPlayersGameListsChange, workflow.DefaultVersion,
		fetchYahooPlayersGameListsVersion)
	if version == workflow.DefaultVersion {
		return fetchYahooPlayersScan(ctx, input)
	}
	return fetchYahooGamePlayerLists(ctx)
}
