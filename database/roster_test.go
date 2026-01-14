package database

import (
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"regexp"
	"testing"
	"time"
)

func TestRosterPlayer_SetVersion(t *testing.T) {
	t.Parallel()
	r := &RosterPlayer{}
	assert.Equal(t, time.Time{}, r.SourceVersion)
	r.SetSourceVersion(time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC))
	assert.Equal(t, time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC), r.SourceVersion)
}

func TestRoster_Ensure(t *testing.T) {
	t.Parallel()
	createdAt1 := time.Date(2022, 10, 19, 10, 15, 25, 0, time.UTC)
	updatedAt1 := time.Date(2022, 9, 30, 10, 15, 26, 0, time.UTC)
	date1 := time.Date(2022, 5, 30, 10, 15, 26, 0, time.UTC)
	fileTS1 := time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC)
	rp1 := RosterPlayer{
		CreatedAt:         createdAt1,
		UpdatedAt:         updatedAt1,
		Date:              date1,
		TeamID:            1,
		PlayerID:          11,
		NHLTeamID:         111,
		EligiblePositions: LW,
		SelectedPosition:  LW,
		SourceVersion:     fileTS1,
	}
	createdAt2 := time.Date(2023, 10, 19, 10, 15, 25, 0, time.UTC)
	updatedAt2 := time.Date(2023, 9, 30, 10, 15, 26, 0, time.UTC)
	date2 := time.Date(2022, 5, 30, 10, 15, 26, 0, time.UTC)
	fileTS2 := time.Date(2023, 3, 24, 10, 15, 27, 0, time.UTC)
	rp2 := RosterPlayer{
		CreatedAt:         createdAt2,
		UpdatedAt:         updatedAt2,
		Date:              date2,
		TeamID:            2,
		PlayerID:          22,
		NHLTeamID:         222,
		EligiblePositions: C,
		SelectedPosition:  C,
		SourceVersion:     fileTS2,
	}
	var players = RosterPlayers{rp1, rp2}
	gormdb, db, mock := sqlmockNew(t)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	stmt := fmt.Sprintf(`INSERT INTO "%s" ("%s","%s","%s","%s","%s","%s","%s","%s","%s","%s") `+
		`VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10),($11,$12,$13,$14,$15,$16,$17,$18,$19,$20) `+
		`ON CONFLICT ("%s","%s","%s") DO UPDATE SET %s`,
		TblRosterPlayers, CCreatedAt, CUpdatedAt, CDeletedAt, CDate, CTeamID, CPlayerID, CNHLTeamID, CEligiblePositions,
		CSelectedPosition, CSourceVersion, CDate, CTeamID, CPlayerID, upexes(rosterUpdateCols))
	mock.ExpectExec(regexp.QuoteMeta(stmt)).
		WithArgs(createdAt1, updatedAt1, nil, date1, 1, 11, 111, LW, LW, fileTS1,
			createdAt2, updatedAt2, nil, date2, 2, 22, 222, C, C, fileTS2).
		WillReturnResult(sqlmock.NewResult(1, 2))
	mock.ExpectCommit()
	if err := players.Ensure(gormdb); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err.Error())
	}
}
