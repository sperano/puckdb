package yahoo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// leagueIdentityErrorType tags imports that would overwrite another season's
// league: yahoo_leagues and its child tables key leagues by the numeric ID
// alone, which Yahoo only keeps unique within one season.
const leagueIdentityErrorType = "YahooLeagueIdentityCollision"

// leagueSettingsSource says where the settings being imported were read
// from; a TEMPORARY stand-in reads another season's league.
type leagueSettingsSource struct {
	source    draft.Source
	season    int
	leagueKey string
	fetchedAt time.Time
}

// ImportYahooLeague imports a Yahoo league from cached XML into the database.
func (a *ImportActivities) ImportYahooLeague(ctx context.Context, input ImportYahooLeagueInput) (ImportYahooLeagueResult, error) {
	defer metrics.TrackActivityDuration("ImportYahooLeague")()

	logger := activity.GetLogger(ctx)

	leagueRes := resource.League{Season: input.Season, LeagueID: input.LeagueID}
	if !a.Storage.Exists(ctx, leagueRes.Path()) {
		return ImportYahooLeagueResult{}, fmt.Errorf("required Yahoo league cache is missing for season %d league %d",
			input.Season, input.LeagueID)
	}
	league, fetchedAt, err := readLeagueSettings(ctx, a.Storage, leagueRes)
	if err != nil {
		return ImportYahooLeagueResult{}, err
	}
	stored, found, err := a.storedLeague(ctx, league.ID)
	if err != nil {
		return ImportYahooLeagueResult{}, err
	}
	if err := checkLeagueIdentity(stored, found, league.ID, league.Season); err != nil {
		return ImportYahooLeagueResult{}, err
	}
	if err := a.clearStandInLeagueSettings(ctx, stored, found); err != nil {
		return ImportYahooLeagueResult{}, err
	}
	source := leagueSettingsSource{source: draft.SourceYahooAPI, season: league.Season, leagueKey: league.Key, fetchedAt: fetchedAt}
	result, err := a.upsertLeague(ctx, league, source)
	if err != nil {
		return ImportYahooLeagueResult{}, err
	}

	logger.Info("Imported Yahoo league",
		"season", input.Season,
		"leagueID", input.LeagueID,
		"rosterPositions", result.RosterPositions,
		"statCategories", result.StatCategories,
		"newRulesVersion", result.NewRulesVersion)

	return result, nil
}

// readLeagueSettings parses the cached settings file straight from storage,
// bypassing the Redis gob cache: an entry encoded before a field was added
// to store.Settings would decode with that field zeroed and silently drop
// rules such as stat directions or points weights. It also returns the
// file's modification time, which is when Yahoo served it.
func readLeagueSettings(ctx context.Context, storage store.Storage, res resource.League) (store.League, time.Time, error) {
	fantasy, err := resource.ReadParsed(ctx, storage, res)
	if err != nil {
		return store.League{}, time.Time{}, fmt.Errorf("read league file %d/%d: %w", res.Season, res.LeagueID, err)
	}
	info, err := storage.Stat(ctx, res.Path())
	if err != nil {
		return store.League{}, time.Time{}, fmt.Errorf("stat league file %d/%d: %w", res.Season, res.LeagueID, err)
	}
	return fantasy.League, info.ModTime(), nil
}

func (a *ImportActivities) storedLeague(ctx context.Context, leagueID int) (sqlcdb.YahooLeague, bool, error) {
	stored, err := a.Queries.GetYahooLeague(ctx, int32(leagueID))
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlcdb.YahooLeague{}, false, nil
	}
	if err != nil {
		return sqlcdb.YahooLeague{}, false, fmt.Errorf("get league %d: %w", leagueID, err)
	}
	return stored, true, nil
}

// checkLeagueIdentity refuses to import a league over a yahoo_leagues row
// that holds another season's league with the same numeric ID. Overwriting it
// would mix both seasons' roster positions, categories, teams and draft picks.
func checkLeagueIdentity(stored sqlcdb.YahooLeague, found bool, leagueID, season int) error {
	if !found || int(stored.Season) == season {
		return nil
	}
	return temporal.NewNonRetryableApplicationError(
		fmt.Sprintf("season %d league %d collides with season %d league %d (key %s): yahoo_leagues is keyed by "+
			"the numeric league ID only, and Yahoo reuses IDs across seasons; importing would overwrite the "+
			"other season's settings, teams and draft results", season, leagueID, stored.Season, leagueID, stored.LeagueKey),
		leagueIdentityErrorType, nil)
}

