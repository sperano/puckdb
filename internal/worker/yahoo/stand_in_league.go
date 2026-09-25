package yahoo

// TEMPORARY: everything in this file exists only while Yahoo answers 403
// "This application is not authorized to perform this action" for the 2026
// leagues. See config.LeagueMetadataSource for the full story and the removal
// steps; delete this file together with that type.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

const (
	// standInLeagueKeyPrefix replaces the Yahoo game key in a stand-in row's
	// league_key. The key is unique, so the stand-in cannot reuse the source
	// league's key, and the real game key is behind the same 403.
	standInLeagueKeyPrefix = "temporary-stand-in"
	// standInLeagueNameFormat marks the stand-in in every place the league
	// name is shown: source name, source season, source league ID.
	standInLeagueNameFormat = "%s [TEMPORARY: %d settings of league %d]"
	// invalidStandInErrorType tags stand-in errors that retrying cannot fix.
	invalidStandInErrorType = "InvalidStandInLeague"
)

// ImportYahooStandInLeagueInput names the league to fill and the earlier
// league whose cached settings stand in for it.
type ImportYahooStandInLeagueInput struct {
	Season   int                         `json:"season"`
	LeagueID int                         `json:"leagueId"`
	Source   config.LeagueMetadataSource `json:"source"`
}

// ImportYahooStandInLeague imports the source league's cached settings under
// the target league's ID and season, marked as a temporary stand-in.
func (a *ImportActivities) ImportYahooStandInLeague(ctx context.Context, input ImportYahooStandInLeagueInput) (ImportYahooLeagueResult, error) {
	defer metrics.TrackActivityDuration("ImportYahooStandInLeague")()
	logger := activity.GetLogger(ctx)

	source, fetchedAt, err := a.readStandInSource(ctx, input)
	if err != nil {
		return ImportYahooLeagueResult{}, err
	}
	if err := a.prepareStandInTarget(ctx, input); err != nil {
		return ImportYahooLeagueResult{}, err
	}
	settingsSource := leagueSettingsSource{
		source: draft.SourceTemporaryStandIn, season: input.Source.Season, leagueKey: source.Key, fetchedAt: fetchedAt,
	}
	result, err := a.upsertLeague(ctx, standInLeague(source, input), settingsSource)
	if err != nil {
		return ImportYahooLeagueResult{}, err
	}

	logger.Warn("Imported TEMPORARY stand-in Yahoo league settings",
		"season", input.Season,
		"leagueID", input.LeagueID,
		"sourceSeason", input.Source.Season,
		"sourceLeagueID", input.Source.LeagueID,
		"rosterPositions", result.RosterPositions,
		"statCategories", result.StatCategories)
	return result, nil
}

func (a *ImportActivities) readStandInSource(ctx context.Context, input ImportYahooStandInLeagueInput) (store.League, time.Time, error) {
	if input.Source.Season >= input.Season {
		return store.League{}, time.Time{}, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("stand-in for season %d league %d must come from an earlier season, got %d",
				input.Season, input.LeagueID, input.Source.Season), invalidStandInErrorType, nil)
	}
	res := resource.League{Season: input.Source.Season, LeagueID: input.Source.LeagueID}
	if !a.Storage.Exists(ctx, res.Path()) {
		return store.League{}, time.Time{}, fmt.Errorf("stand-in source league cache is missing for season %d league %d; "+
			"fetch season %d first", input.Source.Season, input.Source.LeagueID, input.Source.Season)
	}
	return readLeagueSettings(ctx, a.Storage, res)
}

// prepareStandInTarget refuses to overwrite real Yahoo settings or another
// season's league, and clears an earlier stand-in's positions and categories
// so a changed source cannot leave stale rows behind.
func (a *ImportActivities) prepareStandInTarget(ctx context.Context, input ImportYahooStandInLeagueInput) error {
	stored, found, err := a.storedLeague(ctx, input.LeagueID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if err := checkLeagueIdentity(stored, found, input.LeagueID, input.Season); err != nil {
		return err
	}
	if !isStandInLeagueKey(stored.LeagueKey) {
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("league %d already has real Yahoo settings (key %s); remove temporary_metadata_from "+
				"from the seasons config", input.LeagueID, stored.LeagueKey), invalidStandInErrorType, nil)
	}
	return a.deleteLeagueSettings(ctx, input.LeagueID)
}

// clearStandInLeagueSettings runs before a real league import: when the stored
// row is a stand-in, it deletes the stand-in's positions and categories so
// ones the real league no longer uses do not survive the upsert.
func (a *ImportActivities) clearStandInLeagueSettings(ctx context.Context, stored sqlcdb.YahooLeague, found bool) error {
	if !found || !isStandInLeagueKey(stored.LeagueKey) {
		return nil
	}
	activity.GetLogger(ctx).Info("Replacing TEMPORARY stand-in Yahoo league settings", "leagueID", stored.ID)
	return a.deleteLeagueSettings(ctx, int(stored.ID))
}

func (a *ImportActivities) deleteLeagueSettings(ctx context.Context, leagueID int) error {
	if err := a.Queries.DeleteYahooLeagueRosterPositions(ctx, int32(leagueID)); err != nil {
		return fmt.Errorf("delete stand-in roster positions for league %d: %w", leagueID, err)
	}
	if err := a.Queries.DeleteYahooLeagueStatCategories(ctx, int32(leagueID)); err != nil {
		return fmt.Errorf("delete stand-in stat categories for league %d: %w", leagueID, err)
	}
	return nil
}

// standInLeague relabels the source league as the target league. Values that
// only describe the source season (dates, draft time and status, URLs, update
// timestamp) are cleared rather than presented as the target season's.
func standInLeague(source store.League, input ImportYahooStandInLeagueInput) store.League {
	league := store.League{
		ID:           input.LeagueID,
		Key:          standInLeagueKey(input.LeagueID),
		Name:         fmt.Sprintf(standInLeagueNameFormat, source.Name, input.Source.Season, input.Source.LeagueID),
		LogoURL:      source.LogoURL,
		NumTeams:     source.NumTeams,
		ScoringType:  source.ScoringType,
		LeagueType:   source.LeagueType,
		IsProLeague:  source.IsProLeague,
		IsCashLeague: source.IsCashLeague,
		GameCode:     source.GameCode,
		Season:       input.Season,
		Settings:     source.Settings,
	}
	league.Settings.DraftTime = 0
	league.Settings.TradeEndDate = ""
	league.Settings.PersistentURL = ""
	return league
}

func standInLeagueKey(leagueID int) string {
	return fmt.Sprintf("%s.l.%d", standInLeagueKeyPrefix, leagueID)
}

func isStandInLeagueKey(key string) bool {
	return strings.HasPrefix(key, standInLeagueKeyPrefix+".")
}
