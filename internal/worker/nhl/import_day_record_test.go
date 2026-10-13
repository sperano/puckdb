package nhl

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

const (
	recordTestGameID   = nhlapi.GameID(2024020001)
	recordTestSeasonID = 20232024
	// recordTestDayFiles is the schedule, five game files and the standings.
	recordTestDayFiles = 7
)

// fakeDayImportStore keeps import records in memory, keyed by season and day.
type fakeDayImportStore struct {
	records map[string]sqlcdb.UpsertNHLDayImportParams
	upserts int
}

func newFakeDayImportStore() *fakeDayImportStore {
	return &fakeDayImportStore{records: map[string]sqlcdb.UpsertNHLDayImportParams{}}
}

func dayImportKey(season int32, day time.Time) string {
	return fmt.Sprintf("%d/%s", season, day.Format(time.DateOnly))
}

func (f *fakeDayImportStore) GetNHLDayImport(_ context.Context, arg sqlcdb.GetNHLDayImportParams) (sqlcdb.GetNHLDayImportRow, error) {
	record, ok := f.records[dayImportKey(arg.Season, arg.Date.Time)]
	if !ok {
		return sqlcdb.GetNHLDayImportRow{}, pgx.ErrNoRows
	}
	return sqlcdb.GetNHLDayImportRow{ImporterVersion: record.ImporterVersion, InputsHash: record.InputsHash}, nil
}

func (f *fakeDayImportStore) UpsertNHLDayImport(_ context.Context, arg sqlcdb.UpsertNHLDayImportParams) error {
	f.records[dayImportKey(arg.Season, arg.Date.Time)] = arg
	f.upserts++
	return nil
}

type ImportDayIfChangedSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	day     time.Time
	mem     *store.MemStorage
	records *fakeDayImportStore
}

func TestImportDayIfChangedSuite(t *testing.T) {
	suite.Run(t, new(ImportDayIfChangedSuite))
}

func (s *ImportDayIfChangedSuite) SetupTest() {
	s.day = time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	s.mem = store.NewMemStorage()
	s.records = newFakeDayImportStore()
	seedRegularSeasonSchedule(s.T(), s.mem, s.day, recordTestGameID)
	seedEmptyBoxscore(s.T(), s.mem, s.day, recordTestGameID)
	seedEmptyPlayByPlay(s.T(), s.mem, s.day, recordTestGameID)
	seedEmptyShiftChart(s.T(), s.mem, s.day, recordTestGameID)
	seedEmptyGameStory(s.T(), s.mem, s.day, recordTestGameID)
	seedEmptyStandings(s.T(), s.mem, s.day)
}

func (s *ImportDayIfChangedSuite) input(force bool) ImportDayIfChangedInput {
	return ImportDayIfChangedInput{
		ImportDayInput: ImportDayInput{Date: s.day, Season: 2023, SeasonID: recordTestSeasonID},
		Force:          force,
	}
}

