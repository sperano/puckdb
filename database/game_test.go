package database

import (
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestGame_SetSourceVersion(t *testing.T) {
	t.Parallel()
	g := &Game{}
	assert.Equal(t, time.Time{}, g.SourceVersion)
	g.SetSourceVersion(time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC))
	assert.Equal(t, time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC), g.SourceVersion)
}

func TestGameGraphQLModel(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2022, 10, 19, 10, 15, 25, 0, time.UTC)
	updatedAt := time.Date(2022, 9, 30, 10, 15, 25, 0, time.UTC)
	date := time.Date(2022, 12, 15, 17, 21, 32, 0, time.UTC)
	ga := &Game{
		Model: gorm.Model{
			ID:        76,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		},
		Date:            date,
		HomeTeamScore1:  1,
		HomeTeamScore2:  2,
		HomeTeamScore3:  3,
		HomeTeamScoreO:  4,
		HomeTeamScoreSO: 5,
		AwayTeamScore1:  6,
		AwayTeamScore2:  7,
		AwayTeamScore3:  8,
		AwayTeamScoreO:  9,
		AwayTeamScoreSO: 10,
		State:           "final",
	}
	g := ga.GraphQLModel()
	assert.Equal(t, 76, g.ID)
	assert.Equal(t, date, g.Date)
	assert.Equal(t, 1, g.HomeTeamScore1)
	assert.Equal(t, 2, g.HomeTeamScore2)
	assert.Equal(t, 3, g.HomeTeamScore3)
	assert.Equal(t, 4, g.HomeTeamScoreO)
	assert.Equal(t, 5, g.HomeTeamScoreSo)
	assert.Equal(t, 6, g.AwayTeamScore1)
	assert.Equal(t, 7, g.AwayTeamScore2)
	assert.Equal(t, 8, g.AwayTeamScore3)
	assert.Equal(t, 9, g.AwayTeamScoreO)
	assert.Equal(t, 10, g.AwayTeamScoreSo)
	assert.Equal(t, "final", g.State)
	assert.Equal(t, createdAt, g.CreatedAt)
	assert.Equal(t, updatedAt, g.UpdatedAt)
}

func TestGameRegularTime(t *testing.T) {
	t.Parallel()
	ga := &Game{
		HomeTeamScore1:  1,
		HomeTeamScore2:  2,
		HomeTeamScore3:  3,
		HomeTeamScoreO:  0,
		HomeTeamScoreSO: 0,
		AwayTeamScore1:  1,
		AwayTeamScore2:  1,
		AwayTeamScore3:  1,
		AwayTeamScoreO:  0,
		AwayTeamScoreSO: 0,
	}
	assert.False(t, ga.IsOvertime())
	assert.False(t, ga.IsShootout())
	assert.Equal(t, 6, ga.HomeTeamScore())
	assert.Equal(t, 6, ga.CountingHomeTeamScore())
	assert.Equal(t, 3, ga.AwayTeamScore())
	assert.Equal(t, 3, ga.CountingAwayTeamScore())
}

func TestGameOvertime(t *testing.T) {
	t.Parallel()
	ga := &Game{
		HomeTeamScore1:  1,
		HomeTeamScore2:  2,
		HomeTeamScore3:  3,
		HomeTeamScoreO:  1,
		HomeTeamScoreSO: 0,
		AwayTeamScore1:  1,
		AwayTeamScore2:  1,
		AwayTeamScore3:  1,
		AwayTeamScoreO:  0,
		AwayTeamScoreSO: 0,
	}
	assert.True(t, ga.IsOvertime())
	assert.False(t, ga.IsShootout())
	assert.Equal(t, 7, ga.HomeTeamScore())
	assert.Equal(t, 7, ga.CountingHomeTeamScore())
	assert.Equal(t, 3, ga.AwayTeamScore())
	assert.Equal(t, 3, ga.CountingAwayTeamScore())
}

func TestGameShootout(t *testing.T) {
	t.Parallel()
	ga := &Game{
		HomeTeamScore1:  1,
		HomeTeamScore2:  2,
		HomeTeamScore3:  3,
		HomeTeamScoreO:  0,
		HomeTeamScoreSO: 1,
		AwayTeamScore1:  1,
		AwayTeamScore2:  1,
		AwayTeamScore3:  1,
		AwayTeamScoreO:  0,
		AwayTeamScoreSO: 0,
	}
	assert.False(t, ga.IsOvertime())
	assert.True(t, ga.IsShootout())
	assert.Equal(t, 7, ga.HomeTeamScore())
	assert.Equal(t, 6, ga.CountingHomeTeamScore())
	assert.Equal(t, 3, ga.AwayTeamScore())
	assert.Equal(t, 3, ga.CountingAwayTeamScore())
}

func TestGame_IsPostPoned(t *testing.T) {
	t.Parallel()
	g := &Game{}
	assert.False(t, g.IsPostPoned())
	g.State = GamePostponedState
	assert.True(t, g.IsPostPoned())
}

func TestHomeTeamWins(t *testing.T) {
	t.Parallel()
	ga := &Game{
		HomeTeamScore1:  1,
		HomeTeamScore2:  2,
		HomeTeamScore3:  3,
		HomeTeamScoreO:  0,
		HomeTeamScoreSO: 0,
		AwayTeamScore1:  1,
		AwayTeamScore2:  1,
		AwayTeamScore3:  1,
		AwayTeamScoreO:  0,
		AwayTeamScoreSO: 0,
	}
	assert.True(t, ga.HomeTeamWins())
}

