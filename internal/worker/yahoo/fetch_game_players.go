package yahoo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// invalidGamePlayersPageErrorType tags a game players page request that names
// no game key, which no retry can fix.
const invalidGamePlayersPageErrorType = "InvalidYahooGamePlayersPage"

// PlanYahooGamePlayerListInput names the season whose Yahoo game player list
// may need a fresh download.
type PlanYahooGamePlayerListInput struct {
	Season int `json:"season"`
}

// YahooGamePlayerListPlan says whether to download the season's game player
// list again. GameKey is the season's Yahoo game key, which the page
// downloads need; Unavailable means Yahoo lists no NHL game for the season.
// PreviousPlayerCount is the player count of the last committed list (0 when
// there is none or it is unreadable), which sizes the download's progress
// before its pages arrive.
type YahooGamePlayerListPlan struct {
	Refresh             bool   `json:"refresh"`
	Unavailable         bool   `json:"unavailable,omitempty"`
	Reason              string `json:"reason"`
	GameKey             int    `json:"gameKey,omitempty"`
	PreviousPlayerCount int    `json:"previousPlayerCount,omitempty"`
}

// FetchYahooGamePlayersPageInput names one page of one game player list
// download.
type FetchYahooGamePlayersPageInput struct {
	Season     int   `json:"season"`
	GameKey    int   `json:"gameKey"`
	DownloadID int64 `json:"downloadId"`
	Start      int   `json:"start"`
}

// FetchYahooGamePlayersPageResult reports what one page held.
type FetchYahooGamePlayersPageResult struct {
	Players int `json:"players"`
}

// CommitYahooGamePlayerListInput records a complete game player list download.
type CommitYahooGamePlayerListInput struct {
	Season     int       `json:"season"`
	GameKey    int       `json:"gameKey"`
	DownloadID int64     `json:"downloadId"`
	FetchedAt  time.Time `json:"fetchedAt"`
	Starts     []int     `json:"starts"`
	Players    int       `json:"players"`
	// Discarded are this download's pages fetched concurrently past its last
	// page. No manifest names them, so the commit deletes them.
	Discarded []int `json:"discarded,omitempty"`
}

// PlanYahooGamePlayerList decides whether the season's game player list is
// due for a download: a committed list of a season that is over is final;
// the current season's list is downloaded again once it is older than
// --yahoo-game-player-list-max-age.
func (a *FetchActivities) PlanYahooGamePlayerList(ctx context.Context, input PlanYahooGamePlayerListInput) (YahooGamePlayerListPlan, error) {
	return a.planYahooGamePlayerList(ctx, input, time.Now())
}

func (a *FetchActivities) planYahooGamePlayerList(ctx context.Context, input PlanYahooGamePlayerListInput, now time.Time) (YahooGamePlayerListPlan, error) {
	game, found, err := a.nhlGame(ctx, input.Season)
	if err != nil {
		return YahooGamePlayerListPlan{}, err
	}
	if !found {
		return YahooGamePlayerListPlan{
			Unavailable: true, Reason: fmt.Sprintf("Yahoo lists no NHL game for season %d", input.Season),
		}, nil
	}
	plan := YahooGamePlayerListPlan{GameKey: game.Key}
	manifest, err := resource.ReadParsed(ctx, a.Storage, resource.GamePlayerList{Season: input.Season})
	var parseErr *resource.ParseError
	switch {
	case errors.Is(err, os.ErrNotExist):
		plan.Refresh, plan.Reason = true, "list never downloaded"
		return plan, nil
	case errors.As(err, &parseErr):
		plan.Refresh, plan.Reason = true, "list manifest unreadable"
		return plan, nil
	case err != nil:
		return YahooGamePlayerListPlan{}, fmt.Errorf("read game player list manifest %d: %w", input.Season, err)
	}
	plan.PreviousPlayerCount = manifest.Players
	return refreshGamePlayerList(plan, manifest, game, now), nil
}

// refreshGamePlayerList completes the plan of a season with a committed list.
func refreshGamePlayerList(plan YahooGamePlayerListPlan, manifest resource.GamePlayerListManifest,
	game store.FantasyGame, now time.Time) YahooGamePlayerListPlan {
	if manifest.GameKey != game.Key {
		plan.Refresh, plan.Reason = true, fmt.Sprintf("list downloaded for game key %d", manifest.GameKey)
		return plan
	}
	if nhlGameOver(game, now) {
		plan.Reason = fmt.Sprintf("season %d is over, its list is final", game.Season)
		return plan
	}
	maxAge := time.Duration(shared.ViperIntOrDefault(config.FlagYahooGamePlayerListMaxAge,
		config.DefaultYahooGamePlayerListMaxAge)) * time.Hour
	age := now.Sub(manifest.FetchedAt)
	if age < maxAge {
		plan.Reason = fmt.Sprintf("list downloaded %s ago", age.Truncate(time.Minute))
		return plan
	}
	plan.Refresh, plan.Reason = true, "list older than "+maxAge.String()
	return plan
}

