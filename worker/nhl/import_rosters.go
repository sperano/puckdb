package nhl

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/matching"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/worker/shared"
	"go.temporal.io/sdk/activity"
)

// ImportSeasonRosters imports cached season rosters for all teams into the database.
func (a *SeasonsActivities) ImportSeasonRosters(ctx context.Context, input FetchSeasonRostersInput) error {
	return a.importSeasonRosters(ctx, a.RosterQueries, input)
}

// importSeasonRosters contains the core roster import logic, accepting a narrow
// interface to allow testing without a full database connection.
func (a *SeasonsActivities) importSeasonRosters(ctx context.Context, queries SeasonRosterUpserter, input FetchSeasonRostersInput) error {
	logger := activity.GetLogger(ctx)

	season := nhlapi.NewSeason(input.Season)
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

		if err := ensureRosterPlayersExist(ctx, queries, players, team.Abbrev); err != nil {
			return err
		}

		params := make([]sqlcdb.UpsertSeasonRosterBatchParams, len(players))
		for i, p := range players {
			params[i] = sqlcdb.UpsertSeasonRosterBatchParams{
				Season:             int32(season.ID()),
				TeamID:             team.TeamID,
				PlayerID:           int64(p.ID),
				Position:           sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPosition(p.Position), Valid: p.Position != ""},
				ShootsCatches:      string(p.ShootsCatches),
				SweaterNumber:      int16(p.SweaterNumber),
				HeightInches:       int16(p.HeightInInches),
				WeightPounds:       int16(p.WeightInPounds),
				BirthDate:          p.BirthDate,
				BirthCity:          localizedStringToText(p.BirthCity),
				BirthStateProvince: localizedStringToText(p.BirthStateProvince),
				BirthCountry:       p.BirthCountry,
			}
		}

		if err := shared.ExecBatch(queries.UpsertSeasonRosterBatch(ctx, params), func(i int) string {
			return fmt.Sprintf("player %d (%s)", params[i].PlayerID, team.Abbrev)
		}); err != nil {
			return fmt.Errorf("upsert roster for %s: %w", team.Abbrev, err)
		}

		totalPlayers += len(players)
	}

	logger.Info("Imported season rosters", "season", input.Season, "teams", len(teams), "players", totalPlayers)
	return nil
}

// ensureRosterPlayersExist creates stub player records for any roster players
// not yet in the players table.
func ensureRosterPlayersExist(ctx context.Context, queries SeasonRosterUpserter, players []nhlapi.RosterPlayer, teamAbbrev string) error {
	params := make([]sqlcdb.EnsurePlayerExistsBatchParams, len(players))
	for i, p := range players {
		firstName := strings.TrimSpace(p.FirstName.Default)
		lastName := strings.TrimSpace(p.LastName.Default)

		params[i] = sqlcdb.EnsurePlayerExistsBatchParams{
			ID:                  int64(p.ID),
			FirstName:           firstName,
			LastName:            lastName,
			FirstNameNormalized: matching.NormalizeName(firstName),
			LastNameNormalized:  matching.NormalizeName(lastName),
			Position:            sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPosition(p.Position), Valid: string(p.Position) != ""},
			ShootsCatches:       sqlcdb.NullHandSide{HandSide: sqlcdb.HandSide(p.ShootsCatches), Valid: string(p.ShootsCatches) != ""},
			HeadshotURL:         strings.TrimSpace(p.Headshot),
			HeightInches:        pgtype.Int4{Int32: int32(p.HeightInInches), Valid: p.HeightInInches > 0},
			WeightPounds:        pgtype.Int4{Int32: int32(p.WeightInPounds), Valid: p.WeightInPounds > 0},
			BirthCity:           localizedStringToText(p.BirthCity),
			BirthStateProvince:  localizedStringToText(p.BirthStateProvince),
			BirthCountry:        pgtype.Text{String: p.BirthCountry, Valid: p.BirthCountry != ""},
			SweaterNumber:       pgtype.Int4{Int32: int32(p.SweaterNumber), Valid: p.SweaterNumber > 0},
		}

		params[i].BirthDate = shared.ParseDateToPgDate(p.BirthDate)
	}

	return shared.ExecBatch(queries.EnsurePlayerExistsBatch(ctx, params), func(i int) string {
		return fmt.Sprintf("ensure player %d (%s)", params[i].ID, teamAbbrev)
	})
}

// localizedStringToText converts a *nhlapi.LocalizedString to a pgtype.Text using the Default field.
func localizedStringToText(ls *nhlapi.LocalizedString) pgtype.Text {
	if ls == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: ls.Default, Valid: true}
}
