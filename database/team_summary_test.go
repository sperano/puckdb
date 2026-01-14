package database

import (
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"regexp"
	"testing"
	"time"
)

func TestTeamSummaryEnsure(t *testing.T) {
	t.Parallel()
	createdAt1 := time.Date(2022, 10, 19, 10, 15, 25, 0, time.UTC)
	updatedAt1 := time.Date(2022, 9, 30, 10, 15, 26, 0, time.UTC)
	date1 := time.Date(2022, 3, 25, 10, 15, 27, 0, time.UTC)
	fileTS1 := time.Date(2022, 3, 24, 10, 15, 28, 0, time.UTC)
	ts1 := &TeamSummary{
		CreatedAt: createdAt1,
		UpdatedAt: updatedAt1,
		Date:      date1,
		TeamID:    23,
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
	gormdb, db, mock := sqlmockNew(t)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	stmt := fmt.Sprintf(`INSERT INTO "%s" ("%s","%s","%s","%s","%s","%s","%s","%s","%s","%s",`+
		`"%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s","%s") `+
		`VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23) `+
		`ON CONFLICT ("%s","%s") DO UPDATE SET %s`,
		TblTeamSummaries, CCreatedAt, CUpdatedAt, CDeletedAt, CDate, CTeamID, CSourceVersion, CGoalAgainst, CShotsAgainst,
		CSaves, CGoalieTimeOnIce, CGoals, CAssists, CPlusMinus, CPenaltyMinutes, CShotsOnGoal, CFaceoffsWon, CFaceoffsLost,
		CHits, CBlocks, CTimeOnIce, CShifts, CTakeAways, CGiveAways, CDate, CTeamID,
		upexes(tsummaryUpdateCols))
	mock.ExpectExec(regexp.QuoteMeta(stmt)).
		WithArgs(createdAt1, updatedAt1, nil, date1, 23, fileTS1, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17).
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectCommit()
	if err := ts1.Ensure(gormdb); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err.Error())
	}
}

func TestTeamSummary_SetSourceVersion(t *testing.T) {
	t.Parallel()
	ts := &TeamSummary{}
	assert.Equal(t, time.Time{}, ts.SourceVersion)
	ts.SetSourceVersion(time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC))
	assert.Equal(t, time.Date(2022, 3, 24, 10, 15, 27, 0, time.UTC), ts.SourceVersion)
}

func TestTeamSummaryDiffSame(t *testing.T) {
	t.Parallel()
	date1 := time.Date(2022, 3, 25, 10, 15, 27, 0, time.UTC)
	ts1 := &TeamSummary{
		Date:   date1,
		TeamID: 10,
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
	diffs := DiffTeamSummary(ts1, ts1)
	assert.Empty(t, len(diffs))
}

func TestTeamSummary_Diffs(t *testing.T) {
	t.Parallel()
	date1 := time.Date(2022, 3, 25, 10, 15, 27, 0, time.UTC)
	ts1 := &TeamSummary{
		Date:   date1,
		TeamID: 10,
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
	ts2 := &TeamSummary{
		Date:   date2,
		TeamID: 102,
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
	diffs := DiffTeamSummary(ts2, ts1)
	assert.Equal(t, 19, len(diffs))
	assertDiff(t, diffs[0], "Date", date1, date2)
	assertDiff(t, diffs[1], "TeamID", uint(10), uint(102))
	assertDiff(t, diffs[2], "GoalAgainst", uint(1), uint(12))
	assertDiff(t, diffs[3], "ShotsAgainst", uint(2), uint(22))
	assertDiff(t, diffs[4], "Saves", uint(3), uint(32))
	assertDiff(t, diffs[5], "GoalieTimeOnIce", uint(4), uint(42))
	assertDiff(t, diffs[6], "Goals", uint(5), uint(52))
	assertDiff(t, diffs[7], "Assists", uint(6), uint(62))
	assertDiff(t, diffs[8], "PlusMinus", 7, 72)
	assertDiff(t, diffs[9], "PenaltyMinutes", uint(8), uint(82))
	assertDiff(t, diffs[10], "ShotsOnGoal", uint(9), uint(92))
	assertDiff(t, diffs[11], "FaceoffsWon", uint(10), uint(102))
	assertDiff(t, diffs[12], "FaceoffsLost", uint(11), uint(112))
	assertDiff(t, diffs[13], "Hits", uint(12), uint(122))
	assertDiff(t, diffs[14], "Blocks", uint(13), uint(132))
	assertDiff(t, diffs[15], "TimeOnIce", uint(14), uint(142))
	assertDiff(t, diffs[16], "Shifts", uint(15), uint(152))
	assertDiff(t, diffs[17], "TakeAways", uint(16), uint(162))
	assertDiff(t, diffs[18], "GiveAways", uint(17), uint(172))
}
