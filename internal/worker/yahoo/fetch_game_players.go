package yahoo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// invalidGamePlayersPageErrorType tags a game players page request whose
// season, game key, download ID or offset is out of range, which no retry
// can fix.
const invalidGamePlayersPageErrorType = "InvalidYahooGamePlayersPage"

// invalidGamePlayerListErrorType tags a commit that no retry can fix: its
// manifest GamePlayerListManifest.Validate rejects, a discarded page it would
// also name, or stored pages of another game or player count.
const invalidGamePlayerListErrorType = "InvalidYahooGamePlayerList"

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
	// Discarded are pages of this download past its last page, which the
	// commit deletes since no manifest names them. The game player list
	// download requests its pages one at a time and never sets it.
	Discarded []int `json:"discarded,omitempty"`
}

// DiscardYahooGamePlayerListInput names the pages a game player list
// download requested before it failed.
type DiscardYahooGamePlayerListInput struct {
	Season     int   `json:"season"`
	DownloadID int64 `json:"downloadId"`
	Starts     []int `json:"starts"`
}

// MaxGamePlayerListPages caps a game player list download so a Yahoo
// response that never ends cannot loop forever: 200 pages of 25 is 5,000
// players, about three times the largest season list Yahoo serves (1,613).
const MaxGamePlayerListPages = 200

// ListYahooNHLGameSeasons downloads Yahoo's NHL games list again and returns
// the season of every game in it, oldest first. The download keeps the list
// the plans read through the cache current: a new season's game and the
// is_game_over flag of the last one.
func (a *FetchActivities) ListYahooNHLGameSeasons(ctx context.Context) ([]int, error) {
	defer metrics.TrackActivityDuration("ListYahooNHLGameSeasons")()
	content, err := a.fetcher().Refresh(ctx, resource.YahooGames{})
	if err != nil {
		return nil, fmt.Errorf("refresh Yahoo NHL games: %w", err)
	}
	seasons := make([]int, 0, len(content.Games))
	for _, game := range content.Games {
		seasons = append(seasons, game.Season)
	}
	slices.Sort(seasons)
	return seasons, nil
}

