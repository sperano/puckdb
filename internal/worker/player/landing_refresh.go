package player

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"

	"github.com/sperano/puckdb/internal/resource"
)

// landingCacheState is what the cache holds of one player's landing page.
type landingCacheState int

const (
	// landingAbsent: no cached page, it must be downloaded.
	landingAbsent landingCacheState = iota
	// landingCurrent: the cached page is used as is: LandingMaxAge is zero,
	// the page is younger than LandingMaxAge, or it is final (an inactive
	// player's).
	landingCurrent
	// landingStale: a page older than LandingMaxAge that is an active
	// player's or does not parse; it is downloaded again.
	landingStale
)

// cachedLandingState tells whether the cached landing page of res can be
// used as is. A landing's seasonTotals are the source of
// player_season_totals, so an active player's page is downloaded again once
// older than LandingMaxAge (its current season's totals change every game);
// an inactive player's page is final.
func (a *Activities) cachedLandingState(ctx context.Context, res resource.PlayerLanding) (landingCacheState, error) {
	info, err := resource.Stat(ctx, a.Storage, res)
	if errors.Is(err, os.ErrNotExist) {
		return landingAbsent, nil
	}
	if err != nil {
		return 0, fmt.Errorf("stat player %s landing: %w", res.PlayerID.String(), err)
	}
	if a.LandingMaxAge <= 0 || a.now().Sub(info.ModTime()) < a.LandingMaxAge {
		return landingCurrent, nil
	}
	landing, err := resource.ReadParsed(ctx, a.Storage, res)
	var parseErr *resource.ParseError
	switch {
	case errors.As(err, &parseErr):
		return landingStale, nil
	case err != nil:
		return 0, fmt.Errorf("read player %s landing: %w", res.PlayerID.String(), err)
	case !landing.IsActive:
		return landingCurrent, nil
	}
	return landingStale, nil
}

// keepStaleLanding handles a failed refresh (downloadErr) of the stale
// cached page res: the page was served before, so it is kept rather than
// the batch failed or the player marked missing (which would drop their
// stored totals). A 404 rewrites the page's own bytes so its age restarts
// and the NHL is asked again only after LandingMaxAge; any other error
// leaves the page as is, to be retried on the next run. Cancellation is
// returned.
func (a *Activities) keepStaleLanding(
	ctx context.Context,
	res resource.PlayerLanding,
	downloadErr error,
) (playerLandingStatus, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	playerID := res.PlayerID.String()
	if !errors.Is(downloadErr, nhl.ErrNotFound) {
		log.Warn().Err(downloadErr).Str("player_id", playerID).
			Msg("Stale player landing refresh failed, using the cached page")
		return playerLandingCached, nil
	}
	data, err := resource.Read(ctx, a.Storage, res)
	if err != nil {
		return 0, fmt.Errorf("read stale player %s landing: %w", playerID, err)
	}
	if err := resource.Write(ctx, a.Storage, res, data); err != nil {
		return 0, fmt.Errorf("renew stale player %s landing: %w", playerID, err)
	}
	log.Warn().Str("player_id", playerID).Msg("Stale player landing answered 404, keeping the cached page")
	return playerLandingCached, nil
}
