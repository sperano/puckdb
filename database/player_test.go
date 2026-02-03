package database

import (
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
	"regexp"
	"testing"
	"time"
)

func TestTrySetStatUInt(t *testing.T) {
	t.Parallel()
	var foo uint
	err := TrySetStatUInt(&foo, "123")
	assert.Nil(t, err)
	assert.Equal(t, uint(123), foo)
}

func TestTrySetStatUIntEmpty(t *testing.T) {
	t.Parallel()
	var foo uint
	err := TrySetStatUInt(&foo, "")
	assert.Nil(t, err)
	assert.Zero(t, foo)
}

func TestTrySetStatUIntInvalid(t *testing.T) {
	t.Parallel()
	var foo uint
	err := TrySetStatUInt(&foo, "foo")
	assert.Equal(t, "strconv.Atoi: parsing \"foo\": invalid syntax", err.Error())
	assert.Zero(t, foo)
}

func TestTrySetStatInt(t *testing.T) {
	t.Parallel()
	var foo int
	err := TrySetStatInt(&foo, "123")
	assert.Nil(t, err)
	assert.Equal(t, 123, foo)
}

func TestTrySetStatIntEmpty(t *testing.T) {
	t.Parallel()
	var foo int
	err := TrySetStatInt(&foo, "")
	assert.Nil(t, err)
	assert.Zero(t, foo)
}

func TestTrySetStatIntInvalid(t *testing.T) {
	t.Parallel()
	var foo int
	err := TrySetStatInt(&foo, "foo")
	assert.Equal(t, "strconv.Atoi: parsing \"foo\": invalid syntax", err.Error())
	assert.Zero(t, foo)
}

func TestPlayersEnsure(t *testing.T) {
	t.Parallel()
	createdAt1 := time.Date(2022, 10, 19, 10, 15, 25, 0, time.UTC)
	updatedAt1 := time.Date(2022, 9, 30, 10, 15, 26, 0, time.UTC)
	fileTS1 := time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC)
	p1 := &Player{
		Model: gorm.Model{
			ID:        76,
			CreatedAt: createdAt1,
			UpdatedAt: updatedAt1,
		},
		FirstName:     "my-first-name1",
		LastName:      "my-last-name1",
		UniformNumber: 76,
		HomeURL:       "my-home-url1",
		ImageSmall:    "my-image-small1",
		ImageMedium:   "my-image-medium1",
		ImageLarge:    "my-image-large1",
		NHLTeamID:     760,
		SourceVersion: fileTS1,
	}
	createdAt2 := time.Date(2023, 10, 19, 10, 15, 25, 0, time.UTC)
	updatedAt2 := time.Date(2023, 9, 30, 10, 15, 26, 0, time.UTC)
	fileTS2 := time.Date(2023, 3, 24, 10, 15, 27, 0, time.UTC)
	p2 := &Player{
		Model: gorm.Model{
			ID:        83,
			CreatedAt: createdAt2,
			UpdatedAt: updatedAt2,
		},
		FirstName:     "my-first-name2",
		LastName:      "my-last-name2",
		UniformNumber: 83,
		HomeURL:       "my-home-url2",
		ImageSmall:    "my-image-small2",
		ImageMedium:   "my-image-medium2",
		ImageLarge:    "my-image-large2",
		NHLTeamID:     830,
		SourceVersion: fileTS2,
	}
	var players = Players{p1, p2}
	gormdb, db, mock := sqlmockNew(t)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	stmt := fmt.Sprintf(`INSERT INTO "%s" ("%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s") `+
		`VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13),($14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26) `+
		`ON CONFLICT ("%s") DO UPDATE SET %s RETURNING "%s"`,
		TblPlayers, CCreatedAt, CUpdatedAt, CDeletedAt, CFirstName, CLastName, CUniformNumber, CHomeURL,
		CImageSmall, CImageMedium, CImageLarge, CNHLTeamID, CSourceVersion, CID, CID,
		upexes(playerUpdateCols), CID)
	mock.ExpectQuery(regexp.QuoteMeta(stmt)).
		WithArgs(createdAt1, updatedAt1, nil, "my-first-name1", "my-last-name1", 76, "my-home-url1", "my-image-small1", "my-image-medium1", "my-image-large1", 760, fileTS1, 76,
			createdAt2, updatedAt2, nil, "my-first-name2", "my-last-name2", 83, "my-home-url2", "my-image-small2", "my-image-medium2", "my-image-large2", 830, fileTS2, 83).
		WillReturnRows(sqlmock.NewRows([]string{CID}).AddRow(76).AddRow(83))
	mock.ExpectCommit()

	if err := players.Ensure(gormdb); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err.Error())
	}
}

