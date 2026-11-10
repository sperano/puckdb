package nhl

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
)

// importDayProbeActivity is the activity name under which the tests run
// importDay, whose partial result an activity error would otherwise hide.
const importDayProbeActivity = "ImportDayProbe"

// earlierStepReads is what the boxscore and game story steps read for the
// seeded day: the schedule and the game's file, each.
const earlierStepReads = 4

// corruptCachedFile is a cached file no importer can parse.
const corruptCachedFile = "not a cached file"

// emptyYahooRosterXML is a readable Yahoo roster with no players: the Yahoo
// step counts its read and writes nothing.
const emptyYahooRosterXML = `<fantasy_content><team><roster><players count="0"></players></roster></team></fantasy_content>`

// oneYahooPlayerRosterXML is a Yahoo roster with one player: the Yahoo step
// writes its row in a transaction.
const oneYahooPlayerRosterXML = `<fantasy_content><team><roster><players count="1"><player>` +
	`<player_id>6616</player_id><player_key>423.p.6616</player_key>` +
	`<selected_position><position>C</position><is_flex>0</is_flex></selected_position>` +
	`</player></players></roster></team></fantasy_content>`

// stepHeartbeats is what importDay records before each step, in order.
var stepHeartbeats = []string{
	"boxscores", "game-stories", "play-by-play", "shift-charts", "season-series", "standings", "yahoo",
}

// importDayTeam is the one Yahoo team of the tests' Yahoo days.
var importDayTeam = yahoo.TeamInfo{LeagueID: 12345, TeamID: 1}

// importDayProbe is importDay's result and error message, returned as one
// successful activity result, and the error itself, which the activity
// result cannot carry.
type importDayProbe struct {
	Result dayImportResult
	Err    string
	Cause  error `json:"-"`
}

// heartbeatRecorder keeps the string details of every heartbeat an activity
// records; the SDK's heartbeat listener only sees throttled ones.
type heartbeatRecorder struct {
	interceptor.WorkerInterceptorBase
	mu      sync.Mutex
	details []string
}

func (r *heartbeatRecorder) InterceptActivity(_ context.Context, next interceptor.ActivityInboundInterceptor) interceptor.ActivityInboundInterceptor {
	in := &heartbeatRecorderInbound{recorder: r}
	in.Next = next
	return in
}

// recorded is the heartbeats recorded by importDay itself, without the
// per-game ones its importers record (named "<step>:game:<id>").
func (r *heartbeatRecorder) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var steps []string
	for _, detail := range r.details {
		if !strings.Contains(detail, ":") {
			steps = append(steps, detail)
		}
	}
	return steps
}

type heartbeatRecorderInbound struct {
	interceptor.ActivityInboundInterceptorBase
	recorder *heartbeatRecorder
}

func (in *heartbeatRecorderInbound) Init(outbound interceptor.ActivityOutboundInterceptor) error {
	out := &heartbeatRecorderOutbound{recorder: in.recorder}
	out.Next = outbound
	return in.Next.Init(out)
}

type heartbeatRecorderOutbound struct {
	interceptor.ActivityOutboundInterceptorBase
	recorder *heartbeatRecorder
}

func (out *heartbeatRecorderOutbound) RecordHeartbeat(ctx context.Context, details ...any) {
	out.recorder.mu.Lock()
	for _, detail := range details {
		if s, ok := detail.(string); ok {
			out.recorder.details = append(out.recorder.details, s)
		}
	}
	out.recorder.mu.Unlock()
	out.Next.RecordHeartbeat(ctx, details...)
}

// failingYahooTx fails every Yahoo write transaction.
type failingYahooTx struct{ err error }

func (f failingYahooTx) InTx(context.Context, func(yahoo.YahooDataUpserter) error) error {
	return f.err
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

// newImportDayActivities returns activities over mem whose queries upsert
// the day's one game with upsertErr's outcome, and a Yahoo importer over the
// same storage that writes nothing.
func newImportDayActivities(mem *store.MemStorage, upsertErr error) (*ImportActivities, *MockQueries) {
	q := &MockQueries{}
	q.On("UpsertGame", mock.Anything, mock.AnythingOfType("sqlcdb.UpsertGameParams")).Return(upsertErr).Once()
	yahooImport := &yahoo.ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil)}
	return &ImportActivities{Storage: mem, GobCache: newImportTestGobCache(), Queries: q, Yahoo: yahooImport}, q
}

