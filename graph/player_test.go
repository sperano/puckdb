package graph

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/stretchr/testify/assert"
)

func TestSqlcPlayerToGQL_AllFields(t *testing.T) {
	t.Parallel()

	birthDate := time.Date(1995, 7, 13, 0, 0, 0, 0, time.UTC)
	row := sqlcdb.Player{
		ID:                 8478402,
		YahooID:            pgtype.Int8{Int64: 5441, Valid: true},
		FirstName:          "Connor",
		LastName:           "McDavid",
		TeamID:             pgtype.Int8{Int64: 22, Valid: true},
		Position:           "C",
		ShootsCatches:      "L",
		HeightInches:       pgtype.Int4{Int32: 73, Valid: true},
		WeightPounds:       pgtype.Int4{Int32: 193, Valid: true},
		BirthDate:          pgtype.Date{Time: birthDate, Valid: true},
		BirthCity:          pgtype.Text{String: "Richmond Hill", Valid: true},
		BirthStateProvince: pgtype.Text{String: "ON", Valid: true},
		BirthCountry:       pgtype.Text{String: "CAN", Valid: true},
		SweaterNumber:      pgtype.Int4{Int32: 97, Valid: true},
		IsActive:           true,
		HeadshotURL:        "https://example.com/headshot.jpg",
		HeroImageURL:       pgtype.Text{String: "https://example.com/hero.jpg", Valid: true},
		YahooImageSmall:    "https://example.com/yahoo-small.jpg",
		YahooImageMedium:   "https://example.com/yahoo-medium.jpg",
		YahooImageLarge:    "https://example.com/yahoo-large.jpg",
		YahooHomeURL:       "https://sports.yahoo.com/nhl/players/5441",
		PlayerSlug:         pgtype.Text{String: "connor-mcdavid-8478402", Valid: true},
		DraftYear:          pgtype.Int4{Int32: 2015, Valid: true},
		DraftTeamAbbrev:    pgtype.Text{String: "EDM", Valid: true},
		DraftRound:         pgtype.Int4{Int32: 1, Valid: true},
		DraftPickInRound:   pgtype.Int4{Int32: 1, Valid: true},
		DraftOverallPick:   pgtype.Int4{Int32: 1, Valid: true},
	}

	result := sqlcPlayerToGQL(row)

	assert.Equal(t, int64(8478402), result.ID)
	assert.NotNil(t, result.YahooID)
	assert.Equal(t, int64(5441), *result.YahooID)
	assert.Equal(t, "Connor", result.FirstName)
	assert.Equal(t, "McDavid", result.LastName)
	assert.Equal(t, "C", result.Position)
	assert.Equal(t, "L", result.ShootsCatches)
	assert.NotNil(t, result.HeightInches)
	assert.Equal(t, 73, *result.HeightInches)
	assert.NotNil(t, result.WeightPounds)
	assert.Equal(t, 193, *result.WeightPounds)
	assert.NotNil(t, result.BirthDate)
	assert.Equal(t, "1995-07-13", *result.BirthDate)
	assert.NotNil(t, result.BirthCity)
	assert.Equal(t, "Richmond Hill", *result.BirthCity)
	assert.NotNil(t, result.BirthStateProvince)
	assert.Equal(t, "ON", *result.BirthStateProvince)
	assert.NotNil(t, result.BirthCountry)
	assert.Equal(t, "CAN", *result.BirthCountry)
	assert.NotNil(t, result.SweaterNumber)
	assert.Equal(t, 97, *result.SweaterNumber)
	assert.True(t, result.IsActive)
	assert.Equal(t, "https://example.com/headshot.jpg", result.HeadshotURL)
	assert.NotNil(t, result.HeroImageURL)
	assert.Equal(t, "https://example.com/hero.jpg", *result.HeroImageURL)
	assert.Equal(t, "https://example.com/yahoo-small.jpg", result.YahooImageSmall)
	assert.Equal(t, "https://example.com/yahoo-medium.jpg", result.YahooImageMedium)
	assert.Equal(t, "https://example.com/yahoo-large.jpg", result.YahooImageLarge)
	assert.Equal(t, "https://sports.yahoo.com/nhl/players/5441", result.YahooHomeURL)
	assert.NotNil(t, result.PlayerSlug)
	assert.Equal(t, "connor-mcdavid-8478402", *result.PlayerSlug)
	assert.NotNil(t, result.DraftYear)
	assert.Equal(t, 2015, *result.DraftYear)
	assert.NotNil(t, result.DraftTeamAbbrev)
	assert.Equal(t, "EDM", *result.DraftTeamAbbrev)
	assert.NotNil(t, result.DraftRound)
	assert.Equal(t, 1, *result.DraftRound)
	assert.NotNil(t, result.DraftPickInRound)
	assert.Equal(t, 1, *result.DraftPickInRound)
	assert.NotNil(t, result.DraftOverallPick)
	assert.Equal(t, 1, *result.DraftOverallPick)

	// Check NHL team ID
	assert.NotNil(t, result.NHLTeamID)
	assert.Equal(t, int64(22), *result.NHLTeamID)
}

