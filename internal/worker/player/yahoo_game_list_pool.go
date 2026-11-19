package player

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/sperano/puckdb/internal/matching"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"go.temporal.io/sdk/activity"
)

// YahooIDPoolSource names what a ProcessPlayers chain built its Yahoo ID
// pool from, so a rebuild reads the same source and checks it the same way.
type YahooIDPoolSource string

const (
	// YahooIDPoolSourcePages is the scraped yahoo-players/ pages. It is the
	// zero value: chains started before the game player lists decode it.
	YahooIDPoolSourcePages YahooIDPoolSource = ""
	// YahooIDPoolSourceGameLists is the committed Yahoo game player lists
	// (resource.GamePlayerList), one per season.
	YahooIDPoolSourceGameLists YahooIDPoolSource = "game_lists"
)

// gameListPlayers is every player of the committed game player lists, one
// entry per Yahoo ID, and the seasons whose list was read (newest first).
type gameListPlayers struct {
	Players []store.YahooPlayer
	Seasons []int
}

// BuildYahooIDPool is phase 1 of a ProcessPlayers chain started after the
// game player lists replaced the page scan: it reads every committed game
// player list, adds the yahoo_league_players players no list names, and
// saves the pool to Redis in one activity, so the pool never crosses the
// workflow history.
func (a *Activities) BuildYahooIDPool(ctx context.Context) (*SaveYahooIDPoolResult, error) {
	listed, err := a.readGameListPlayers(ctx)
	if err != nil {
		return nil, err
	}
	result, err := a.saveGameListPool(ctx, listed)
	if err != nil {
		return nil, err
	}
	activity.GetLogger(ctx).Info("Saved Yahoo ID pool from the game player lists",
		"seasons", len(result.ListSeasons),
		"list_entries", result.ListEntries,
		"total", result.TotalPlayers,
		"available", result.AvailablePlayers,
		"skipped_non_nhl", result.SkippedNonNHL,
		"pool_size", result.PoolSize)
	return result, nil
}

// saveGameListPool merges the league players into listed (a list wins on the
// same Yahoo ID) and writes the pool.
func (a *Activities) saveGameListPool(ctx context.Context, listed gameListPlayers) (*SaveYahooIDPoolResult, error) {
	players, listEntries, err := a.withLeaguePlayers(ctx, listed.Players)
	if err != nil {
		return nil, err
	}
	result, err := saveYahooPlayersToRedisImpl(ctx, a.RedisClient, players)
	if err != nil {
		return nil, err
	}
	result.Source = YahooIDPoolSourceGameLists
	result.ListSeasons = listed.Seasons
	result.ListEntries = listEntries
	return result, nil
}

// rebuildYahooIDPoolFromGameLists writes a list-built pool again. Every page
// a manifest names was checked when it was committed, so a page that cannot
// be read or parsed now fails the rebuild (retried) instead of leaving a
// partial pool. A season whose list phase 1 read but that now has none fails
// it too. Counts are not compared: the current season's list is replaced by
// each refresh, and may list fewer players than the one phase 1 read.
func (a *Activities) rebuildYahooIDPoolFromGameLists(ctx context.Context, result EnsureYahooIDPoolResult, expected EnsureYahooIDPoolInput) (EnsureYahooIDPoolResult, error) {
	listed, err := a.readGameListPlayers(ctx)
	if err != nil {
		return result, err
	}
	if missing := missingSeasons(expected.ListSeasons, listed.Seasons); len(missing) > 0 {
		return result, fmt.Errorf("rebuild yahoo id pool: no committed game player list for seasons %v that phase 1 read", missing)
	}
	saved, err := a.saveGameListPool(ctx, listed)
	if err != nil {
		return result, err
	}
	if err := a.removeMatchedFromAvailable(ctx); err != nil {
		return result, err
	}
	return a.rebuiltResult(ctx, result, saved)
}

// missingSeasons returns the seasons of want that got lacks.
func missingSeasons(want, got []int) []int {
	var missing []int
	for _, season := range want {
		if !slices.Contains(got, season) {
			missing = append(missing, season)
		}
	}
	return missing
}

// readGameListPlayers reads the committed game player list of every season
// in the cached Yahoo NHL games list, newest season first, and keeps each
// Yahoo ID's entry from the newest season that lists it. No cached games
// list, or a season without a committed list, contributes nothing; a
// manifest that does not parse is skipped with a warning, as a commit
// treats it. Any other read error fails.
func (a *Activities) readGameListPlayers(ctx context.Context) (gameListPlayers, error) {
	seasons, err := a.yahooGameSeasons(ctx)
	if err != nil {
		return gameListPlayers{}, err
	}
	var listed gameListPlayers
	seen := make(map[store.YahooPlayerID]struct{})
	for _, season := range seasons {
		manifest, ok, err := a.readCommittedGamePlayerList(ctx, season)
		if err != nil {
			return gameListPlayers{}, err
		}
		if !ok {
			continue
		}
		players, err := a.readGamePlayerListPages(ctx, season, manifest)
		if err != nil {
			return gameListPlayers{}, err
		}
		listed.Seasons = append(listed.Seasons, season)
		for _, p := range players {
			if _, dup := seen[p.YahooID]; dup {
				continue
			}
			seen[p.YahooID] = struct{}{}
			listed.Players = append(listed.Players, p)
		}
	}
	return listed, nil
}

