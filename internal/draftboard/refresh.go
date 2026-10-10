package draftboard

import (
	"context"
	"errors"
	"fmt"

	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/draftwatch"
)

// ErrRefreshUnavailable reports that a synchronization this process cannot
// signal holds the league's session lock: a CLI or another API process
// watching the league, or a refresh already in progress. That holder keeps
// updating the persisted board, so the refresh is refused rather than failed.
var ErrRefreshUnavailable = errors.New("refresh now is unavailable")

// Refresh polls Yahoo for a league immediately. When this process watches the
// league, the poll runs on that watch's loop, which holds the session lock;
// otherwise it is a one-shot poll through the shared draftwatch runner.
func (s *Service) Refresh(ctx context.Context, league string, season int) (draftwatch.Session, draftsession.Report, error) {
	identity, err := s.ResolveIdentity(ctx, league, season)
	if err != nil {
		return draftwatch.Session{}, draftsession.Report{}, err
	}
	if s.watch != nil {
		outcome, routed, err := s.watch.Refresh(ctx, identity.LeagueKey)
		if err != nil {
			return draftwatch.Session{}, draftsession.Report{}, err
		}
		if routed {
			return outcome.Session, outcome.Report, outcome.Err
		}
	}
	if s.refresher == nil {
		return draftwatch.Session{}, draftsession.Report{}, errors.New("draft refresh runner is unavailable")
	}
	session, report, err := s.refresher.SyncOnce(ctx, identity)
	if errors.Is(err, draftwatch.ErrSyncInProgress) {
		return draftwatch.Session{}, draftsession.Report{}, fmt.Errorf(
			"%w: another Yahoo draft sync for %s is running (a CLI or another API process watching the league, "+
				"or a refresh already in progress); it keeps updating this board, try again once it stops",
			ErrRefreshUnavailable, identity.LeagueKey)
	}
	return session, report, err
}