func TestSqlcPlayerToGQL_NullableFieldsEmpty(t *testing.T) {
	t.Parallel()

	row := sqlcdb.Player{
		ID:               8471675,
		FirstName:        "Sidney",
		LastName:         "Crosby",
		Position:         "C",
		ShootsCatches:    "L",
		IsActive:         true,
		HeadshotURL:      "https://example.com/crosby.jpg",
		YahooImageSmall:  "",
		YahooImageMedium: "",
		YahooImageLarge:  "",
		YahooHomeURL:     "",
		// All nullable fields left as zero values (Invalid)
	}

	result := sqlcPlayerToGQL(row)

	assert.Equal(t, int64(8471675), result.ID)
	assert.Nil(t, result.YahooID)
	assert.Equal(t, "Sidney", result.FirstName)
	assert.Equal(t, "Crosby", result.LastName)
	assert.Equal(t, "C", result.Position)
	assert.Nil(t, result.HeightInches)
	assert.Nil(t, result.WeightPounds)
	assert.Nil(t, result.BirthDate)
	assert.Nil(t, result.BirthCity)
	assert.Nil(t, result.BirthStateProvince)
	assert.Nil(t, result.BirthCountry)
	assert.Nil(t, result.SweaterNumber)
	assert.Nil(t, result.HeroImageURL)
	assert.Nil(t, result.PlayerSlug)
	assert.Nil(t, result.DraftYear)
	assert.Nil(t, result.DraftTeamAbbrev)
	assert.Nil(t, result.DraftRound)
	assert.Nil(t, result.DraftPickInRound)
	assert.Nil(t, result.DraftOverallPick)
	assert.Nil(t, result.NHLTeamID)
}

func TestSqlcPlayerToGQL_InactivePlayer(t *testing.T) {
	t.Parallel()

	row := sqlcdb.Player{
		ID:            8445001,
		FirstName:     "Wayne",
		LastName:      "Gretzky",
		Position:      "C",
		ShootsCatches: "L",
		IsActive:      false,
		HeadshotURL:   "",
	}

	result := sqlcPlayerToGQL(row)

	assert.Equal(t, int64(8445001), result.ID)
	assert.Equal(t, "Wayne", result.FirstName)
	assert.Equal(t, "Gretzky", result.LastName)
	assert.False(t, result.IsActive)
}

func TestSqlcPlayerToGQL_Goalie(t *testing.T) {
	t.Parallel()

	row := sqlcdb.Player{
		ID:            8477424,
		FirstName:     "Juuse",
		LastName:      "Saros",
		Position:      "G",
		ShootsCatches: "L",
		IsActive:      true,
		HeadshotURL:   "https://example.com/saros.jpg",
		SweaterNumber: pgtype.Int4{Int32: 74, Valid: true},
		TeamID:        pgtype.Int8{Int64: 18, Valid: true},
	}

	result := sqlcPlayerToGQL(row)

	assert.Equal(t, "G", result.Position)
	assert.Equal(t, "L", result.ShootsCatches)
	assert.NotNil(t, result.SweaterNumber)
	assert.Equal(t, 74, *result.SweaterNumber)
	assert.NotNil(t, result.NHLTeamID)
	assert.Equal(t, int64(18), *result.NHLTeamID)
}

func TestSqlcPlayerToGQL_PlayerWithYahooIDButNoTeam(t *testing.T) {
	t.Parallel()

	row := sqlcdb.Player{
		ID:               12345,
		YahooID:          pgtype.Int8{Int64: 9999, Valid: true},
		FirstName:        "Free",
		LastName:         "Agent",
		Position:         "D",
		ShootsCatches:    "R",
		IsActive:         true,
		HeadshotURL:      "",
		YahooImageSmall:  "https://example.com/fa-small.jpg",
		YahooImageMedium: "https://example.com/fa-medium.jpg",
		YahooImageLarge:  "https://example.com/fa-large.jpg",
		YahooHomeURL:     "https://sports.yahoo.com/nhl/players/9999",
		// No team info
	}

	result := sqlcPlayerToGQL(row)

	assert.NotNil(t, result.YahooID)
	assert.Equal(t, int64(9999), *result.YahooID)
	assert.Nil(t, result.NHLTeamID)
	assert.Equal(t, "https://example.com/fa-small.jpg", result.YahooImageSmall)
}
