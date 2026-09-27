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
	shiftTestGameID   = nhlapi.GameID(2024020001)
	shiftTestSegments = int64(24)
)

// stubTransactor runs fn with its rebuilder. It cannot roll back, so tests
// assert on which rebuilder calls were made; commitErr simulates a failed
// commit after fn succeeds.
type stubTransactor struct {
	rebuilder EvenStrengthSegmentRebuilder
	calls     int
	commitErr error
}

func (t *stubTransactor) InTx(_ context.Context, fn func(EvenStrengthSegmentRebuilder) error) error {
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
	s.Zero(result.SegmentsImported)
	s.Zero(tx.calls)
	q.AssertNotCalled(s.T(), "UpsertShiftBatch")
}

func (s *ImportShiftChartSuite) TestWithShifts_UpsertsThenRebuildsSegments() {
	mem := store.NewMemStorage()
	q := &MockQueries{}
	s.seedTwoShifts(mem)
	s.expectUpsert(q, nil)
	del := q.On("DeleteEvenStrengthSegmentsForGame", mock.Anything, int64(shiftTestGameID)).Return(nil)
	q.On("InsertEvenStrengthSegmentsForGame", mock.Anything, int64(shiftTestGameID)).
		Return(shiftTestSegments, nil).NotBefore(del)
	act, tx := s.newActivities(mem, q)

	result, err := s.run(act)
	s.Require().NoError(err)
	s.Equal(1, result.GamesProcessed)
	s.Equal(2, result.ShiftsImported)
	s.Equal(shiftTestSegments, result.SegmentsImported)
	s.Equal(1, tx.calls, "delete and insert share one transaction")
	q.AssertExpectations(s.T())
}

func (s *ImportShiftChartSuite) TestUpsertFails_DoesNotRebuildSegments() {
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
	q.AssertNotCalled(s.T(), "DeleteEvenStrengthSegmentsForGame", mock.Anything, mock.Anything)
	q.AssertNotCalled(s.T(), "InsertEvenStrengthSegmentsForGame", mock.Anything, mock.Anything)
}

func (s *ImportShiftChartSuite) TestRebuildFailures_ReturnErrorWithGameContext() {
	cases := []struct {
		name      string
		deleteErr error
		insertErr error
		commitErr error
		want      string
	}{
		{name: "delete", deleteErr: errors.New("delete denied"), want: "delete: delete denied"},
		{name: "insert", insertErr: errors.New("insert denied"), want: "insert: insert denied"},
		{name: "commit", commitErr: errors.New("commit denied"), want: "commit denied"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.env = s.NewTestActivityEnvironment()
			mem := store.NewMemStorage()
			q := &MockQueries{}
			s.seedTwoShifts(mem)
			s.expectUpsert(q, nil)
			q.On("DeleteEvenStrengthSegmentsForGame", mock.Anything, int64(shiftTestGameID)).Return(tc.deleteErr)
			if tc.deleteErr == nil {
				q.On("InsertEvenStrengthSegmentsForGame", mock.Anything, int64(shiftTestGameID)).
					Return(shiftTestSegments, tc.insertErr)
			}
			act, tx := s.newActivities(mem, q)
			tx.commitErr = tc.commitErr

			_, err := s.run(act)
			s.Require().Error(err)
			s.Contains(err.Error(), "rebuild even-strength segments for game "+shiftTestGameID.String())
			s.Contains(err.Error(), tc.want)
			q.AssertExpectations(s.T())
		})
	}
}
