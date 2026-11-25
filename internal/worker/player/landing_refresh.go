package player

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/sperano/puckdb/internal/resource"
)

// landingCacheState is what the cache holds of one player's landing page.
type landingCacheState int

const (
	// landingAbsent: no cached page, it must be downloaded.
	landingAbsent landingCacheState = iota
	// landingCurrent: the cached page is younger than LandingMaxAge, or
	// final (an inactive player's).
	landingCurrent
	// landingStale: an active player's page older than LandingMaxAge, or an
	// unreadable one; it is downloaded again.
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