func TestAwayTeamWins(t *testing.T) {
	t.Parallel()
	ga := &Game{
		HomeTeamScore1:  1,
		HomeTeamScore2:  2,
		HomeTeamScore3:  3,
		HomeTeamScoreO:  0,
		HomeTeamScoreSO: 0,
		AwayTeamScore1:  1,
		AwayTeamScore2:  1,
		AwayTeamScore3:  5,
		AwayTeamScoreO:  0,
		AwayTeamScoreSO: 0,
	}
	assert.False(t, ga.HomeTeamWins())
}

func TestGameEnsure(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2022, 10, 19, 10, 15, 25, 0, time.UTC)
	updatedAt := time.Date(2022, 9, 30, 10, 15, 26, 0, time.UTC)
	fileTS := time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC)
	date := time.Date(2022, 2, 12, 10, 15, 28, 0, time.UTC)
	g := &Game{
		Model: gorm.Model{
			ID:        76,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		},
		Date:            date,
		HomeTeamID:      123,
		HomeTeamScore1:  1,
		HomeTeamScore2:  2,
		HomeTeamScore3:  3,
		HomeTeamScoreO:  4,
		HomeTeamScoreSO: 5,
		AwayTeamID:      456,
		AwayTeamScore1:  6,
		AwayTeamScore2:  7,
		AwayTeamScore3:  8,
		AwayTeamScoreO:  9,
		AwayTeamScoreSO: 10,
		SourceVersion:   fileTS,
		State:           "postponed",
	}
	gormdb, db, mock := sqlmockNew(t)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	stmt := fmt.Sprintf(`INSERT INTO "%s" ("%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s") `+
		`VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19) ON CONFLICT ("%s") DO UPDATE SET %s RETURNING "%s"`,
		TblGames, CCreatedAt, CUpdatedAt, CDeletedAt, CDate, CHomeTeamID, CHomeTeamScore1, CHomeTeamScore2, CHomeTeamScore3,
		CHomeTeamScoreO, CHomeTeamScoreSO, CAwayTeamID, CAwayTeamScore1, CAwayTeamScore2, CAwayTeamScore3, CAwayTeamScoreO, CAwayTeamScoreSO,
		CState, CSourceVersion, CID, CID, upexes(gameUpdateCols), CID)
	mock.ExpectQuery(regexp.QuoteMeta(stmt)).
		WithArgs(createdAt, updatedAt, nil, date, 123, 1, 2, 3, 4, 5, 456, 6, 7, 8, 9, 10, "postponed", fileTS, 76).
		WillReturnRows(sqlmock.NewRows([]string{CID}).AddRow(76))
	mock.ExpectCommit()

	if err := g.Ensure(gormdb); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err.Error())
	}
}

func TestDiffGame(t *testing.T) {
	t.Parallel()
	date1 := time.Date(2022, 2, 12, 10, 15, 28, 0, time.UTC)
	date2 := time.Date(2022, 3, 12, 10, 15, 28, 0, time.UTC)
	fileTS1 := time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC)
	fileTS2 := time.Date(2023, 3, 24, 10, 15, 27, 0, time.UTC)

	g1 := &Game{
		Model: gorm.Model{
			ID: 76,
		},
		Date:            date1,
		HomeTeamID:      123,
		HomeTeamScore1:  1,
		HomeTeamScore2:  2,
		HomeTeamScore3:  3,
		HomeTeamScoreO:  4,
		HomeTeamScoreSO: 5,
		AwayTeamID:      456,
		AwayTeamScore1:  6,
		AwayTeamScore2:  7,
		AwayTeamScore3:  8,
		AwayTeamScoreO:  9,
		AwayTeamScoreSO: 10,
		State:           "postponed",
		SourceVersion:   fileTS1,
	}
	g2 := &Game{
		Model: gorm.Model{
			ID: 83,
		},
		Date:            date2,
		HomeTeamID:      1232,
		HomeTeamScore1:  12,
		HomeTeamScore2:  22,
		HomeTeamScore3:  32,
		HomeTeamScoreO:  42,
		HomeTeamScoreSO: 52,
		AwayTeamID:      4562,
		AwayTeamScore1:  62,
		AwayTeamScore2:  72,
		AwayTeamScore3:  82,
		AwayTeamScoreO:  92,
		AwayTeamScoreSO: 102,
		State:           "postponed2",
		SourceVersion:   fileTS2,
	}
	diffs := DiffGame(g2, g1)
	assert.Equal(t, 16, len(diffs))
	assertDiff(t, diffs[0], AID, uint(76), uint(83))
	assertDiff(t, diffs[1], ADate, date1, date2)
	assertDiff(t, diffs[2], "HomeTeamID", uint(123), uint(1232))
	assertDiff(t, diffs[3], "HomeTeamScore1", 1, 12)
	assertDiff(t, diffs[4], "HomeTeamScore2", 2, 22)
	assertDiff(t, diffs[5], "HomeTeamScore3", 3, 32)
	assertDiff(t, diffs[6], "HomeTeamScoreO", 4, 42)
	assertDiff(t, diffs[7], "HomeTeamScoreSO", 5, 52)
	assertDiff(t, diffs[8], "AwayTeamID", uint(456), uint(4562))
	assertDiff(t, diffs[9], "AwayTeamScore1", 6, 62)
	assertDiff(t, diffs[10], "AwayTeamScore2", 7, 72)
	assertDiff(t, diffs[11], "AwayTeamScore3", 8, 82)
	assertDiff(t, diffs[12], "AwayTeamScoreO", 9, 92)
	assertDiff(t, diffs[14], "State", "postponed", "postponed2")
	assertDiff(t, diffs[15], "SourceVersion", fileTS1, fileTS2)
}

