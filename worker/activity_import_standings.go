package worker

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// StandingsUpserter is the interface for standings database operations.
type StandingsUpserter interface {
	UpsertStandingsSnapshotBatch(ctx context.Context, arg []sqlcdb.UpsertStandingsSnapshotBatchParams) *sqlcdb.UpsertStandingsSnapshotBatchBatchResults
}

// ImportStandingsForDate imports the standings snapshot for a single date from
// the cache into the database. It is a no-op when no standings file exists for
// that date (e.g. off-season days).
func (a *SeasonsActivities) ImportStandingsForDate(ctx context.Context, input DateSeasonInput) error {
	standingsRes := resource.DailyStandings{Date: input.Date}
	if !a.Storage.Exists(standingsRes.Path()) {
		return nil
	}

	standings, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, standingsRes)
	if err != nil {
		return fmt.Errorf("read standings for %s: %w", input.Date.Format(config.DateFormat), err)
	}

	if len(standings) == 0 {
		return nil
	}

	pgDate := pgtype.Date{Time: input.Date, Valid: true}
	params := make([]sqlcdb.UpsertStandingsSnapshotBatchParams, len(standings))
	for i, s := range standings {
		var confAbbrev, confName pgtype.Text
		if s.ConferenceAbbrev != nil {
			confAbbrev = pgtype.Text{String: *s.ConferenceAbbrev, Valid: true}
		}
		if s.ConferenceName != nil {
			confName = pgtype.Text{String: *s.ConferenceName, Valid: true}
		}

		params[i] = sqlcdb.UpsertStandingsSnapshotBatchParams{
			Season:           int32(input.Season),
			Date:             pgDate,
			TeamAbbrev:       s.TeamAbbrev.Default,
			Wins:             int32(s.Wins),
			Losses:           int32(s.Losses),
			OtLosses:         int32(s.OTLosses),
			Points:           int32(s.Points),
			DivisionAbbrev:   s.DivisionAbbrev,
			DivisionName:     s.DivisionName,
			ConferenceAbbrev: confAbbrev,
			ConferenceName:   confName,
		}
	}

	var batchErr error
	results := a.ImportQueries.UpsertStandingsSnapshotBatch(ctx, params)
	results.Exec(func(i int, err error) {
		if err != nil && batchErr == nil {
			batchErr = fmt.Errorf("standings %s team %s: %w",
				input.Date.Format(config.DateFormat), params[i].TeamAbbrev, err)
		}
	})
	if batchErr != nil {
		return batchErr
	}

	logger := activity.GetLogger(ctx)
	logger.Info("Imported standings snapshot",
		"date", input.Date.Format(config.DateFormat),
		"teams", len(params))
	return nil
}