// nhlGameOver reports whether the game's season is over: Yahoo says so, or
// a later NHL season has started (a cached games list keeps the flag it had
// when it was downloaded).
func nhlGameOver(game store.FantasyGame, now time.Time) bool {
	return game.IsGameOver == 1 || game.Season < resource.DeduceSeason(now)
}

// nhlGame returns the season's game from Yahoo's NHL games list, read
// through the cache. A cached list without the season predates its game, so
// the list is downloaded again once before the season is reported missing.
func (a *FetchActivities) nhlGame(ctx context.Context, season int) (store.FantasyGame, bool, error) {
	fetcher := a.fetcher()
	content, _, err := fetcher.Fetch(ctx, resource.YahooGames{})
	if err != nil {
		return store.FantasyGame{}, false, fmt.Errorf("fetch Yahoo NHL games: %w", err)
	}
	if game, ok := resource.FindNHLGame(content, season); ok {
		return game, true, nil
	}
	content, err = fetcher.Refresh(ctx, resource.YahooGames{})
	if err != nil {
		return store.FantasyGame{}, false, fmt.Errorf("refresh Yahoo NHL games: %w", err)
	}
	game, ok := resource.FindNHLGame(content, season)
	return game, ok, nil
}

// FetchYahooGamePlayersPage downloads one page of the season's game player
// list, even when a copy is cached, under the download's own directory.
func (a *FetchActivities) FetchYahooGamePlayersPage(ctx context.Context, input FetchYahooGamePlayersPageInput) (FetchYahooGamePlayersPageResult, error) {
	defer metrics.TrackActivityDuration("FetchYahooGamePlayersPage")()
	if input.GameKey <= 0 {
		return FetchYahooGamePlayersPageResult{}, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("game players %d start %d: no game key", input.Season, input.Start),
			invalidGamePlayersPageErrorType, nil)
	}
	res := resource.GamePlayers{
		Season: input.Season, DownloadID: input.DownloadID, Start: input.Start, GameKey: input.GameKey,
	}
	content, err := a.fetcher().Refresh(ctx, res)
	if err != nil {
		return FetchYahooGamePlayersPageResult{}, fmt.Errorf("fetch game players %d start %d: %w",
			input.Season, input.Start, err)
	}
	players := len(content.Game.Players.Slice)
	activity.GetLogger(ctx).Info("Game players page downloaded",
		"season", input.Season, "game_key", input.GameKey, "start", input.Start, "players", players)
	return FetchYahooGamePlayersPageResult{Players: players}, nil
}

// CommitYahooGamePlayerList writes the manifest that makes the downloaded
// pages the season's game player list, once every page it names is stored,
// then removes the pages of the list it replaces and the download's
// discarded pages. Pages of downloads that failed before their commit are
// never named by a manifest and are left in place.
func (a *FetchActivities) CommitYahooGamePlayerList(ctx context.Context, input CommitYahooGamePlayerListInput) error {
	for _, start := range input.Starts {
		page := resource.GamePlayers{Season: input.Season, DownloadID: input.DownloadID, Start: start}
		if !resource.Exists(ctx, a.Storage, page) {
			return fmt.Errorf("commit game player list %d: page %s is missing", input.Season, page.Path())
		}
	}
	res := resource.GamePlayerList{Season: input.Season}
	previous, prevErr := resource.ReadParsed(ctx, a.Storage, res)
	manifest := resource.GamePlayerListManifest{
		DownloadID: input.DownloadID, GameKey: input.GameKey, FetchedAt: input.FetchedAt,
		Starts: input.Starts, Players: input.Players,
	}
	if err := resource.WriteParsed(ctx, a.Storage, res, manifest); err != nil {
		return fmt.Errorf("write game player list manifest %d: %w", input.Season, err)
	}
	activity.GetLogger(ctx).Info("Game player list downloaded",
		"season", input.Season, "game_key", input.GameKey, "players", input.Players, "pages", len(input.Starts))
	if prevErr == nil && previous.DownloadID != input.DownloadID {
		a.deleteGamePlayerPages(ctx, input.Season, previous.DownloadID, previous.Starts)
	}
	a.deleteGamePlayerPages(ctx, input.Season, input.DownloadID, input.Discarded)
	return nil
}

// deleteGamePlayerPages removes pages of one download that no manifest names
// any more. It is best effort: a leftover page wastes space but is never
// read again.
func (a *FetchActivities) deleteGamePlayerPages(ctx context.Context, season int, downloadID int64, starts []int) {
	for _, start := range starts {
		page := resource.GamePlayers{Season: season, DownloadID: downloadID, Start: start}
		if err := resource.Delete(ctx, a.Storage, page); err != nil {
			activity.GetLogger(ctx).Warn("Could not delete replaced game player list page", "path", page.Path(), "error", err)
		}
	}
}