// importDayInput is the seeded day, with the given Yahoo teams.
func importDayInput(day time.Time, teams ...yahoo.TeamInfo) ImportDayInput {
	return ImportDayInput{Date: day, Season: 2023, SeasonID: recordTestSeasonID, TeamIDs: teams}
}

// runImportDayWith runs act.importDay in an activity environment and returns
// its probe, with the error importDay returned, and the heartbeats it
// recorded.
func runImportDayWith(t *testing.T, act *ImportActivities, input ImportDayInput) (importDayProbe, []string) {
	t.Helper()
	heartbeats := &heartbeatRecorder{}
	var env testsuite.WorkflowTestSuite
	activityEnv := env.NewTestActivityEnvironment()
	activityEnv.SetWorkerOptions(worker.Options{Interceptors: []interceptor.WorkerInterceptor{heartbeats}})
	var cause error
	activityEnv.RegisterActivityWithOptions(func(ctx context.Context, input ImportDayInput) (importDayProbe, error) {
		result, err := act.importDay(ctx, input)
		cause = err
		probe := importDayProbe{Result: result}
		if err != nil {
			probe.Err = err.Error()
		}
		return probe, nil
	}, activity.RegisterOptions{Name: importDayProbeActivity})
	future, err := activityEnv.ExecuteActivity(importDayProbeActivity, input)
	require.NoError(t, err)
	var probe importDayProbe
	require.NoError(t, future.Get(&probe))
	probe.Cause = cause
	return probe, heartbeats.recorded()
}

// runImportDay runs importDay over mem for the day without Yahoo teams, with
// the day's one game upserted by upsertErr's outcome.
func runImportDay(t *testing.T, mem *store.MemStorage, day time.Time, upsertErr error) importDayProbe {
	t.Helper()
	act, q := newImportDayActivities(mem, upsertErr)
	probe, _ := runImportDayWith(t, act, importDayInput(day))
	q.AssertExpectations(t)
	return probe
}

func TestDayImportSteps_RunInDependencyOrder(t *testing.T) {
	steps := (&ImportActivities{}).dayImportSteps(ImportDayInput{})

	type stepNames struct{ heartbeat, what string }
	names := make([]stepNames, len(steps))
	for i, step := range steps {
		names[i] = stepNames{heartbeat: step.heartbeat, what: step.what}
	}
	assert.Equal(t, []stepNames{
		{"boxscores", "boxscores"},
		{"game-stories", "game story"},
		{"play-by-play", "play-by-play"},
		{"shift-charts", "shift chart"},
		{"season-series", "season series"},
		{"standings", "standings"},
		{"yahoo", "Yahoo data"},
	}, names)
}

func TestDayImportSteps_YahooStepSkipsDayWithoutTeams(t *testing.T) {
	// A nil Yahoo importer panics if the step calls it.
	steps := (&ImportActivities{}).dayImportSteps(ImportDayInput{})

	result, err := steps[len(steps)-1].run(context.Background())

	require.NoError(t, err)
	assert.Equal(t, dayImportResult{}, result)
}

func TestImportDay_RecordsEachStepHeartbeatInOrder(t *testing.T) {
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	act, q := newImportDayActivities(seedImportDay(t, day), nil)

	probe, heartbeats := runImportDayWith(t, act, importDayInput(day))

	require.Empty(t, probe.Err)
	assert.Equal(t, stepHeartbeats, heartbeats)
	q.AssertExpectations(t)
}

func TestImportDay_WrapsFailedStepWithItsName(t *testing.T) {
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	dbErr := errors.New("db failure")

	probe := runImportDay(t, seedImportDay(t, day), day, dbErr)

	assert.Contains(t, probe.Err, "import boxscores for 2024-01-15")
	require.ErrorIs(t, probe.Cause, dbErr, "the failure classifier sees the step's cause")
}

// Each step whose importer can fail on its own input names its data in the
// day's error and stops the day at its heartbeat. The game story and
// season-series steps cannot: they skip a game's failed rows, and their only
// error is the schedule read, which the boxscore step makes first, so the
// step table test pins their names.
func TestImportDay_FailedStepNamesItsData(t *testing.T) {
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	corrupt := func(path string) func(*testing.T, *store.MemStorage) {
		return func(t *testing.T, mem *store.MemStorage) {
			require.NoError(t, mem.Write(context.Background(), path, []byte(corruptCachedFile)))
		}
	}
	cases := []struct {
		what      string
		heartbeat string
		breakStep func(*testing.T, *store.MemStorage)
	}{
		{"boxscores", "boxscores", corrupt(resource.Boxscore{Date: day, GameID: recordTestGameID}.Path())},
		{"play-by-play", "play-by-play", corrupt(resource.PlayByPlay{Date: day, GameID: recordTestGameID}.Path())},
		{"shift chart", "shift-charts", corrupt(resource.ShiftChart{Date: day, GameID: recordTestGameID}.Path())},
		{"standings", "standings", corrupt(resource.DailyStandings{Date: day}.Path())},
	}
	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			mem := seedImportDay(t, day)
			tc.breakStep(t, mem)
			act, _ := newImportDayActivities(mem, nil)

			probe, heartbeats := runImportDayWith(t, act, importDayInput(day))

			assert.Contains(t, probe.Err, "import "+tc.what+" for 2024-01-15")
			require.NotEmpty(t, heartbeats)
			assert.Equal(t, tc.heartbeat, heartbeats[len(heartbeats)-1], "no step runs after the failed one")
		})
	}
}