// PlanYahooGamePlayerList decides whether the season's game player list is
// due for a download: a committed list of a season that is over is final,
// unless the manifest names another game key, which is always downloaded
// again; the current season's list is downloaded again once it is older
// than --yahoo-game-player-list-max-age.
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
// the list is downloaded again once before the season is reported missing; a
// list Fetch has just downloaded is current already.
func (a *FetchActivities) nhlGame(ctx context.Context, season int) (store.FantasyGame, bool, error) {
	fetcher := a.fetcher()
	content, origin, err := fetcher.Fetch(ctx, resource.YahooGames{})
	if err != nil {
		return store.FantasyGame{}, false, fmt.Errorf("fetch Yahoo NHL games: %w", err)
	}
	if game, ok := resource.FindNHLGame(content, season); ok || origin == core.OriginRemoteYahooAPI {
		return game, ok, nil
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
	if err := input.validate(); err != nil {
		return FetchYahooGamePlayersPageResult{}, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("game players %d start %d: %v", input.Season, input.Start, err),
			invalidGamePlayersPageErrorType, err)
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

// validate rejects a page that could be stored where no manifest can name it.
func (input FetchYahooGamePlayersPageInput) validate() error {
	switch {
	case input.Season <= 0:
		return fmt.Errorf("season %d is not a season", input.Season)
	case input.GameKey <= 0:
		return fmt.Errorf("game key %d is not a game key", input.GameKey)
	case input.DownloadID <= 0:
		return fmt.Errorf("download ID %d is not a download ID", input.DownloadID)
	case input.Start < 0:
		return fmt.Errorf("offset %d is negative", input.Start)
	}
	return nil
}

// CommitYahooGamePlayerList writes the manifest that makes the downloaded
// pages the season's game player list, once every page it names is stored
// and parses as a page of the season's game key and the pages hold the
// commit's player count, then removes the pages of the list it replaces and
// the download's discarded pages. Pages of a download that failed before
// its commit are left to DiscardYahooGamePlayerList, and those of a
// replaced manifest that no longer parses stay in place. A replaced manifest that cannot
// be read fails the commit before the write, so a retry still finds the
// pages it names.
func (a *FetchActivities) CommitYahooGamePlayerList(ctx context.Context, input CommitYahooGamePlayerListInput) error {
	manifest := resource.GamePlayerListManifest{
		DownloadID: input.DownloadID, GameKey: input.GameKey, FetchedAt: input.FetchedAt,
		Starts: input.Starts, Players: input.Players,
	}
	if err := validateGamePlayerListCommit(manifest, input.Discarded); err != nil {
		return invalidGamePlayerListError(input.Season, err)
	}
	if err := a.checkGamePlayerPages(ctx, input); err != nil {
		return err
	}
	res := resource.GamePlayerList{Season: input.Season}
	previous, hadPrevious, err := a.readReplacedGamePlayerList(ctx, res)
	if err != nil {
		return err
	}
	if err := resource.WriteParsed(ctx, a.Storage, res, manifest); err != nil {
		return fmt.Errorf("write game player list manifest %d: %w", input.Season, err)
	}
	activity.GetLogger(ctx).Info("Game player list downloaded",
		"season", input.Season, "game_key", input.GameKey, "players", input.Players, "pages", len(input.Starts))
	if hadPrevious && previous.DownloadID != input.DownloadID {
		a.deleteGamePlayerPages(ctx, input.Season, previous.DownloadID, previous.Starts)
	}
	a.deleteGamePlayerPages(ctx, input.Season, input.DownloadID, input.Discarded)
	return nil
}

// validateGamePlayerListCommit rejects a manifest Validate rejects and a
// discarded page the manifest names, which the commit would delete.
func validateGamePlayerListCommit(manifest resource.GamePlayerListManifest, discarded []int) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	named := make(map[int]bool, len(manifest.Starts))
	for _, start := range manifest.Starts {
		named[start] = true
	}
	for _, start := range discarded {
		if named[start] {
			return fmt.Errorf("discarded page offset %d is named by the list", start)
		}
	}
	return nil
}

func invalidGamePlayerListError(season int, err error) error {
	return temporal.NewNonRetryableApplicationError(
		fmt.Sprintf("commit game player list %d: %v", season, err), invalidGamePlayerListErrorType, err)
}

// checkGamePlayerPages reads every page the commit names: a missing page or
// a storage error is retryable (the page may still be written), a page of
// another season or game key, or pages holding another player count than
// the commit's, is not.
func (a *FetchActivities) checkGamePlayerPages(ctx context.Context, input CommitYahooGamePlayerListInput) error {
	players := 0
	for _, start := range input.Starts {
		page := resource.GamePlayers{
			Season: input.Season, DownloadID: input.DownloadID, Start: start, GameKey: input.GameKey,
		}
		content, err := resource.ReadParsed(ctx, a.Storage, page)
		var parseErr *resource.ParseError
		switch {
		case errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("commit game player list %d: page %s is missing", input.Season, page.Path())
		case errors.As(err, &parseErr):
			return invalidGamePlayerListError(input.Season, fmt.Errorf("page %s: %w", page.Path(), err))
		case err != nil:
			return fmt.Errorf("commit game player list %d: read page %s: %w", input.Season, page.Path(), err)
		}
		players += len(content.Game.Players.Slice)
	}
	if players != input.Players {
		return invalidGamePlayerListError(input.Season,
			fmt.Errorf("pages hold %d players, not %d", players, input.Players))
	}
	return nil
}

// readReplacedGamePlayerList returns the committed manifest a commit
// replaces. An absent manifest names no pages; one that no longer parses
// cannot say which, so it is logged and its pages are left in place.
func (a *FetchActivities) readReplacedGamePlayerList(ctx context.Context, res resource.GamePlayerList) (resource.GamePlayerListManifest, bool, error) {
	previous, err := resource.ReadParsed(ctx, a.Storage, res)
	var parseErr *resource.ParseError
	switch {
	case err == nil:
		return previous, true, nil
	case errors.Is(err, os.ErrNotExist):
		return resource.GamePlayerListManifest{}, false, nil
	case errors.As(err, &parseErr):
		activity.GetLogger(ctx).Warn("Replaced game player list manifest is unreadable, its pages stay",
			"path", res.Path(), "error", err)
		return resource.GamePlayerListManifest{}, false, nil
	default:
		return resource.GamePlayerListManifest{}, false, fmt.Errorf("read replaced game player list manifest %d: %w", res.Season, err)
	}
}

// deleteGamePlayerPages removes pages of one download that no manifest names
// any more. It is best effort: a leftover page wastes space but is never
// read again.
func (a *FetchActivities) deleteGamePlayerPages(ctx context.Context, season int, downloadID int64, starts []int) {
	for _, start := range starts {
		page := resource.GamePlayers{Season: season, DownloadID: downloadID, Start: start}
		if err := resource.Delete(ctx, a.Storage, page); err != nil {
			activity.GetLogger(ctx).Warn("Could not delete game player list page", "path", page.Path(), "error", err)
		}
	}
}

// DiscardYahooGamePlayerList deletes the pages of a download that failed
// before its commit, so they do not stay on the cache disk. When the
// season's committed manifest names that download (its commit wrote the
// manifest but the workflow did not get the result), the pages are the
// season's list and stay. A manifest that cannot be read for another reason
// than being absent or unparseable fails the activity, which is retried.
func (a *FetchActivities) DiscardYahooGamePlayerList(ctx context.Context, input DiscardYahooGamePlayerListInput) error {
	defer metrics.TrackActivityDuration("DiscardYahooGamePlayerList")()
	res := resource.GamePlayerList{Season: input.Season}
	manifest, err := resource.ReadParsed(ctx, a.Storage, res)
	var parseErr *resource.ParseError
	switch {
	case err == nil && manifest.DownloadID == input.DownloadID:
		activity.GetLogger(ctx).Info("Failed game player list download was committed, its pages stay",
			"season", input.Season, "download_id", input.DownloadID)
		return nil
	case err != nil && !errors.Is(err, os.ErrNotExist) && !errors.As(err, &parseErr):
		return fmt.Errorf("read game player list manifest %d: %w", input.Season, err)
	}
	a.deleteGamePlayerPages(ctx, input.Season, input.DownloadID, input.Starts)
	return nil
}