// run executes ImportDayIfChanged with fresh query mocks; importing expects
// the day's one game to be upserted, skipping expects no query at all.
func (s *ImportDayIfChangedSuite) run(force, expectImport bool) ImportDayIfChangedResult {
	q := &MockQueries{}
	if expectImport {
		q.On("UpsertGame", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertGameParams")).Return(nil).Once()
	}
	act := &ImportActivities{Storage: s.mem, GobCache: newImportTestGobCache(), Queries: q, DayImports: s.records}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(act.ImportDayIfChanged)
	future, err := env.ExecuteActivity(act.ImportDayIfChanged, s.input(force))
	s.Require().NoError(err)
	var result ImportDayIfChangedResult
	s.Require().NoError(future.Get(&result))
	q.AssertExpectations(s.T())
	return result
}

func (s *ImportDayIfChangedSuite) onlyRecord() sqlcdb.UpsertNHLDayImportParams {
	s.Require().Len(s.records.records, 1)
	for _, record := range s.records.records {
		return record
	}
	return sqlcdb.UpsertNHLDayImportParams{}
}

func (s *ImportDayIfChangedSuite) TestFirstImportRecordsFingerprint() {
	result := s.run(false, true)

	s.False(result.Skipped)
	record := s.onlyRecord()
	s.Equal(int32(recordTestSeasonID), record.Season)
	s.True(record.Date.Time.Equal(s.day))
	s.Equal(int32(DayImporterVersion), record.ImporterVersion)
	s.Equal(int32(recordTestDayFiles), record.InputFiles)
	s.NotEmpty(record.InputsHash)
}

func (s *ImportDayIfChangedSuite) TestUnchangedDayIsSkipped() {
	s.run(false, true)

	result := s.run(false, false)

	s.True(result.Skipped)
	s.Empty(result.Origins)
	s.Equal(1, s.records.upserts, "a skipped day is not recorded again")
}

func (s *ImportDayIfChangedSuite) TestRewrittenFileReimportsDay() {
	s.run(false, true)
	before := s.onlyRecord().InputsHash
	shiftChart := resource.ShiftChart{Date: s.day, GameID: recordTestGameID}.Path()
	data, err := s.mem.Read(context.Background(), shiftChart)
	s.Require().NoError(err)
	s.mem.SetFileWithTime(shiftChart, data, time.Now().Add(time.Minute))

	result := s.run(false, true)

	s.False(result.Skipped)
	s.NotEqual(before, s.onlyRecord().InputsHash)
	s.True(s.run(false, false).Skipped, "the new fingerprint is recorded")
}

func (s *ImportDayIfChangedSuite) TestFileAppearingChangesFingerprint() {
	s.run(false, true)
	series := resource.SeasonSeries{Date: s.day, GameID: recordTestGameID}.Path()
	s.Require().NoError(s.mem.Write(context.Background(), series, []byte("{}")))
	act := &ImportActivities{Storage: s.mem, GobCache: newImportTestGobCache()}

	fingerprint, err := act.dayInputsFingerprint(context.Background(), s.input(false).ImportDayInput)

	s.Require().NoError(err)
	s.NotEqual(s.onlyRecord().InputsHash, fingerprint.Hash)
	s.Equal(recordTestDayFiles, fingerprint.Files)
}

func (s *ImportDayIfChangedSuite) TestImporterVersionChangeReimportsDay() {
	s.run(false, true)
	for key, record := range s.records.records {
		record.ImporterVersion = DayImporterVersion - 1
		s.records.records[key] = record
	}

	result := s.run(false, true)

	s.False(result.Skipped)
	s.Equal(int32(DayImporterVersion), s.onlyRecord().ImporterVersion)
}

func (s *ImportDayIfChangedSuite) TestForceReimportsUnchangedDay() {
	s.run(false, true)

	result := s.run(true, true)

	s.False(result.Skipped)
	s.Equal(2, s.records.upserts)
}

func (s *ImportDayIfChangedSuite) TestFailedImportIsNotRecorded() {
	q := &MockQueries{}
	q.On("UpsertGame", mock.Anything, mock.Anything).Return(errors.New("db failure"))
	act := &ImportActivities{Storage: s.mem, GobCache: newImportTestGobCache(), Queries: q, DayImports: s.records}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(act.ImportDayIfChanged)

	_, err := env.ExecuteActivity(act.ImportDayIfChanged, s.input(false))

	s.Require().Error(err)
	s.Empty(s.records.records)
}

func TestDayInputs_ListsYahooTeamFilesWithoutSchedule(t *testing.T) {
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	act := &ImportActivities{Storage: store.NewMemStorage(), GobCache: newImportTestGobCache()}
	teams := []yahoo.TeamInfo{{LeagueID: 1, TeamID: 2}}

	inputs, err := act.dayInputs(context.Background(), ImportDayInput{Date: day, TeamIDs: teams})

	require.NoError(t, err)
	paths := make([]string, len(inputs))
	for i, r := range inputs {
		paths[i] = r.Path()
	}
	require.Equal(t, []string{
		resource.DailySchedule{Date: day}.Path(),
		resource.DailyStandings{Date: day}.Path(),
		resource.Roster{LeagueID: 1, TeamID: 2, Date: day}.Path(),
		resource.TeamSummary{LeagueID: 1, TeamID: 2, Date: day}.Path(),
	}, paths)
}
