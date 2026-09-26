package cmd

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/worker/draftranking"
	"go.temporal.io/sdk/worker"
)

// registerDraftRankingActivities registers the draft ranking refresh
// activities.
func registerDraftRankingActivities(w worker.Worker, pool *pgxpool.Pool) {
	activities := &draftranking.Activities{Pool: pool}
	w.RegisterActivity(activities.RefreshDraftRanking)
	w.RegisterActivity(activities.CancelDraftRankingRefreshes)
}
