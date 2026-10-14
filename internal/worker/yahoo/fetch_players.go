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
)

// MaxLeaguePlayerPoolPages caps a pool download so a Yahoo response that
// never ends cannot loop forever: 200 pages of 25 is 5,000 players, about
// twice the NHL player universe Yahoo lists.
const MaxLeaguePlayerPoolPages = 200

// seasonEndGraceDays keeps the pool refreshing through the league's last day.
const seasonEndGraceDays = 1

// PlanYahooLeaguePlayerPoolInput names the league whose pool may need a
// fresh download.
type PlanYahooLeaguePlayerPoolInput struct {
	Season   int `json:"season"`
	LeagueID int `json:"leagueId"`
}

// YahooLeaguePlayerPoolPlan says whether to download the pool again.
// PreviousPlayerCount is the player count of the last committed snapshot (0
// when there is none or it is unreadable), which sizes the download's
// progress before its pages arrive.
type YahooLeaguePlayerPoolPlan struct {
	Refresh             bool   `json:"refresh"`
	Reason              string `json:"reason"`
	PreviousPlayerCount int    `json:"previousPlayerCount,omitempty"`
}

// FetchYahooLeaguePlayersPageInput names one page of one pool download.
type FetchYahooLeaguePlayersPageInput struct {
	Season     int   `json:"season"`
	LeagueID   int   `json:"leagueId"`
	DownloadID int64 `json:"downloadId"`
	Start      int   `json:"start"`
}

// FetchYahooLeaguePlayersPageResult reports what one page held.
type FetchYahooLeaguePlayersPageResult struct {
	Unavailable bool   `json:"unavailable,omitempty"`
	Players     int    `json:"players"`
	LeagueKey   string `json:"leagueKey"`
	GameKey     int    `json:"gameKey"`
}

// CommitYahooLeaguePlayerPoolInput records a complete pool download.
type CommitYahooLeaguePlayerPoolInput struct {
	Season     int       `json:"season"`
	LeagueID   int       `json:"leagueId"`
	DownloadID int64     `json:"downloadId"`
	LeagueKey  string    `json:"leagueKey"`
	GameKey    int       `json:"gameKey"`
	FetchedAt  time.Time `json:"fetchedAt"`
	Starts     []int     `json:"starts"`
	Players    int       `json:"players"`
	// Discarded are this download's pages fetched concurrently past its last
	// page. No manifest names them, so the commit deletes them.
	Discarded []int `json:"discarded,omitempty"`
}

// PlanYahooLeaguePlayerPool decides whether the league's pool is due for a
// download: not once the league's season is over, nor while the last
// complete download is younger than the max age for its season phase.
func (a *FetchActivities) PlanYahooLeaguePlayerPool(ctx context.Context, input PlanYahooLeaguePlayerPoolInput) (YahooLeaguePlayerPoolPlan, error) {
	return a.planYahooLeaguePlayerPool(ctx, input, time.Now())
}

func (a *FetchActivities) planYahooLeaguePlayerPool(ctx context.Context, input PlanYahooLeaguePlayerPoolInput, now time.Time) (YahooLeaguePlayerPoolPlan, error) {
	league, _, err := readLeagueSettings(ctx, a.Storage, resource.League{Season: input.Season, LeagueID: input.LeagueID})
	if err != nil {
		return YahooLeaguePlayerPoolPlan{}, err
	}
	if leagueSeasonEnded(league, now) {
		return YahooLeaguePlayerPoolPlan{Reason: fmt.Sprintf("league season ended %s", league.EndDate)}, nil
	}
	manifest, err := resource.ReadParsed(ctx, a.Storage, resource.LeaguePlayerPool{Season: input.Season, LeagueID: input.LeagueID})
	if errors.Is(err, os.ErrNotExist) {
		return YahooLeaguePlayerPoolPlan{Refresh: true, Reason: "pool never downloaded"}, nil
	}
	var parseErr *resource.ParseError
	if errors.As(err, &parseErr) {
		return YahooLeaguePlayerPoolPlan{Refresh: true, Reason: "pool manifest unreadable"}, nil
	}
	if err != nil {
		return YahooLeaguePlayerPoolPlan{}, fmt.Errorf("read player pool manifest %d/%d: %w", input.Season, input.LeagueID, err)
	}
	maxAge := yahooPlayerPoolMaxAge(league, now)
	if age := now.Sub(manifest.FetchedAt); age < maxAge {
		return YahooLeaguePlayerPoolPlan{
			Reason:              fmt.Sprintf("pool downloaded %s ago", age.Truncate(time.Minute)),
			PreviousPlayerCount: manifest.Players,
		}, nil
	}
	return YahooLeaguePlayerPoolPlan{
		Refresh: true, Reason: "pool older than " + maxAge.String(), PreviousPlayerCount: manifest.Players,
	}, nil
}

// yahooPlayerPoolMaxAge keeps full pool snapshots frequent through the draft
// window, then relaxes the cadence when eligibility changes less often. A
// later refinement could refresh the top ownership-ranked pages daily while
// refreshing the complete pool less often.
func yahooPlayerPoolMaxAge(league store.League, now time.Time) time.Duration {
	flagName := config.FlagYahooPlayerPoolMaxAge
	fallback := config.DefaultYahooPlayerPoolMaxAge
	if leagueRegularSeasonStarted(league, now) {
		flagName = config.FlagYahooPlayerPoolMaxAgeInSeason
		fallback = config.DefaultYahooPlayerPoolMaxAgeInSeason
	}
	return time.Duration(shared.ViperIntOrDefault(flagName, fallback)) * time.Hour
}

