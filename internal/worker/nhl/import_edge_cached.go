package nhl

import (
	"context"
	"fmt"
	"slices"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// Kinds of cached Edge detail, used in heartbeat details, logs and errors.
const (
	edgeKindSkater       = "skater"
	edgeKindGoalie       = "goalie"
	edgeKindTeam         = "team"
	edgeKindTeamZoneTime = "team-zt"
)

// edgeImportScope is the season and game type every Edge row is keyed by.
type edgeImportScope struct {
	season   nhlapi.Season
	gameType nhlapi.GameType
	seasonID int32
	gtLabel  sqlcdb.GameType
}

// newEdgeImportScope derives the scope from an activity input's season start
// year (e.g. 2024 for 2024-2025) and numeric game type.
func newEdgeImportScope(seasonStartYear, gameType int) edgeImportScope {
	season := nhlapi.NewSeason(seasonStartYear)
	gt := nhlapi.GameType(gameType)
	return edgeImportScope{
		season:   season,
		gameType: gt,
		seasonID: int32(season.ID()),
		gtLabel:  sqlcdb.GameType(gt.Label()),
	}
}

// edgeTeam identifies the team a per-team import runs for.
type edgeTeam struct {
	id     nhlapi.TeamID
	abbrev string
}

// edgeTeamImporter imports one kind of Edge data for one team and reports
// how many cached details it imported.
type edgeTeamImporter func(ctx context.Context, scope edgeImportScope, team edgeTeam) (int, error)

// importEdgeSeason runs a per-team importer over every team of the season.
func (a *SeasonsActivities) importEdgeSeason(ctx context.Context, input FetchEdgeInput, kind string, importTeam edgeTeamImporter) error {
	scope := newEdgeImportScope(input.Season, input.GameType)

	teams, err := a.EdgeQueries.GetSeasonTeamAbbrevs(ctx, scope.seasonID)
	if err != nil {
		return fmt.Errorf("get season teams: %w", err)
	}

	var imported int
	for _, row := range teams {
		n, err := importTeam(ctx, scope, edgeTeam{id: nhlapi.TeamID(row.TeamID), abbrev: row.Abbrev})
		if err != nil {
			return err
		}
		imported += n
	}

	activity.GetLogger(ctx).Info("Imported edge stats", "kind", kind, "season", input.Season, "count", imported)
	return nil
}

// importEdgeTeamSkaters imports the cached Edge detail of every skater on the team's roster.
func (a *SeasonsActivities) importEdgeTeamSkaters(ctx context.Context, scope edgeImportScope, team edgeTeam) (int, error) {
	roster, ok, err := a.edgeRoster(ctx, scope, team)
	if !ok {
		return 0, err
	}
	return importEdgeRosterDetails(ctx, a, edgeKindSkater, team, slices.Concat(roster.Forwards, roster.Defensemen),
		func(id nhlapi.PlayerID) core.Parseable[*nhlapi.EdgeSkaterDetail] {
			return resource.EdgeSkaterDetail{PlayerID: id, Season: scope.season, GameType: scope.gameType}
		},
		func(ctx context.Context, id nhlapi.PlayerID, detail *nhlapi.EdgeSkaterDetail) error {
			return importEdgeSkaterDetail(ctx, a.EdgeQueries, detail, int64(id), scope.seasonID, scope.gtLabel)
		})
}

// importEdgeTeamGoalies imports the cached Edge detail of every goalie on the team's roster.
func (a *SeasonsActivities) importEdgeTeamGoalies(ctx context.Context, scope edgeImportScope, team edgeTeam) (int, error) {
	roster, ok, err := a.edgeRoster(ctx, scope, team)
	if !ok {
		return 0, err
	}
	return importEdgeRosterDetails(ctx, a, edgeKindGoalie, team, roster.Goalies,
		func(id nhlapi.PlayerID) core.Parseable[*nhlapi.EdgeGoalieDetail] {
			return resource.EdgeGoalieDetail{GoalieID: id, Season: scope.season, GameType: scope.gameType}
		},
		func(ctx context.Context, id nhlapi.PlayerID, detail *nhlapi.EdgeGoalieDetail) error {
			return importEdgeGoalieDetail(ctx, a.EdgeQueries, detail, int64(id), scope.seasonID, scope.gtLabel)
		})
}

// importEdgeTeamStats imports the team's cached Edge detail.
func (a *SeasonsActivities) importEdgeTeamStats(ctx context.Context, scope edgeImportScope, team edgeTeam) (int, error) {
	res := resource.EdgeTeamDetail{TeamID: team.id, Season: scope.season, GameType: scope.gameType}
	return importCachedEdgeDetail(ctx, a, edgeKindTeam, team.abbrev, res, func(ctx context.Context, detail *nhlapi.EdgeTeamDetail) error {
		return importEdgeTeamDetail(ctx, a.EdgeQueries, detail, int64(team.id), scope.seasonID, scope.gtLabel)
	})
}

// importEdgeTeamZoneTimeDetails imports the team's cached zone time details.
func (a *SeasonsActivities) importEdgeTeamZoneTimeDetails(ctx context.Context, scope edgeImportScope, team edgeTeam) (int, error) {
	res := resource.EdgeTeamZoneTimeDetails{TeamID: team.id, Season: scope.season, GameType: scope.gameType}
	return importCachedEdgeDetail(ctx, a, edgeKindTeamZoneTime, team.abbrev, res, func(ctx context.Context, detail *nhlapi.EdgeTeamZoneTimeDetails) error {
		return importEdgeTeamZoneTime(ctx, a.EdgeQueries, detail, int64(team.id), scope.seasonID, scope.gtLabel)
	})
}

// edgeRoster loads the team's cached season roster. A team without a usable
// roster has nothing to import, so it reports ok=false without an error; only
// cancellation is reported as an error.
func (a *SeasonsActivities) edgeRoster(ctx context.Context, scope edgeImportScope, team edgeTeam) (roster *nhlapi.Roster, ok bool, err error) {
	roster, err = a.loadSeasonRoster(ctx, team.abbrev, scope.season)
	if err == nil {
		return roster, true, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, false, fmt.Errorf("load roster %s: %w", team.abbrev, ctxErr)
	}
	activity.GetLogger(ctx).Debug("No roster found for team", "team", team.abbrev, "season", scope.season.StartYear(), "err", err)
	return nil, false, nil
}

// importEdgeRosterDetails imports the cached Edge detail of each player and
// returns how many were imported.
func importEdgeRosterDetails[T any](
	ctx context.Context,
	a *SeasonsActivities,
	kind string,
	team edgeTeam,
	players []nhlapi.RosterPlayer,
	resourceFor func(nhlapi.PlayerID) core.Parseable[T],
	upsert func(context.Context, nhlapi.PlayerID, T) error,
) (int, error) {
	var imported int
	for _, player := range players {
		id := player.ID
		subject := fmt.Sprintf("%s:%d", team.abbrev, id)
		n, err := importCachedEdgeDetail(ctx, a, kind, subject, resourceFor(id), func(ctx context.Context, detail T) error {
			return upsert(ctx, id, detail)
		})
		if err != nil {
			return imported, err
		}
		imported += n
	}

	activity.GetLogger(ctx).Debug("Imported edge details for team", "kind", kind, "team", team.abbrev, "count", imported)
	return imported, nil
}

// importCachedEdgeDetail imports one cached Edge detail file and returns 1 if
// it was imported, 0 if it was skipped (not cached, or unreadable). upsert
// receives this function's ctx so the helper, not the caller's closure, owns
// the context the database call runs under.
//
// Every call heartbeats before touching storage: a heartbeat is both what
// keeps a slow import alive past the heartbeat timeout and the only channel
// through which Temporal delivers cancellation to the activity context.
func importCachedEdgeDetail[T any](
	ctx context.Context,
	a *SeasonsActivities,
	kind, subject string,
	res core.Parseable[T],
	upsert func(context.Context, T) error,
) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("import edge %s %s: %w", kind, subject, err)
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("import-edge-%s:%s", kind, subject))

	if !a.Storage.Exists(ctx, res.Path()) {
		return 0, nil
	}

	detail, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, res)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, fmt.Errorf("import edge %s %s: %w", kind, subject, ctxErr)
		}
		activity.GetLogger(ctx).Warn("Failed to read edge detail from cache", "kind", kind, "subject", subject, "err", err)
		return 0, nil
	}

	if err := upsert(ctx, detail); err != nil {
		return 0, fmt.Errorf("import edge %s %s: %w", kind, subject, err)
	}
	return 1, nil
}