func TestPlayerGraphQLModel(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2022, 10, 19, 10, 15, 25, 0, time.UTC)
	updatedAt := time.Date(2022, 9, 30, 10, 15, 25, 0, time.UTC)
	fileTS := time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC)
	p := &Player{
		Model: gorm.Model{
			ID:        76,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		},
		FirstName:     "my-first-name",
		LastName:      "my-last-name",
		UniformNumber: 76,
		HomeURL:       "my-home-url",
		ImageSmall:    "my-image-small",
		ImageMedium:   "my-image-medium",
		ImageLarge:    "my-image-large",
		NHLTeam: NHLTeam{
			Model: gorm.Model{
				ID: 760,
			},
		},
		SourceVersion: fileTS,
	}
	g := p.GraphQLModel()
	assert.Equal(t, int64(76), g.ID)
	assert.Equal(t, "my-first-name", g.FirstName)
	assert.Equal(t, "my-last-name", g.LastName)
	assert.NotNil(t, g.SweaterNumber)
	assert.Equal(t, 76, *g.SweaterNumber)
	assert.Equal(t, "my-home-url", g.YahooHomeURL)
	assert.Equal(t, "my-image-small", g.YahooImageSmall)
	assert.Equal(t, "my-image-medium", g.YahooImageMedium)
	assert.Equal(t, "my-image-large", g.YahooImageLarge)
	assert.Equal(t, 760, g.NhlTeam.ID)
}

func TestPlayer_SetVersion(t *testing.T) {
	t.Parallel()
	p := &Player{}
	assert.Equal(t, time.Time{}, p.SourceVersion)
	p.SetSourceVersion(time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC))
	assert.Equal(t, time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC), p.SourceVersion)
}

func TestPlayerStatsEnsure(t *testing.T) {
	t.Parallel()
	createdAt1 := time.Date(2022, 10, 19, 10, 15, 25, 0, time.UTC)
	updatedAt1 := time.Date(2022, 9, 30, 10, 15, 26, 0, time.UTC)
	date1 := time.Date(2022, 3, 25, 10, 15, 27, 0, time.UTC)
	fileTS1 := time.Date(2022, 3, 24, 10, 15, 28, 0, time.UTC)
	ps1 := &PlayerStats{
		CreatedAt: createdAt1,
		UpdatedAt: updatedAt1,
		Date:      date1,
		PlayerID:  23,
		NHLTeamID: 10,
		StatsGoaler: StatsGoaler{
			GoalAgainst:     1,
			ShotsAgainst:    2,
			Saves:           3,
			GoalieTimeOnIce: 4,
		},
		StatsSkater: StatsSkater{
			Goals:          5,
			Assists:        6,
			PlusMinus:      7,
			PenaltyMinutes: 8,
			ShotsOnGoal:    9,
			FaceoffsWon:    10,
			FaceoffsLost:   11,
			Hits:           12,
			Blocks:         13,
			TimeOnIce:      14,
			Shifts:         15,
			TakeAways:      16,
			GiveAways:      17,
		},
		SourceVersion: fileTS1,
	}
	createdAt2 := time.Date(2023, 10, 19, 10, 15, 29, 0, time.UTC)
	updatedAt2 := time.Date(2023, 9, 30, 10, 15, 30, 0, time.UTC)
	date2 := time.Date(2023, 3, 26, 10, 15, 31, 0, time.UTC)
	fileTS2 := time.Date(2023, 3, 24, 10, 15, 32, 0, time.UTC)
	ps2 := &PlayerStats{
		CreatedAt: createdAt2,
		UpdatedAt: updatedAt2,
		Date:      date2,
		PlayerID:  232,
		NHLTeamID: 102,
		StatsGoaler: StatsGoaler{
			GoalAgainst:     12,
			ShotsAgainst:    22,
			Saves:           32,
			GoalieTimeOnIce: 42,
		},
		StatsSkater: StatsSkater{
			Goals:          52,
			Assists:        62,
			PlusMinus:      72,
			PenaltyMinutes: 82,
			ShotsOnGoal:    92,
			FaceoffsWon:    102,
			FaceoffsLost:   112,
			Hits:           122,
			Blocks:         132,
			TimeOnIce:      142,
			Shifts:         152,
			TakeAways:      162,
			GiveAways:      172,
		},
		SourceVersion: fileTS2,
	}
	var playersStats = PlayersStats{ps1, ps2}
	gormdb, db, mock := sqlmockNew(t)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	stmt := fmt.Sprintf(`INSERT INTO "%s" ("%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s",`+
		`"%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s") `+
		`VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24),`+
		`($25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,$40,$41,$42,$43,$44,$45,$46,$47,$48) `+
		`ON CONFLICT ("%s","%s") DO UPDATE SET %s`,
		TblPlayerStats, CCreatedAt, CUpdatedAt, CDeletedAt, CDate, CPlayerID, CSourceVersion, CNHLTeamID, CGoalAgainst,
		CShotsAgainst, CSaves, CGoalieTimeOnIce, CGoals, CAssists, CPlusMinus, CPenaltyMinutes, CShotsOnGoal, CFaceoffsWon,
		CFaceoffsLost, CHits, CBlocks, CTimeOnIce, CShifts, CTakeAways, CGiveAways,
		CDate, CPlayerID, upexes(playerStatsUpdateCols))
	mock.ExpectExec(regexp.QuoteMeta(stmt)).
		WithArgs(
			createdAt1, updatedAt1, nil, date1, 23, fileTS1, 10, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17,
			createdAt2, updatedAt2, nil, date2, 232, fileTS2, 102, 12, 22, 32, 42, 52, 62, 72, 82, 92, 102, 112, 122, 132, 142, 152, 162, 172).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := playersStats.Ensure(gormdb); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err.Error())
	}
}

