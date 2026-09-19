package nhl

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/rs/zerolog/log"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/worker/shared"
)

// aggregateSettleDelay is how long after a team's last game a cached
// per-season aggregate (club stats, roster) must have been fetched before it
// is trusted. The NHL API keeps updating aggregates for a while after the
// final horn, and game dates are calendar days while file mtimes are
// instants: a file written on the last game day may predate that night's game.
const aggregateSettleDelay = 48 * time.Hour

// seasonAggregateStale reports whether a per-season aggregate cached at
// cachedAt must be refetched, judged against the team's club schedule.
// Only games whose type is in gameTypes count; an empty gameTypes means all.
//
// The aggregate is stale when the schedule is unknown (nil), when any
// counted game is not final yet, or when the file predates the last counted
// game by less than aggregateSettleDelay. FetchOrCache treats any cached
// file as a permanent hit, so this is the only thing standing between a
// mid-season snapshot and the database (a 2025-26 rebuild re-imported
// club stats fetched on April 1 for a season that ended April 16).
//
// A calendar gate (IsCurrentSeason) is deliberately not used: nhl.Current()
// rolls over on July 1, which froze mid-playoff caches once the next sync
// ran in the offseason. See FetchTeamPlayoffGames for the same reasoning.
func seasonAggregateStale(schedule *nhlapi.TeamScheduleResponse, cachedAt time.Time, gameTypes ...nhlapi.GameType) bool {
	if schedule == nil {
		return true
	}
	var lastGame time.Time
	for _, game := range schedule.Games {
		if !gameTypeCounted(game.GameType, gameTypes) {
			continue
		}
		if !game.GameState.IsFinal() {
			return true
		}
		gameDate, err := parseGameDate(game)
		if err != nil {
			return true
		}
		if gameDate.After(lastGame) {
			lastGame = gameDate
		}
	}
	if lastGame.IsZero() {
		// No counted games (e.g. a team that missed the playoffs): there is
		// nothing the cached aggregate could be missing.
		return false
	}
	return cachedAt.Before(lastGame.Add(aggregateSettleDelay))
}

// gameTypeCounted reports whether gameType is one of gameTypes; an empty
// gameTypes counts everything.
func gameTypeCounted(gameType nhlapi.GameType, gameTypes []nhlapi.GameType) bool {
	if len(gameTypes) == 0 {
		return true
	}
	for _, gt := range gameTypes {
		if gt == gameType {
			return true
		}
	}
	return false
}

// readCachedClubSchedule returns the team's cached club schedule, or nil when
// it is absent or unreadable. The schedule is written and refreshed by
// FetchTeamPlayoffGames, which runs after the aggregate fetches in
// FetchSeasonWorkflow, so the copy read here is one sync run behind. That
// only errs on the side of refetching: an old schedule still lists non-final
// games, and playoff rounds are appended within the settle delay of the
// previous round ending. A missing schedule makes every aggregate look
// stale, which costs one refetch per resource per run and never a stale
// import.
func readCachedClubSchedule(ctx context.Context, storage store.Storage, gobCache *cache.GobCache, season int, teamAbbrev string) *nhlapi.TeamScheduleResponse {
	res := resource.ClubScheduleSeason{Season: season, TeamAbbrev: teamAbbrev}
	schedule, _, err := cache.ReadParsedCached(ctx, storage, gobCache, res)
	if err != nil {
		return nil
	}
	return schedule
}

// seasonAggregateNeedsRefetch reports whether the cached copy of r, if any,
// must be replaced by a fresh fetch according to seasonAggregateStale. A
// resource that is not cached at all does not need forcing: FetchOrCache
// will fetch it anyway. Any other stat failure counts as unknown age, i.e.
// stale: an extra fetch is cheap, importing a stale snapshot is not.
func seasonAggregateNeedsRefetch(ctx context.Context, storage store.Storage, r core.Resource, schedule *nhlapi.TeamScheduleResponse, gameTypes ...nhlapi.GameType) bool {
	info, err := storage.Stat(ctx, r.Path())
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		return true
	}
	return seasonAggregateStale(schedule, info.ModTime(), gameTypes...)
}

// fetchSeasonAggregate fetches r through the cache, or straight from the API
// when the cached copy is stale for the given game types. The stale copy is
// overwritten only once the fetch succeeds, so a 404 or transient failure
// never loses data the API may not serve again.
func fetchSeasonAggregate[T any](
	ctx context.Context,
	storage store.Storage,
	gobCache *cache.GobCache,
	r core.ReadWritable[T],
	schedule *nhlapi.TeamScheduleResponse,
	fetch func(ctx context.Context) (T, error),
	gameTypes ...nhlapi.GameType,
) (T, error) {
	if seasonAggregateNeedsRefetch(ctx, storage, r, schedule, gameTypes...) {
		log.Debug().Str("path", r.Path()).Msg("Cached season aggregate is stale, refetching")
		obj, _, err := shared.FetchAndCache(ctx, storage, gobCache, r, fetch)
		return obj, err
	}
	obj, _, err := shared.FetchOrCache(ctx, storage, gobCache, r, fetch)
	return obj, err
}
