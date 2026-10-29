package nhl

import (
	"context"
	"slices"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"go.temporal.io/sdk/activity"
)

// SeasonSkaterManifest is the one list of skaters whose game logs
// ImportPlayerLogsWorkflow imports for a season: the parent sizes the
// season's bar from PlayerIDs and passes the same IDs to the season's child,
// so both count the same unique players. The other fields report the cached
// files the scan could not use, so a season built from an incomplete cache
// says so instead of completing silently.
type SeasonSkaterManifest struct {
	// PlayerIDs are the unique skaters (forwards and defensemen, no goalies)
	// of the season's final, non-preseason boxscores, ascending.
	PlayerIDs []int64 `json:"playerIDs"`
	// UnreadableSchedules counts the cached daily schedules that failed to
	// read; their games are not scanned.
	UnreadableSchedules int `json:"unreadableSchedules,omitempty"`
	// MissingBoxscores counts the final games whose boxscore is not cached.
	MissingBoxscores int `json:"missingBoxscores,omitempty"`
	// UnreadableBoxscores counts the cached boxscores that failed to read.
	UnreadableBoxscores int `json:"unreadableBoxscores,omitempty"`
}

// Incomplete reports whether the scan skipped any schedule or boxscore.
func (m SeasonSkaterManifest) Incomplete() bool {
	return m.UnreadableSchedules > 0 || m.MissingBoxscores > 0 || m.UnreadableBoxscores > 0
}

// CollectSeasonSkaters scans the season's cached daily schedules and
// boxscores, from the season start through input.EndDate, and returns the
// season's SeasonSkaterManifest.
func (a *ImportActivities) CollectSeasonSkaters(ctx context.Context, input CollectSeasonPlayerIDsInput) (SeasonSkaterManifest, error) {
	defer metrics.TrackActivityDuration("CollectSeasonSkaters")()

	var manifest SeasonSkaterManifest
	seen := make(map[int64]struct{})
	for d := input.Season.StandingsStart.Time; !d.After(input.EndDate); d = d.AddDate(0, 0, 1) {
		if err := ctx.Err(); err != nil {
			return SeasonSkaterManifest{}, err
		}
		a.collectDaySkaters(ctx, d, seen, &manifest)
		activity.RecordHeartbeat(ctx, d.Format(config.DateFormat))
	}

	manifest.PlayerIDs = make([]int64, 0, len(seen))
	for id := range seen {
		manifest.PlayerIDs = append(manifest.PlayerIDs, id)
	}
	slices.Sort(manifest.PlayerIDs)
	return manifest, nil
}

// collectDaySkaters adds the skaters of one day's final, non-preseason
// boxscores to seen and counts the files it could not use in manifest.
func (a *ImportActivities) collectDaySkaters(ctx context.Context, day time.Time, seen map[int64]struct{}, manifest *SeasonSkaterManifest) {
	scheduleRes := resource.DailySchedule{Date: day}
	if !resource.Exists(ctx, a.Storage, scheduleRes) {
		return
	}
	schedule, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, scheduleRes)
	if err != nil {
		log.Warn().Err(err).Str("path", scheduleRes.Path()).Msg("Unreadable daily schedule; its games are not scanned for skaters")
		manifest.UnreadableSchedules++
		return
	}

	for _, game := range schedule.Games {
		if shouldSkipGame(game) {
			continue
		}
		boxscoreRes := resource.Boxscore{Date: day, GameID: game.ID}
		if !resource.Exists(ctx, a.Storage, boxscoreRes) {
			log.Warn().Str("path", boxscoreRes.Path()).Msg("Final game has no cached boxscore; its skaters are not listed")
			manifest.MissingBoxscores++
			continue
		}
		boxscore, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, boxscoreRes)
		if err != nil {
			log.Warn().Err(err).Str("path", boxscoreRes.Path()).Msg("Unreadable boxscore; its skaters are not listed")
			manifest.UnreadableBoxscores++
			continue
		}
		collectPlayerIDs(boxscore, seen)
	}
}
