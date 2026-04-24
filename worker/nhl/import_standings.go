package nhl

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	nhl "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/worker/shared"
	"go.temporal.io/sdk/activity"
)

// ImportStandingsForDate imports the standings snapshot for a single date from
// the cache into the database. It is a no-op when no standings file exists for
// that date (e.g. off-season days).
func (a *ImportActivities) ImportStandingsForDate(ctx context.Context, input shared.DateSeasonInput) error {
	standingsRes := resource.DailyStandings{Date: input.Date}
	if !a.Storage.Exists(ctx, standingsRes.Path()) {
		return nil
	}

	standings, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, standingsRes)
	if err != nil {
		return fmt.Errorf("read standings for %s: %w", input.Date.Format(config.DateFormat), err)
	}

	if len(standings) == 0 {
		return nil
	}

	seasonID := int32(nhl.NewSeason(input.Season).ID())

	// Build abbreviation → team_id map for this season.
	teamRows, err := a.Queries.GetSeasonTeamAbbrevs(ctx, seasonID)
	if err != nil {
		return fmt.Errorf("get season team abbrevs for %d: %w", seasonID, err)
	}
	abbrevToTeamID := make(map[string]int64, len(teamRows))
	for _, row := range teamRows {
		abbrevToTeamID[row.Abbrev] = row.TeamID
	}

	pgDate := pgtype.Date{Time: input.Date, Valid: true}
	params := make([]sqlcdb.UpsertStandingsSnapshotBatchParams, 0, len(standings))
	for _, s := range standings {
		abbrev := s.TeamAbbrev.Default
		teamID, ok := abbrevToTeamID[abbrev]
		if !ok {
			return fmt.Errorf("no team_id for abbreviation %q in season %d", abbrev, seasonID)
		}

		var confAbbrev, confName pgtype.Text
		if s.ConferenceAbbrev != nil {
			confAbbrev = pgtype.Text{String: *s.ConferenceAbbrev, Valid: true}
		}
		if s.ConferenceName != nil {
			confName = pgtype.Text{String: *s.ConferenceName, Valid: true}
		}

		params = append(params, sqlcdb.UpsertStandingsSnapshotBatchParams{
			Season:           seasonID,
			Date:             pgDate,
			TeamID:           teamID,
			TeamAbbrev:       abbrev,
			Wins:             int32(s.Wins),
			Losses:           int32(s.Losses),
			OtLosses:         int32(s.OTLosses),
			Points:           int32(s.Points),
			DivisionAbbrev:   s.DivisionAbbrev,
			DivisionName:     s.DivisionName,
			ConferenceAbbrev: confAbbrev,
			ConferenceName:   confName,
		})
	}

	if err := shared.ExecBatch(a.Queries.UpsertStandingsSnapshotBatch(ctx, params), func(i int) string {
		return fmt.Sprintf("standings %s team %s", input.Date.Format(config.DateFormat), params[i].TeamAbbrev)
	}); err != nil {
		return err
	}

	logger := activity.GetLogger(ctx)
	logger.Info("Imported standings snapshot",
		"date", input.Date.Format(config.DateFormat),
		"teams", len(params))
	return nil
}