func TestPlayerDiffSame(t *testing.T) {
	t.Parallel()
	p := &Player{
		Model: gorm.Model{
			ID: 76,
		},
		FirstName:     "my-first-name",
		LastName:      "my-last-name",
		UniformNumber: 76,
		HomeURL:       "my-home-url",
		ImageSmall:    "my-image-small",
		ImageMedium:   "my-image-medium",
		ImageLarge:    "my-image-large",
		NHLTeam: NHLTeam{
			Model: gorm.Model{
				ID: 760,
			},
		},
	}
	diffs := DiffPlayer(p, p)
	assert.Empty(t, diffs)
}

func TestPlayerDiff(t *testing.T) {
	t.Parallel()
	p1 := &Player{
		Model: gorm.Model{
			ID: 76,
		},
		FirstName:     "my-first-name1",
		LastName:      "my-last-name1",
		UniformNumber: 76,
		HomeURL:       "my-home-url1",
		ImageSmall:    "my-image-small1",
		ImageMedium:   "my-image-medium1",
		ImageLarge:    "my-image-large1",
		NHLTeam: NHLTeam{
			Model: gorm.Model{
				ID: 760,
			},
		},
	}
	p2 := &Player{
		Model: gorm.Model{
			ID: 83,
		},
		FirstName:     "my-first-name2",
		LastName:      "my-last-name2",
		UniformNumber: 83,
		HomeURL:       "my-home-url2",
		ImageSmall:    "my-image-small2",
		ImageMedium:   "my-image-medium2",
		ImageLarge:    "my-image-large2",
		NHLTeam: NHLTeam{
			Model: gorm.Model{
				ID: 830,
			},
		},
	}
	diffs := DiffPlayer(p2, p1)
	assert.Equal(t, 9, len(diffs))
	assertDiff(t, diffs[0], AID, uint(76), uint(83))
	assertDiff(t, diffs[1], "FirstName", "my-first-name1", "my-first-name2")
	assertDiff(t, diffs[2], "LastName", "my-last-name1", "my-last-name2")
	assertDiff(t, diffs[3], "UniformNumber", 76, 83)
	assertDiff(t, diffs[4], "HomeURL", "my-home-url1", "my-home-url2")
	assertDiff(t, diffs[5], "ImageSmall", "my-image-small1", "my-image-small2")
	assertDiff(t, diffs[6], "ImageMedium", "my-image-medium1", "my-image-medium2")
	assertDiff(t, diffs[7], "ImageLarge", "my-image-large1", "my-image-large2")
	assertDiff(t, diffs[8], AID, uint(760), uint(830))

}

func TestPlayerStats_SetVersion(t *testing.T) {
	t.Parallel()
	ps := &PlayerStats{}
	assert.Equal(t, time.Time{}, ps.SourceVersion)
	ps.SetSourceVersion(time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC))
	assert.Equal(t, time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC), ps.SourceVersion)
}

