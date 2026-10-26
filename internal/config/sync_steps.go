package config

import (
	"fmt"
	"slices"
	"strings"
)

// Sync step names — each maps to a single workflow.
const (
	StepInit                   = "init"
	StepYahooPlayers           = "yahoo-players"
	StepFetchSeasons           = "fetch-seasons"
	StepExtractBoxscorePlayers = "extract-boxscore-players"
	StepFetchPlayerLandings    = "fetch-player-landings"
	StepFetchPlayerLogs        = "fetch-player-logs"
	StepProcessPlayers         = "process-players"
	StepImportSeasons          = "import-seasons"
	StepImportPlayerLogs       = "import-player-logs"
	StepFetchEdgeStats         = "fetch-edge-stats"
	StepImportEdgeStats        = "import-edge-stats"
	StepFetchAssets            = "fetch-assets"
	StepRefreshNews            = "refresh-news"
	StepRefreshDraftRankings   = "refresh-draft-rankings"
)

// allSyncSteps lists every atomic step in execution order. News refresh runs
// after the imports because it resolves players against the imported players
// and Yahoo pools; the draft ranking refresh follows it because rankings are
// adjusted by the news it extracted. Asset fetch runs
// last because every asset URL is sourced from a row populated by an earlier
// step (player headshots from players, team logos from season_teams, Yahoo
// images from yahoo_* tables).
var allSyncSteps = []string{
	StepInit, StepYahooPlayers, StepFetchSeasons,
	StepExtractBoxscorePlayers, StepFetchPlayerLandings, StepFetchPlayerLogs,
	StepProcessPlayers, StepImportSeasons, StepImportPlayerLogs,
	StepFetchEdgeStats, StepImportEdgeStats,
	StepRefreshNews,
	StepRefreshDraftRankings,
	StepFetchAssets,
}

// AllSyncSteps returns every atomic step in execution order, as a copy the
// caller may modify.
func AllSyncSteps() []string {
	return slices.Clone(allSyncSteps)
}

// SyncStepGroups maps shortcut names to the steps they expand to.
var SyncStepGroups = map[string][]string{
	"seasons": {StepFetchSeasons, StepImportSeasons},
	"players": {
		StepYahooPlayers, StepExtractBoxscorePlayers, StepFetchPlayerLandings,
		StepFetchPlayerLogs, StepProcessPlayers, StepImportPlayerLogs,
	},
	"edge":  {StepFetchEdgeStats, StepImportEdgeStats},
	"draft": {StepRefreshNews, StepRefreshDraftRankings},
}

// ParseSyncSteps resolves CLI arguments into a set of enabled step names.
// An empty args slice enables all steps (default behavior).
// Group shortcuts are expanded. Unknown names produce an error.
func ParseSyncSteps(args []string) (map[string]bool, error) {
	if len(args) == 0 {
		enabled := make(map[string]bool, len(allSyncSteps))
		for _, s := range allSyncSteps {
			enabled[s] = true
		}
		return enabled, nil
	}

	enabled := make(map[string]bool)
	var unknown []string

	for _, arg := range args {
		if group, ok := SyncStepGroups[arg]; ok {
			for _, s := range group {
				enabled[s] = true
			}
			continue
		}
		if slices.Contains(allSyncSteps, arg) {
			enabled[arg] = true
			continue
		}
		unknown = append(unknown, arg)
	}

	if len(unknown) > 0 {
		valid := validSyncStepNames(allSyncSteps, SyncStepGroups)
		return nil, fmt.Errorf("unknown sync step(s): %s\nvalid steps: %s",
			strings.Join(unknown, ", "), strings.Join(valid, ", "))
	}

	return enabled, nil
}

// validSyncStepNames lists steps followed by the sorted group names, in a new
// slice: appending to steps could write into its spare capacity.
func validSyncStepNames(steps []string, groups map[string][]string) []string {
	return slices.Concat(steps, sortedKeys(groups))
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
