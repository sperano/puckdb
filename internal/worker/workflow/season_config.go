package workflow

import (
	"fmt"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"go.temporal.io/sdk/workflow"
)

// seasonConfig is the configuration FetchNHLSeasonWorkflow and ImportNHLSeasonWorkflow
// snapshot at start: both the Yahoo leagues to process and the day concurrency
// decide which activities get scheduled, so neither may be re-read on replay.
type seasonConfig struct {
	Yahoo          shared.YahooSeasonsSnapshot `json:"yahoo"`
	DayConcurrency int                         `json:"dayConcurrency"`
}

// loadYahooSeasons is the Yahoo seasons loader used by loadSeasonConfig. It is
// a variable so replay tests can swap the configuration between recording and
// replaying a history.
var loadYahooSeasons = shared.LoadYahooSeasonsSnapshot

func loadSeasonConfig() seasonConfig {
	return seasonConfig{
		Yahoo:          loadYahooSeasons(),
		DayConcurrency: shared.ResolveConfigInt(nil, shared.DayConcurrencyParam, nil),
	}
}

// snapshotSeasonConfig records the season configuration in history and fails
// the workflow when the Yahoo seasons file is set but unreadable or malformed.
func snapshotSeasonConfig(ctx workflow.Context) (seasonConfig, error) {
	cfg, err := shared.SnapshotConfig(ctx, loadSeasonConfig)
	if err != nil {
		return cfg, err
	}
	if err := cfg.Yahoo.LoadError(); err != nil {
		return cfg, fmt.Errorf("load yahoo seasons config: %w", err)
	}
	return cfg, nil
}

// yahooTeamIDsForSeason returns the Yahoo team IDs to import/fetch per-day
// data for, drawn from the season's snapshotted Yahoo config. Stand-in
// leagues (UsesTemporaryMetadata) make no Yahoo API calls and contribute no
// teams.
//
// This performs no activity calls: league/team metadata itself is imported
// independently by ImportYahooSeasonWorkflow (or fetched by
// FetchYahooSeasonWorkflow) in the parent season-sync workflow, which must
// complete before the season's day loop starts — per-day roster rows FK to
// yahoo_teams(league_id, id).
func yahooTeamIDsForSeason(season config.Season) []yahoo.TeamInfo {
	var teamIDs []yahoo.TeamInfo
	for _, league := range season.Leagues {
		if league.UsesTemporaryMetadata() {
			continue
		}
		for _, teamID := range league.TeamIDs {
			teamIDs = append(teamIDs, yahoo.TeamInfo{LeagueID: league.LeagueID, TeamID: teamID})
		}
	}
	return teamIDs
}