// upsertLeague writes the league row, its roster positions and its stat
// categories, then records the rules version.
func (a *ImportActivities) upsertLeague(ctx context.Context, league store.League, source leagueSettingsSource) (ImportYahooLeagueResult, error) {
	rules := draft.FromLeague(league)
	if err := a.Queries.UpsertYahooLeague(ctx, yahooLeagueParams(league)); err != nil {
		return ImportYahooLeagueResult{}, fmt.Errorf("upsert league %d: %w", league.ID, err)
	}
	positions, err := a.upsertRosterPositions(ctx, league)
	if err != nil {
		return ImportYahooLeagueResult{}, err
	}
	categories, err := a.upsertStatCategories(ctx, league.ID, rules.Categories)
	if err != nil {
		return ImportYahooLeagueResult{}, err
	}
	newVersion, err := a.recordRulesSnapshot(ctx, rules, source)
	if err != nil {
		return ImportYahooLeagueResult{}, err
	}
	return ImportYahooLeagueResult{RosterPositions: positions, StatCategories: categories, NewRulesVersion: newVersion}, nil
}

func yahooLeagueParams(league store.League) sqlcdb.UpsertYahooLeagueParams {
	var draftTime pgtype.Timestamptz
	if league.Settings.DraftTime > 0 {
		draftTime = pgtype.Timestamptz{
			Time:  time.Unix(int64(league.Settings.DraftTime), 0),
			Valid: true,
		}
	}

	return sqlcdb.UpsertYahooLeagueParams{
		ID:                    int32(league.ID),
		LeagueKey:             league.Key,
		Name:                  league.Name,
		Url:                   league.URL,
		LogoUrl:               league.LogoURL,
		Season:                int32(league.Season),
		GameCode:              league.GameCode,
		NumTeams:              int32(league.NumTeams),
		ScoringType:           league.ScoringType,
		LeagueType:            league.LeagueType,
		DraftStatus:           league.DraftStatus,
		IsProLeague:           league.IsProLeague == 1,
		IsCashLeague:          league.IsCashLeague == 1,
		StartDate:             shared.ParseDateToPgDate(league.StartDate),
		EndDate:               shared.ParseDateToPgDate(league.EndDate),
		DraftType:             league.Settings.DraftType,
		IsAuctionDraft:        league.Settings.IsAuctionDraft == 1,
		DraftTime:             draftTime,
		DraftPickTime:         pgtype.Int4{Int32: int32(league.Settings.DraftPickTime), Valid: league.Settings.DraftPickTime > 0},
		WaiverType:            league.Settings.WaiverType,
		WaiverRule:            league.Settings.WaiverRule,
		WaiverTime:            pgtype.Int4{Int32: int32(league.Settings.WaiverTime), Valid: league.Settings.WaiverTime > 0},
		TradeEndDate:          shared.ParseDateToPgDate(league.Settings.TradeEndDate),
		TradeRatifyType:       league.Settings.TradeRatifyType,
		TradeRejectTime:       pgtype.Int4{Int32: int32(league.Settings.TradeRejectTime), Valid: league.Settings.TradeRejectTime > 0},
		MaxTeams:              pgtype.Int4{Int32: int32(league.Settings.MaxTeams), Valid: league.Settings.MaxTeams > 0},
		PlayerPool:            league.Settings.PlayerPool,
		PostDraftPlayers:      league.Settings.PostDraftPlayers,
		CantCutList:           league.Settings.CantCutList,
		UsesPlayoff:           league.Settings.UsesPlayoff == 1,
		PersistentUrl:         league.Settings.PersistentURL,
		LeagueUpdateTimestamp: pgtype.Int8{Int64: int64(league.LeagueUpdateTimestamp), Valid: league.LeagueUpdateTimestamp > 0},
	}
}

