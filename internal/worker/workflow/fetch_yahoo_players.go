package workflow

import "go.temporal.io/sdk/workflow"

const WorkflowIDFetchYahooPlayers = "fetch-yahoo-players"

// Retain the version marker for game-list workflow histories recorded before
// the scan branch was removed.
const (
	fetchYahooPlayersGameListsChange  = "fetch-yahoo-players-game-lists"
	fetchYahooPlayersGameListsVersion = 1
)

// GroupFetchYahooPlayers is the only progress group of
// FetchYahooPlayersWorkflow.
const GroupFetchYahooPlayers = 0

// FetchYahooPlayersInput preserves the workflow's recorded input shape.
// Game list downloads do not use it.
type FetchYahooPlayersInput struct{}

// FetchYahooPlayersWorkflow downloads the game player list of each NHL
// season whose list is due (see fetchYahooGamePlayerLists).
func FetchYahooPlayersWorkflow(ctx workflow.Context, _ *FetchYahooPlayersInput) error {
	workflow.GetVersion(ctx, fetchYahooPlayersGameListsChange, workflow.DefaultVersion,
		fetchYahooPlayersGameListsVersion)
	return fetchYahooGamePlayerLists(ctx)
}