// leagueRegularSeasonStarted reports whether the Yahoo league's start-date
// day has begun. Missing or invalid dates retain the preseason cadence.
func leagueRegularSeasonStarted(league store.League, now time.Time) bool {
	start, err := time.Parse(config.DateFormat, league.StartDate)
	return err == nil && !now.Before(start)
}

// leagueSeasonEnded reports whether the league's last day is over. A league
// without a readable end date is treated as current.
func leagueSeasonEnded(league store.League, now time.Time) bool {
	end, err := time.Parse(config.DateFormat, league.EndDate)
	return err == nil && now.After(end.AddDate(0, 0, seasonEndGraceDays))
}

// FetchYahooLeaguePlayersPage downloads one page of the league's pool, even
// when a copy is cached: eligibility and status change during the preseason.
func (a *FetchActivities) FetchYahooLeaguePlayersPage(ctx context.Context, input FetchYahooLeaguePlayersPageInput) (FetchYahooLeaguePlayersPageResult, error) {
	defer metrics.TrackActivityDuration("FetchYahooLeaguePlayersPage")()
	gameKey, err := GetGameKeyForSeason(ctx, a.Storage, a.GobCache, a.Download, input.Season)
	if err != nil {
		return FetchYahooLeaguePlayersPageResult{}, err
	}
	res := resource.LeaguePlayers{
		Season: input.Season, LeagueID: input.LeagueID, DownloadID: input.DownloadID, Start: input.Start, GameKey: gameKey,
	}
	content, err := a.fetcher().Refresh(ctx, res)
	if err != nil {
		if input.Start == 0 && isPreseasonResourceUnavailable(err) {
			activity.GetLogger(ctx).Warn("Yahoo league player pool is not available yet",
				"season", input.Season, "leagueID", input.LeagueID)
			return FetchYahooLeaguePlayersPageResult{Unavailable: true}, nil
		}
		return FetchYahooLeaguePlayersPageResult{}, fmt.Errorf("fetch league players %d/%d start %d: %w",
			input.Season, input.LeagueID, input.Start, err)
	}
	players := len(content.League.Players.Slice)
	activity.GetLogger(ctx).Info("League players page downloaded",
		"season", input.Season, "leagueID", input.LeagueID, "start", input.Start, "players", players)
	return FetchYahooLeaguePlayersPageResult{Players: players, LeagueKey: content.League.Key, GameKey: gameKey}, nil
}

// CommitYahooLeaguePlayerPool writes the manifest that makes the downloaded
// pages the league's current pool snapshot, then removes the pages of the
// snapshot it replaces and the download's discarded pages. Pages of
// downloads that failed before their commit are never named by a manifest
// and are left in place.
func (a *FetchActivities) CommitYahooLeaguePlayerPool(ctx context.Context, input CommitYahooLeaguePlayerPoolInput) error {
	res := resource.LeaguePlayerPool{Season: input.Season, LeagueID: input.LeagueID}
	previous, hadPrevious := a.readPreviousManifest(ctx, res)
	manifest := resource.LeaguePlayerPoolManifest{
		DownloadID: input.DownloadID, LeagueKey: input.LeagueKey, GameKey: input.GameKey,
		FetchedAt: input.FetchedAt, Starts: input.Starts, Players: input.Players,
	}
	if err := resource.WriteParsed(ctx, a.Storage, res, manifest); err != nil {
		return fmt.Errorf("write player pool manifest %d/%d: %w", input.Season, input.LeagueID, err)
	}
	logger := activity.GetLogger(ctx)
	logger.Info("League player pool downloaded",
		"season", input.Season, "leagueID", input.LeagueID, "players", input.Players, "pages", len(input.Starts))
	if hadPrevious && previous.DownloadID != input.DownloadID {
		a.deletePoolPages(ctx, input.Season, input.LeagueID, previous.DownloadID, previous.Starts)
	}
	a.deletePoolPages(ctx, input.Season, input.LeagueID, input.DownloadID, input.Discarded)
	return nil
}

// readPreviousManifest returns the committed manifest the commit replaces;
// an absent or unreadable one has no pages to clean up.
func (a *FetchActivities) readPreviousManifest(ctx context.Context, res resource.LeaguePlayerPool) (resource.LeaguePlayerPoolManifest, bool) {
	previous, err := resource.ReadParsed(ctx, a.Storage, res)
	return previous, err == nil
}

// deletePoolPages removes pages of one download that no manifest names any
// more. It is best effort: a leftover page wastes space but is never read
// again.
func (a *FetchActivities) deletePoolPages(ctx context.Context, season, leagueID int, downloadID int64, starts []int) {
	for _, start := range starts {
		page := resource.LeaguePlayers{Season: season, LeagueID: leagueID, DownloadID: downloadID, Start: start}
		if err := resource.Delete(ctx, a.Storage, page); err != nil {
			activity.GetLogger(ctx).Warn("Could not delete replaced player pool page", "path", page.Path(), "error", err)
		}
	}
}