// upsertRosterPositions batch-upserts the league's roster positions and
// returns how many it wrote.
func (a *ImportActivities) upsertRosterPositions(ctx context.Context, league store.League) (int, error) {
	positions := league.Settings.RosterPositions.Slice
	if len(positions) == 0 {
		return 0, nil
	}
	posParams := make([]sqlcdb.UpsertYahooLeagueRosterPositionBatchParams, len(positions))
	for i, pos := range positions {
		posParams[i] = sqlcdb.UpsertYahooLeagueRosterPositionBatchParams{
			LeagueID:           int32(league.ID),
			Position:           pos.Position,
			PositionType:       pos.PositionType,
			Count:              int32(pos.Count),
			IsStartingPosition: pos.IsStartingPosition == 1,
		}
	}

	if err := shared.ExecBatch(a.Queries.UpsertYahooLeagueRosterPositionBatch(ctx, posParams), func(i int) string {
		return fmt.Sprintf("roster position %s", posParams[i].Position)
	}); err != nil {
		return 0, err
	}
	return len(posParams), nil
}

// upsertStatCategories batch-upserts the league's stat categories, including
// their direction, display-only flag and points weight, and returns how many
// it wrote.
func (a *ImportActivities) upsertStatCategories(ctx context.Context, leagueID int, categories []draft.StatCategory) (int, error) {
	if len(categories) == 0 {
		return 0, nil
	}
	statParams := make([]sqlcdb.UpsertYahooLeagueStatCategoryBatchParams, len(categories))
	for i, category := range categories {
		statParams[i] = statCategoryParams(leagueID, category)
	}

	if err := shared.ExecBatch(a.Queries.UpsertYahooLeagueStatCategoryBatch(ctx, statParams), func(i int) string {
		return fmt.Sprintf("stat category %d", statParams[i].StatID)
	}); err != nil {
		return 0, err
	}
	return len(statParams), nil
}

// Yahoo sort_order values stored in yahoo_league_stat_categories.sort_order.
const (
	sortOrderHigherIsBetter = 1
	sortOrderLowerIsBetter  = 0
)

func statCategoryParams(leagueID int, category draft.StatCategory) sqlcdb.UpsertYahooLeagueStatCategoryBatchParams {
	params := sqlcdb.UpsertYahooLeagueStatCategoryBatchParams{
		LeagueID:          int32(leagueID),
		StatID:            int32(category.StatID),
		Name:              category.Name,
		Abbr:              category.Abbr,
		StatGroup:         category.Group,
		Enabled:           category.Enabled,
		DisplayName:       category.DisplayName,
		IsOnlyDisplayStat: category.DisplayOnly,
	}
	if len(category.PositionTypes) == 1 {
		params.PositionType = category.PositionTypes[0]
	}
	if category.Weight != nil {
		params.Value = pgtype.Float4{Float32: float32(*category.Weight), Valid: true}
	}
	switch category.Direction {
	case draft.HigherIsBetter:
		params.SortOrder = pgtype.Int2{Int16: sortOrderHigherIsBetter, Valid: true}
	case draft.LowerIsBetter:
		params.SortOrder = pgtype.Int2{Int16: sortOrderLowerIsBetter, Valid: true}
	}
	return params
}

// recordRulesSnapshot stores the rules as a new version, or marks an
// identical version as seen again. It reports whether the version is new.
func (a *ImportActivities) recordRulesSnapshot(ctx context.Context, rules draft.Rules, source leagueSettingsSource) (bool, error) {
	hash, data, err := rules.Hash()
	if err != nil {
		return false, err
	}
	gameKey := pgtype.Int4{Int32: int32(rules.GameKey), Valid: rules.GameKey > 0}
	row, err := a.Queries.UpsertYahooLeagueRuleSnapshot(ctx, sqlcdb.UpsertYahooLeagueRuleSnapshotParams{
		Season: int32(rules.Season), LeagueID: int32(rules.LeagueID), LeagueKey: rules.LeagueKey,
		GameKey: gameKey, Source: string(source.source), SourceSeason: int32(source.season),
		SourceLeagueKey: source.leagueKey, FetchedAt: pgtype.Timestamptz{Time: source.fetchedAt, Valid: true},
		RulesHash: hash, Rules: data,
	})
	if err != nil {
		return false, fmt.Errorf("record rules snapshot for league %s: %w", rules.LeagueKey, err)
	}
	return row.Inserted, nil
}