// yahooGameSeasons returns the seasons of the cached Yahoo NHL games list,
// newest first, none when no list is cached. It never downloads:
// FetchYahooPlayersWorkflow refreshes the list.
func (a *Activities) yahooGameSeasons(ctx context.Context) ([]int, error) {
	content, err := resource.ReadParsed(ctx, a.Storage, resource.YahooGames{})
	if errors.Is(err, os.ErrNotExist) {
		activity.GetLogger(ctx).Warn("No cached Yahoo NHL games list: the Yahoo ID pool has no game player list")
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Yahoo NHL games list: %w", err)
	}
	seasons := make([]int, 0, len(content.Games))
	for _, game := range content.Games {
		seasons = append(seasons, game.Season)
	}
	slices.Sort(seasons)
	slices.Reverse(seasons)
	return seasons, nil
}

// readCommittedGamePlayerList returns the season's committed manifest. It is
// read from storage, not the Valkey cache: each refresh of the current
// season replaces it under the same path.
func (a *Activities) readCommittedGamePlayerList(ctx context.Context, season int) (resource.GamePlayerListManifest, bool, error) {
	res := resource.GamePlayerList{Season: season}
	manifest, err := resource.ReadParsed(ctx, a.Storage, res)
	var parseErr *resource.ParseError
	switch {
	case err == nil:
		return manifest, true, nil
	case errors.Is(err, os.ErrNotExist):
		return resource.GamePlayerListManifest{}, false, nil
	case errors.As(err, &parseErr):
		activity.GetLogger(ctx).Warn("Game player list manifest does not parse, season left out of the Yahoo ID pool",
			"season", season, "path", res.Path(), "error", err)
		return resource.GamePlayerListManifest{}, false, nil
	default:
		return resource.GamePlayerListManifest{}, false, fmt.Errorf("read game player list manifest %d: %w", season, err)
	}
}

// readGamePlayerListPages reads every page the manifest names, through the
// Valkey cache (a page path names its download, so a cached page never goes
// stale), heartbeating after each page. A page that cannot be read or
// parsed fails: its commit already read it.
func (a *Activities) readGamePlayerListPages(ctx context.Context, season int, manifest resource.GamePlayerListManifest) ([]store.YahooPlayer, error) {
	players := make([]store.YahooPlayer, 0, manifest.Players)
	for _, start := range manifest.Starts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page := resource.GamePlayers{
			Season: season, DownloadID: manifest.DownloadID, Start: start, GameKey: manifest.GameKey,
		}
		content, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, page)
		if err != nil {
			return nil, fmt.Errorf("read game player list page %s: %w", page.Path(), err)
		}
		for _, p := range content.Game.Players.Slice {
			if player, ok := yahooPlayerFromGameList(p); ok {
				players = append(players, player)
			}
		}
		activity.RecordHeartbeat(ctx, page.Path())
	}
	return players, nil
}

// yahooPlayerFromGameList converts a game player list entry to a pool entry:
// name, positions (display position, as for league players), uniform number,
// headshot and the Yahoo page-style team name filterByTeam compares
// (matching.YahooTeamNameForAbbrev; empty for an unknown abbreviation). The
// list carries no birth date. A structured name missing a part is replaced
// by the full name when that splits into both parts, else kept; with no
// structured part at all, the full name's split is used as it is. An entry
// without a name is left out, as a league player with a blank name is.
func yahooPlayerFromGameList(p store.Player) (store.YahooPlayer, bool) {
	first, last := strings.TrimSpace(p.Name.First), strings.TrimSpace(p.Name.Last)
	if first == "" || last == "" {
		f, l := store.ParseCombinedName(p.Name.Full)
		if (f != "" && l != "") || (first == "" && last == "") {
			first, last = f, l
		}
	}
	if first == "" && last == "" {
		return store.YahooPlayer{}, false
	}
	image := p.Headshot.URL
	if image == "" {
		image = p.ImageURL
	}
	return store.YahooPlayer{
		YahooID:      store.YahooPlayerID(p.ID),
		FirstName:    first,
		LastName:     last,
		Positions:    store.ParsePositions(strings.TrimSpace(p.DisplayPosition)),
		JerseyNumber: p.UniformNumber,
		Team:         matching.YahooTeamNameForAbbrev(p.EditorialTeamAbbr),
		ImageURL:     image,
	}, true
}
