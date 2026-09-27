// Package draftranking holds the Temporal activities of the draft ranking
// refresh: computing and storing one league's snapshot, and marking a
// canceled run's unfinished attempts.
package draftranking

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/news"
	"go.temporal.io/sdk/activity"
)

// Activities are the draft ranking refresh activities.
type Activities struct {
	Pool *pgxpool.Pool
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// RefreshInput asks for one league's snapshot.
type RefreshInput struct {
	RunID    string            `json:"runId"`
	Season   int               `json:"season"`
	LeagueID int               `json:"leagueId"`
	Options  draftrank.Options `json:"options"`
	Keep     int               `json:"keep"`
	Sources  []news.Source     `json:"sources"`
}

// RefreshDraftRanking computes and stores one league's snapshot, recording
// the attempt either way. A refresh that fails for a known reason (missing
// rules, pool or projections, unsupported scoring, a ranking error) is
// returned as a failed outcome without an error, so it is not retried; an
// internal failure is returned as an error and retried by Temporal. The
// refresh heartbeats its current step when the step starts and every
// heartbeatInterval while it runs, which is how a workflow cancellation
// reaches it and keeps a slow step from being timed out.
func (a *Activities) RefreshDraftRanking(ctx context.Context, input RefreshInput) (draftrank.Refresh, error) {
	refresher := draftrank.NewRefresher(a.Pool)
	if a.Now != nil {
		refresher.Now = a.Now
	}
	step, stop := startPulse(ctx, heartbeatInterval, func(step string) { activity.RecordHeartbeat(ctx, step) })
	defer stop()
	refresher.Heartbeat = step
	outcome, err := refresher.Refresh(ctx, draftrank.RefreshRequest{
		RunID: input.RunID, Season: input.Season, LeagueID: input.LeagueID,
		Options: input.Options, Keep: input.Keep, Sources: input.Sources,
	})
	var coded *draftrank.RefreshError
	if errors.As(err, &coded) && coded.Code != draftrank.IssueInternalError {
		return outcome, nil
	}
	return outcome, err
}

// CancelInput names a canceled run.
type CancelInput struct {
	RunID string `json:"runId"`
}

// CancelDraftRankingRefreshes marks the run's unfinished attempts canceled,
// for attempts whose activity stopped before recording an outcome.
func (a *Activities) CancelDraftRankingRefreshes(ctx context.Context, input CancelInput) (int64, error) {
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	return draftrank.NewPGStore(a.Pool).CancelRefreshes(ctx, input.RunID, now().UTC())
}
