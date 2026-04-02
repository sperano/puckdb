package worker

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ImportSeasonRosters imports cached season rosters for all teams into the database.
// It reads from the filesystem/gob cache populated by FetchSeasonRosters and performs
// a batch upsert of all players for each team.
func (a *SeasonsActivities) ImportSeasonRosters(ctx context.Context, input FetchSeasonRostersInput) error {
	return a.importSeasonRosters(ctx, a.RosterQueries, input)
}

// importSeasonRosters contains the core roster import logic, accepting a narrow
// interface to allow testing without a full database connection.
func (a *SeasonsActivities) importSeasonRosters(ctx context.Context, queries SeasonRosterUpserter, input FetchSeasonRostersInput) error {
	logger := activity.GetLogger(ctx)

	season := nhl.NewSeason(input.Season)
	teams, err := queries.GetSeasonTeamAbbrevs(ctx, int32(season.ID()))
	if err != nil {
		return fmt.Errorf("get season teams: %w", err)
	}

	if len(teams) == 0 {
		logger.Warn("No teams found for season", "season", input.Season, "seasonID", season.ID())
	}

	var totalPlayers int
	for _, team := range teams {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("import-roster:%s", team.Abbrev))

		res := resource.SeasonRoster{Season: input.Season, TeamAbbrev: team.Abbrev}
		if !a.Storage.Exists(res.Path()) {
			logger.Info("No roster cache for team, skipping", "team", team.Abbrev)
			continue
		}

		roster, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, res)
		if err != nil {
			return fmt.Errorf("read roster cache for %s: %w", team.Abbrev, err)
		}

		players := roster.AllPlayers()
		if len(players) == 0 {
			continue
		}

		params := make([]sqlcdb.UpsertSeasonRosterBatchParams, len(players))
		for i, p := range players {
			params[i] = sqlcdb.UpsertSeasonRosterBatchParams{
				Season:        int32(season.ID()),
				TeamID:        team.TeamID,
				PlayerID:      int64(p.ID),
				Position:      string(p.Position),
				ShootsCatches: string(p.ShootsCatches),
				SweaterNumber: int16(p.SweaterNumber),
				HeightInches:  int16(p.HeightInInches),
				WeightPounds:  int16(p.WeightInPounds),
				BirthDate:     p.BirthDate,
				BirthCity:     localizedStringToText(p.BirthCity),
				BirthStateProvince: localizedStringToText(p.BirthStateProvince),
				BirthCountry:  p.BirthCountry,
			}
		}

		var batchErr error
		results := queries.UpsertSeasonRosterBatch(ctx, params)
		results.Exec(func(i int, err error) {
			if err != nil && batchErr == nil {
				batchErr = fmt.Errorf("player %d (%s): %w", params[i].PlayerID, team.Abbrev, err)
			}
		})
		if batchErr != nil {
			return fmt.Errorf("upsert roster for %s: %w", team.Abbrev, batchErr)
		}

		totalPlayers += len(players)
	}

	logger.Info("Imported season rosters", "season", input.Season, "teams", len(teams), "players", totalPlayers)
	return nil
}

// localizedStringToText converts a *nhl.LocalizedString to a pgtype.Text using the Default field.
func localizedStringToText(ls *nhl.LocalizedString) pgtype.Text {
	if ls == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: ls.Default, Valid: true}
}
