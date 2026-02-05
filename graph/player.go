package graph

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/graph/model"
	"github.com/sperano/puckdb/sqlcdb"
)

func sqlcPlayerRowToGQL(row sqlcdb.ListPlayersRow) *model.Player {
	player := &model.Player{
		ID:              row.ID,
		FirstName:       row.FirstName,
		LastName:        row.LastName,
		Position:        row.Position,
		ShootsCatches:   row.ShootsCatches,
		IsActive:        row.IsActive,
		HeadshotURL:     row.HeadshotURL,
		YahooImageSmall: row.YahooImageSmall,
		YahooImageMedium: row.YahooImageMedium,
		YahooImageLarge: row.YahooImageLarge,
		YahooHomeURL:    row.YahooHomeURL,
	}

	// Handle nullable fields
	if row.YahooID.Valid {
		player.YahooID = &row.YahooID.Int64
	}
	if row.HeightInches.Valid {
		v := int(row.HeightInches.Int32)
		player.HeightInches = &v
	}
	if row.WeightPounds.Valid {
		v := int(row.WeightPounds.Int32)
		player.WeightPounds = &v
	}
	if row.BirthDate.Valid {
		v := row.BirthDate.Time.Format("2006-01-02")
		player.BirthDate = &v
	}
	if row.BirthCity.Valid {
		player.BirthCity = &row.BirthCity.String
	}
	if row.BirthStateProvince.Valid {
		player.BirthStateProvince = &row.BirthStateProvince.String
	}
	if row.BirthCountry.Valid {
		player.BirthCountry = &row.BirthCountry.String
	}
	if row.SweaterNumber.Valid {
		v := int(row.SweaterNumber.Int32)
		player.SweaterNumber = &v
	}
	if row.HeroImageURL.Valid {
		player.HeroImageURL = &row.HeroImageURL.String
	}
	if row.PlayerSlug.Valid {
		player.PlayerSlug = &row.PlayerSlug.String
	}
	if row.DraftYear.Valid {
		v := int(row.DraftYear.Int32)
		player.DraftYear = &v
	}
	if row.DraftTeamAbbrev.Valid {
		player.DraftTeamAbbrev = &row.DraftTeamAbbrev.String
	}
	if row.DraftRound.Valid {
		v := int(row.DraftRound.Int32)
		player.DraftRound = &v
	}
	if row.DraftPickInRound.Valid {
		v := int(row.DraftPickInRound.Int32)
		player.DraftPickInRound = &v
	}
	if row.DraftOverallPick.Valid {
		v := int(row.DraftOverallPick.Int32)
		player.DraftOverallPick = &v
	}

	// Handle NHL team if present
	if row.NHLTeamID.Valid && row.TeamName.Valid {
		player.NHLTeam = &model.NHLTeam{
			ID:           int(row.NHLTeamID.Int64),
			City:         row.TeamCity.String,
			Name:         row.TeamName.String,
			Abbreviation: row.TeamAbbrev.String,
		}
	}

	return player
}

func listPlayers(ctx context.Context, q *sqlcdb.Queries, filter *model.PlayersFilter) ([]*model.Player, error) {
	params := sqlcdb.ListPlayersParams{}

	if filter != nil {
		if filter.Name != nil {
			params.Name = pgtype.Text{String: *filter.Name, Valid: true}
		}
		if filter.SweaterNumber != nil {
			params.SweaterNumber = pgtype.Int4{Int32: int32(*filter.SweaterNumber), Valid: true}
		}
		if filter.HasYahooID != nil {
			params.HasYahooID = pgtype.Bool{Bool: *filter.HasYahooID, Valid: true}
		}
		if filter.TeamID != nil {
			params.TeamID = pgtype.Int8{Int64: int64(*filter.TeamID), Valid: true}
		}
		if filter.Position != nil {
			params.Position = pgtype.Text{String: *filter.Position, Valid: true}
		}
		if filter.IsActive != nil {
			params.IsActive = pgtype.Bool{Bool: *filter.IsActive, Valid: true}
		}
	}

	rows, err := q.ListPlayers(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list players: %w", err)
	}

	players := make([]*model.Player, len(rows))
	for i, row := range rows {
		players[i] = sqlcPlayerRowToGQL(row)
	}

	return players, nil
}