func TestImportDay_FailedYahooStepNamesItsData(t *testing.T) {
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	mem := seedImportDay(t, day)
	roster := resource.Roster{LeagueID: importDayTeam.LeagueID, TeamID: importDayTeam.TeamID, Date: day}
	require.NoError(t, mem.Write(context.Background(), roster.Path(), []byte(oneYahooPlayerRosterXML)))
	act, q := newImportDayActivities(mem, nil)
	txErr := errors.New("yahoo tx failure")
	act.Yahoo.Tx = failingYahooTx{err: txErr}

	probe, _ := runImportDayWith(t, act, importDayInput(day, importDayTeam))

	assert.Contains(t, probe.Err, "import Yahoo data for 2024-01-15")
	require.ErrorIs(t, probe.Cause, txErr, "the failure classifier sees the step's cause")
	q.AssertExpectations(t)
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

// The Yahoo step adds its reads to the day's origins and its unreadable
// files (summaries, standings and rosters, one each here) to the day's
// skipped rows.
func TestImportDay_YahooStepCountsReadsAndReadFailures(t *testing.T) {
	day := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	withoutYahoo := runImportDay(t, seedImportDay(t, day), day, nil)
	require.Empty(t, withoutYahoo.Err)
	mem := seedImportDay(t, day)
	secondTeam := yahoo.TeamInfo{LeagueID: importDayTeam.LeagueID, TeamID: importDayTeam.TeamID + 1}
	write := func(path, content string) {
		require.NoError(t, mem.Write(context.Background(), path, []byte(content)))
	}
	write(resource.Roster{LeagueID: importDayTeam.LeagueID, TeamID: importDayTeam.TeamID, Date: day}.Path(), emptyYahooRosterXML)
	write(resource.TeamSummary{LeagueID: importDayTeam.LeagueID, TeamID: importDayTeam.TeamID, Date: day}.Path(), corruptCachedFile)
	write(resource.Roster{LeagueID: secondTeam.LeagueID, TeamID: secondTeam.TeamID, Date: day}.Path(), corruptCachedFile)
	write(resource.LeagueStandings{Season: importDayInput(day).Season, LeagueID: importDayTeam.LeagueID, Date: day}.Path(), corruptCachedFile)
	act, q := newImportDayActivities(mem, nil)

	probe, _ := runImportDayWith(t, act, importDayInput(day, importDayTeam, secondTeam))

	require.Empty(t, probe.Err)
	assert.Equal(t, 3, probe.Result.SkippedRows, "the unreadable summary, standings and roster")
	assert.Equal(t, withoutYahoo.Result.Origins.Total()+1, probe.Result.Origins.Total(), "the roster read")
	assert.Equal(t, withoutYahoo.Result.Origins[core.OriginFileSystem]+1, probe.Result.Origins[core.OriginFileSystem])
	q.AssertExpectations(t)
}

// seedGameStoryWithThreeStar replaces the day's game story with one naming a
// first star.
func seedGameStoryWithThreeStar(t *testing.T, mem *store.MemStorage, day time.Time) {
	t.Helper()
	data, err := json.Marshal(&nhlapi.GameStory{
		Season:            nhlapi.NewSeason(2023),
		GameType:          nhlapi.GameTypeRegularSeason,
		GameState:         nhlapi.GameStateOff,
		GameScheduleState: nhlapi.GameScheduleStateOK,
		Summary:           &nhlapi.GameSummary{ThreeStars: []nhlapi.ThreeStar{{Star: 1, PlayerID: nhlapi.PlayerID(1)}}},
	})
	require.NoError(t, err)
	require.NoError(t, mem.Write(context.Background(), resource.GameStory{Date: day, GameID: recordTestGameID}.Path(), data))
}
