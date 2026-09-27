package workflow

// TEMPORARY: stand-in league metadata while Yahoo answers 403 for the 2026
// leagues. See config.LeagueMetadataSource for the full story and the removal
// steps; delete this file together with that type.
//
// The stand-in branches are chosen from the snapshotted Yahoo config, so
// histories recorded without temporary_metadata_from replay unchanged.

import (
	"fmt"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"go.temporal.io/sdk/workflow"
)

// fetchStandInLeagueSource makes sure the source league's settings are cached.
// It only touches the source season, which is normally a cache hit, and never
// calls Yahoo for the stand-in league itself.
func fetchStandInLeagueSource(ctx workflow.Context, league config.League) error {
	source := league.TemporaryMetadataFrom
	workflow.GetLogger(ctx).Warn("Using TEMPORARY stand-in Yahoo league settings",
		"leagueID", league.LeagueID, "sourceSeason", source.Season, "sourceLeagueID", source.LeagueID)
	var act *yahoo.FetchActivities
	if err := workflow.ExecuteActivity(ctx, act.FetchLeague, source.Season, source.LeagueID).Get(ctx, nil); err != nil {
		return fmt.Errorf("fetch stand-in source league %d/%d for league %d: %w",
			source.Season, source.LeagueID, league.LeagueID, err)
	}
	return nil
}

// importStandInLeague imports the source league's settings under the league's
// own ID and season.
func importStandInLeague(ctx workflow.Context, startYear int, league config.League) error {
	input := yahoo.ImportYahooStandInLeagueInput{
		Season: startYear, LeagueID: league.LeagueID, Source: *league.TemporaryMetadataFrom,
	}
	var act *yahoo.ImportActivities
	if err := workflow.ExecuteActivity(ctx, act.ImportYahooStandInLeague, input).Get(ctx, nil); err != nil {
		return fmt.Errorf("import stand-in league %d from %d/%d: %w",
			league.LeagueID, input.Source.Season, input.Source.LeagueID, err)
	}
	return nil
}

// standInLeagueNote is the progress entry that tells the operator the league
// runs on stand-in settings and which Yahoo data it is missing.
func standInLeagueNote(league config.League) string {
	source := league.TemporaryMetadataFrom
	return fmt.Sprintf("league %d (using TEMPORARY stand-in settings from %d league %d)",
		league.LeagueID, source.Season, source.LeagueID)
}
