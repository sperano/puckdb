package nhl

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

// importDayProbeActivity is the activity name under which the tests run
// importDay, whose partial result an activity error would otherwise hide.
const importDayProbeActivity = "ImportDayProbe"

// earlierStepReads is what the boxscore and game story steps read for the
// seeded day: the schedule and the game's file, each.
const earlierStepReads = 4

// importDayProbe is importDay's result and error message, returned as one
// successful activity result.
type importDayProbe struct {
	Result dayImportResult
	Err    string
}

func seedImportDay(t *testing.T, day time.Time) *store.MemStorage {
	t.Helper()
	mem := store.NewMemStorage()
	seedRegularSeasonSchedule(t, mem, day, recordTestGameID)
	seedEmptyBoxscore(t, mem, day, recordTestGameID)
	seedEmptyPlayByPlay(t, mem, day, recordTestGameID)
	seedEmptyShiftChart(t, mem, day, recordTestGameID)
	seedEmptyGameStory(t, mem, day, recordTestGameID)
	seedEmptyStandings(t, mem, day)
	return mem
}

// runImportDay runs importDay in an activity environment over mem, with the
// day's one game upserted by upsertErr's outcome.
func runImportDay(t *testing.T, mem *store.MemStorage, day time.Time, upsertErr error) importDayProbe {
	t.Helper()
	q := &MockQueries{}
	q.On("UpsertGame", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertGameParams")).Return(upsertErr).Once()
	act := &ImportActivities{Storage: mem, GobCache: newImportTestGobCache(), Queries: q}
	var env testsuite.WorkflowTestSuite
	activityEnv := env.NewTestActivityEnvironment()
	activityEnv.RegisterActivityWithOptions(func(ctx context.Context, input ImportDayInput) (importDayProbe, error) {
		result, err := act.importDay(ctx, input)
		probe := importDayProbe{Result: result}
		if err != nil {
			probe.Err = err.Error()
		}
		return probe, nil
	}, activity.RegisterOptions{Name: importDayProbeActivity})
	input := ImportDayInput{Date: day, Season: 2023, SeasonID: recordTestSeasonID}
	future, err := activityEnv.ExecuteActivity(importDayProbeActivity, input)
	require.NoError(t, err)
	var probe importDayProbe
	require.NoError(t, future.Get(&probe))
	q.AssertExpectations(t)
	return probe
}

func TestDayImportSteps_RunInDependencyOrder(t *testing.T) {
	steps := (&ImportActivities{}).dayImportSteps(ImportDayInput{})

	heartbeats := make([]string, len(steps))
	for i, step := range steps {
		heartbeats[i] = step.heartbeat
	}
	assert.Equal(t, []string{
		"boxscores", "game-stories", "play-by-play", "shift-charts", "season-series", "standings", "yahoo",
	}, heartbeats)
}

func TestDayImportSteps_YahooStepSkipsDayWithoutTeams(t *testing.T) {
	// A nil Yahoo importer panics if the step calls it.
	steps := (&ImportActivities{}).dayImportSteps(ImportDayInput{})

	result, err := steps[len(steps)-1].run(context.Background())

	require.NoError(t, err)
	assert.Equal(t, dayImportResult{}, result)
}

func TestImportDay_WrapsFailedStepWithItsName(t *testing.T) {
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	dbErr := errors.New("db failure")

	probe := runImportDay(t, seedImportDay(t, day), day, dbErr)

	assert.Contains(t, probe.Err, "import boxscores for 2024-01-15")
	assert.Contains(t, probe.Err, dbErr.Error())
}

func TestImportDay_FailedStepKeepsEarlierStepsCounts(t *testing.T) {
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	full := runImportDay(t, seedImportDay(t, day), day, nil)
	require.Empty(t, full.Err)
	mem := seedImportDay(t, day)
	require.NoError(t, mem.Delete(context.Background(), resource.PlayByPlay{Date: day, GameID: recordTestGameID}.Path()))

	partial := runImportDay(t, mem, day, nil)

	assert.Contains(t, partial.Err, "import play-by-play for 2024-01-15")
	assert.Equal(t, earlierStepReads, partial.Result.Origins.Total(),
		"the boxscore and game story reads are kept, nothing after the failed step runs")
	assert.Greater(t, full.Result.Origins.Total(), earlierStepReads)
}
