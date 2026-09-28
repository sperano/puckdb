package nhl

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

const (
	shiftTestGameID         = nhlapi.GameID(2024020001)
	shiftTestPairTOIRows    = int64(20)
	shiftTestSkaterGameRows = int64(4)
	shiftTestTotalRows      = shiftTestPairTOIRows + shiftTestSkaterGameRows
)

// stubTransactor runs fn with its rebuilder. It cannot roll back, so tests
// assert on which rebuilder calls were made; commitErr simulates a failed
// commit after fn succeeds.
type stubTransactor struct {
	rebuilder EvenStrengthTotalsRebuilder
	calls     int
	commitErr error
}

func (t *stubTransactor) InTx(_ context.Context, fn func(EvenStrengthTotalsRebuilder) error) error {
	t.calls++
	if err := fn(t.rebuilder); err != nil {
		return err
	}
	return t.commitErr
}

type ImportShiftChartSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
	day time.Time
}

func (s *ImportShiftChartSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.day = time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
}

func TestImportShiftChartSuite(t *testing.T) {
	suite.Run(t, new(ImportShiftChartSuite))
}

func (s *ImportShiftChartSuite) newActivities(mem *store.MemStorage, q *MockQueries) (*ImportActivities, *stubTransactor) {
	tx := &stubTransactor{rebuilder: q}
	return &ImportActivities{
		Storage:  mem,
		GobCache: newImportTestGobCache(),
		Queries:  q,
		Tx:       tx,
	}, tx
}

func (s *ImportShiftChartSuite) run(act *ImportActivities) (ImportShiftChartForDateResult, error) {
	s.env.RegisterActivity(act.ImportShiftChartForDate)
	input := ImportShiftChartForDateInput{shared.DateSeasonInput{Date: s.day, Season: 2023}}
	var result ImportShiftChartForDateResult
	future, err := s.env.ExecuteActivity(act.ImportShiftChartForDate, input)
	if err != nil {
		return result, err
	}
	s.Require().NoError(future.Get(&result))
	return result, nil
}

// seedTwoShifts writes a schedule with one regular-season game whose shift
// chart holds two shifts.
func (s *ImportShiftChartSuite) seedTwoShifts(mem *store.MemStorage) {
	seedRegularSeasonSchedule(s.T(), mem, s.day, shiftTestGameID)
	sc := &nhlapi.ShiftChart{
		Data: []nhlapi.ShiftEntry{
			{ID: 1, GameID: shiftTestGameID, PlayerID: nhlapi.PlayerID(8478402), TeamID: nhlapi.TeamID(22)},
			{ID: 2, GameID: shiftTestGameID, PlayerID: nhlapi.PlayerID(8477934), TeamID: nhlapi.TeamID(22)},
		},
	}
	data, err := json.Marshal(sc)
	s.Require().NoError(err)
	s.Require().NoError(mem.Write(context.Background(), resource.ShiftChart{Date: s.day, GameID: shiftTestGameID}.Path(), data))
}

func (s *ImportShiftChartSuite) expectUpsert(q *MockQueries, err error) {
	q.On("UpsertShiftBatch", mock.Anything, mock.MatchedBy(func(params []sqlcdb.UpsertShiftBatchParams) bool {
		return len(params) == 2
	})).Return(err)
}

func (s *ImportShiftChartSuite) TestNoScheduleFile_ReturnsEmpty() {
	act, _ := s.newActivities(store.NewMemStorage(), &MockQueries{})

	result, err := s.run(act)
	s.Require().NoError(err)
	s.Equal(0, result.GamesProcessed)
}

func (s *ImportShiftChartSuite) TestMissingShiftFile_ReturnsError() {
	mem := store.NewMemStorage()
	seedRegularSeasonSchedule(s.T(), mem, s.day, shiftTestGameID)
	act, _ := s.newActivities(mem, &MockQueries{})

	_, err := s.run(act)
	s.Require().Error(err)
	s.Contains(err.Error(), "shift chart file missing")
}

func (s *ImportShiftChartSuite) TestEmptyShifts_SkipsBatchAndSegments() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	seedRegularSeasonSchedule(s.T(), mem, s.day, shiftTestGameID)
	seedEmptyShiftChart(s.T(), mem, s.day, shiftTestGameID)
	act, tx := s.newActivities(mem, q)

	result, err := s.run(act)
	s.Require().NoError(err)
	s.Equal(0, result.GamesProcessed)
	s.Equal(0, result.ShiftsImported)
	s.Zero(result.EvenStrengthRowsImported)
	s.Zero(tx.calls)
	q.AssertNotCalled(s.T(), "UpsertShiftBatch")
}