func TestPlayerStatsDiffSame(t *testing.T) {
	t.Parallel()
	date1 := time.Date(2022, 3, 25, 10, 15, 27, 0, time.UTC)
	ps1 := &PlayerStats{
		Date:      date1,
		PlayerID:  23,
		NHLTeamID: 10,
		StatsGoaler: StatsGoaler{
			GoalAgainst:     1,
			ShotsAgainst:    2,
			Saves:           3,
			GoalieTimeOnIce: 4,
		},
		StatsSkater: StatsSkater{
			Goals:          5,
			Assists:        6,
			PlusMinus:      7,
			PenaltyMinutes: 8,
			ShotsOnGoal:    9,
			FaceoffsWon:    10,
			FaceoffsLost:   11,
			Hits:           12,
			Blocks:         13,
			TimeOnIce:      14,
			Shifts:         15,
			TakeAways:      16,
			GiveAways:      17,
		},
	}
	diffs := DiffPlayerStats(ps1, ps1)
	assert.Empty(t, len(diffs))
}

func TestPlayerStatsDiff(t *testing.T) {
	t.Parallel()
	date1 := time.Date(2022, 3, 25, 10, 15, 27, 0, time.UTC)
	ps1 := &PlayerStats{
		Date:      date1,
		PlayerID:  23,
		NHLTeamID: 10,
		StatsGoaler: StatsGoaler{
			GoalAgainst:     1,
			ShotsAgainst:    2,
			Saves:           3,
			GoalieTimeOnIce: 4,
		},
		StatsSkater: StatsSkater{
			Goals:          5,
			Assists:        6,
			PlusMinus:      7,
			PenaltyMinutes: 8,
			ShotsOnGoal:    9,
			FaceoffsWon:    10,
			FaceoffsLost:   11,
			Hits:           12,
			Blocks:         13,
			TimeOnIce:      14,
			Shifts:         15,
			TakeAways:      16,
			GiveAways:      17,
		},
	}
	date2 := time.Date(2023, 3, 26, 10, 15, 31, 0, time.UTC)
	ps2 := &PlayerStats{
		Date:      date2,
		PlayerID:  232,
		NHLTeamID: 102,
		StatsGoaler: StatsGoaler{
			GoalAgainst:     12,
			ShotsAgainst:    22,
			Saves:           32,
			GoalieTimeOnIce: 42,
		},
		StatsSkater: StatsSkater{
			Goals:          52,
			Assists:        62,
			PlusMinus:      72,
			PenaltyMinutes: 82,
			ShotsOnGoal:    92,
			FaceoffsWon:    102,
			FaceoffsLost:   112,
			Hits:           122,
			Blocks:         132,
			TimeOnIce:      142,
			Shifts:         152,
			TakeAways:      162,
			GiveAways:      172,
		},
	}
	diffs := DiffPlayerStats(ps2, ps1)
	assert.Equal(t, 20, len(diffs))
	assertDiff(t, diffs[0], "Date", date1, date2)
	assertDiff(t, diffs[1], "PlayerID", uint(23), uint(232))
	assertDiff(t, diffs[2], "NHLTeamID", uint(10), uint(102))
	assertDiff(t, diffs[3], "GoalAgainst", uint(1), uint(12))
	assertDiff(t, diffs[4], "ShotsAgainst", uint(2), uint(22))
	assertDiff(t, diffs[5], "Saves", uint(3), uint(32))
	assertDiff(t, diffs[6], "GoalieTimeOnIce", uint(4), uint(42))
	assertDiff(t, diffs[7], "Goals", uint(5), uint(52))
	assertDiff(t, diffs[8], "Assists", uint(6), uint(62))
	assertDiff(t, diffs[9], "PlusMinus", 7, 72)
	assertDiff(t, diffs[10], "PenaltyMinutes", uint(8), uint(82))
	assertDiff(t, diffs[11], "ShotsOnGoal", uint(9), uint(92))
	assertDiff(t, diffs[12], "FaceoffsWon", uint(10), uint(102))
	assertDiff(t, diffs[13], "FaceoffsLost", uint(11), uint(112))
	assertDiff(t, diffs[14], "Hits", uint(12), uint(122))
	assertDiff(t, diffs[15], "Blocks", uint(13), uint(132))
	assertDiff(t, diffs[16], "TimeOnIce", uint(14), uint(142))
	assertDiff(t, diffs[17], "Shifts", uint(15), uint(152))
	assertDiff(t, diffs[18], "TakeAways", uint(16), uint(162))
	assertDiff(t, diffs[19], "GiveAways", uint(17), uint(172))
}