// expectRebuild sets up the delete-then-insert call order for both
// even-strength tables, returning pairRows/skaterGameRows from the inserts.
func (s *ImportShiftChartSuite) expectRebuild(q *MockQueries, pairRows, skaterGameRows int64) {
	delPair := q.On("DeleteEvenStrengthPairTOIForGame", mock.Anything, int64(shiftTestGameID)).Return(nil)
	delSkaterGames := q.On("DeleteEvenStrengthSkaterGamesForGame", mock.Anything, int64(shiftTestGameID)).
		Return(nil).NotBefore(delPair)
	q.On("InsertEvenStrengthPairTOIForGame", mock.Anything, int64(shiftTestGameID)).
		Return(pairRows, nil).NotBefore(delSkaterGames)
	q.On("InsertEvenStrengthSkaterGamesForGame", mock.Anything, int64(shiftTestGameID)).
		Return(skaterGameRows, nil).NotBefore(delSkaterGames)
}

func (s *ImportShiftChartSuite) TestWithShifts_UpsertsThenRebuildsEvenStrengthTotals() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	s.seedTwoShifts(mem)
	s.expectUpsert(q, nil)
	s.expectRebuild(q, shiftTestPairTOIRows, shiftTestSkaterGameRows)
	act, tx := s.newActivities(mem, q)

	result, err := s.run(act)
	s.Require().NoError(err)
	s.Equal(1, result.GamesProcessed)
	s.Equal(2, result.ShiftsImported)
	s.Equal(shiftTestTotalRows, result.EvenStrengthRowsImported)
	s.Equal(1, tx.calls, "deletes and inserts share one transaction")
	q.AssertExpectations(s.T())
}

func (s *ImportShiftChartSuite) TestUpsertFails_DoesNotRebuildEvenStrengthTotals() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	s.seedTwoShifts(mem)
	s.expectUpsert(q, errors.New("connection reset"))
	act, tx := s.newActivities(mem, q)

	_, err := s.run(act)
	s.Require().Error(err)
	s.Contains(err.Error(), "connection reset")
	s.Contains(err.Error(), shiftTestGameID.String())
	s.Zero(tx.calls)
	q.AssertNotCalled(s.T(), "DeleteEvenStrengthPairTOIForGame", mock.Anything, mock.Anything)
	q.AssertNotCalled(s.T(), "DeleteEvenStrengthSkaterGamesForGame", mock.Anything, mock.Anything)
	q.AssertNotCalled(s.T(), "InsertEvenStrengthPairTOIForGame", mock.Anything, mock.Anything)
	q.AssertNotCalled(s.T(), "InsertEvenStrengthSkaterGamesForGame", mock.Anything, mock.Anything)
}

func (s *ImportShiftChartSuite) TestRebuildFailures_ReturnErrorWithGameContext() {
	cases := []struct {
		name            string
		deletePairErr   error
		deleteSkaterErr error
		insertPairErr   error
		insertSkaterErr error
		commitErr       error
		want            string
	}{
		{name: "delete pair toi", deletePairErr: errors.New("delete denied"), want: "delete pair toi: delete denied"},
		{name: "delete skater games", deleteSkaterErr: errors.New("delete denied"), want: "delete skater games: delete denied"},
		{name: "insert pair toi", insertPairErr: errors.New("insert denied"), want: "insert pair toi: insert denied"},
		{name: "insert skater games", insertSkaterErr: errors.New("insert denied"), want: "insert skater games: insert denied"},
		{name: "commit", commitErr: errors.New("commit denied"), want: "commit denied"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.env = s.NewTestActivityEnvironment()
			mem := store.NewMemStorage()
			q := &MockQueries{}
			s.seedTwoShifts(mem)
			s.expectUpsert(q, nil)
			delPair := q.On("DeleteEvenStrengthPairTOIForGame", mock.Anything, int64(shiftTestGameID)).Return(tc.deletePairErr)
			if tc.deletePairErr == nil {
				delSkaterGames := q.On("DeleteEvenStrengthSkaterGamesForGame", mock.Anything, int64(shiftTestGameID)).
					Return(tc.deleteSkaterErr).NotBefore(delPair)
				if tc.deleteSkaterErr == nil {
					q.On("InsertEvenStrengthPairTOIForGame", mock.Anything, int64(shiftTestGameID)).
						Return(shiftTestPairTOIRows, tc.insertPairErr).NotBefore(delSkaterGames)
					if tc.insertPairErr == nil {
						q.On("InsertEvenStrengthSkaterGamesForGame", mock.Anything, int64(shiftTestGameID)).
							Return(shiftTestSkaterGameRows, tc.insertSkaterErr).NotBefore(delSkaterGames)
					}
				}
			}
			act, tx := s.newActivities(mem, q)
			tx.commitErr = tc.commitErr

			_, err := s.run(act)
			s.Require().Error(err)
			s.Contains(err.Error(), "rebuild even-strength totals for game "+shiftTestGameID.String())
			s.Contains(err.Error(), tc.want)
			q.AssertExpectations(s.T())
		})
	}
}
