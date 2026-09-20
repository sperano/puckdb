package nhl

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// =============================================================================
// Shared fixtures for import_edge_test.go and import_edge_liveness_test.go.
// =============================================================================

const (
	testEdgeSeasonStartYear = 2023
	testEdgeGameTypeInt     = int(nhlapi.GameTypeRegularSeason)
	testEdgeTeamAbbrev      = "MTL"
	testEdgeTeamID          = int64(8)

	testSkaterID1 = int64(8478463)
	testSkaterID2 = int64(8477934)
	testSkaterID3 = int64(8471214)
	testSkaterID4 = int64(8480012)
	testGoalieID1 = int64(8479406)
	testGoalieID2 = int64(8471695)

	// Mapping spot-check values: pinned so parity tests catch accidental
	// regressions in the field mapping, not just call counts.
	skaterTopSpeedImperial      = 22.5
	skaterSogLocationCode       = "slot"
	goalieGAAValue              = 2.45
	teamZoneStrengthCode        = "5v5"
	teamShotAttemptDifferential = 3.25
	teamSOGDifferential         = 1.5
)

// testScope returns the edgeImportScope every fixture in this file is keyed by.
func testScope() edgeImportScope {
	return newEdgeImportScope(testEdgeSeasonStartYear, testEdgeGameTypeInt)
}

// testTeamInput returns the ImportEdgeTeamInput for the fixture team/season.
func testTeamInput() ImportEdgeTeamInput {
	return ImportEdgeTeamInput{
		Season:     testEdgeSeasonStartYear,
		GameType:   testEdgeGameTypeInt,
		TeamID:     testEdgeTeamID,
		TeamAbbrev: testEdgeTeamAbbrev,
	}
}

// =============================================================================
// fakeEdgeUpserter: a hand-rolled EdgeStatsUpserter fake. It runs on the
// activity's goroutine (which may differ from the test goroutine, and — for
// the heartbeat-batching flush — a background SDK goroutine), so every field
// is mutex-protected for -race safety.
// =============================================================================

// edgeUpsertCall records one upsert call: the method name, the player/team ID
// it was keyed by, and the full params struct passed in.
type edgeUpsertCall struct {
	method string
	id     int64
	params any
}

// fakeEdgeUpserter is a hand-rolled EdgeStatsUpserter for tests. Configure it
// with setDelay/setErrFor/setOnSkaterStats/setAbbrevs before executing the
// activity; read results back with allCalls/callsFor after.
type fakeEdgeUpserter struct {
	mu            sync.Mutex
	calls         []edgeUpsertCall
	delay         time.Duration
	errByID       map[int64]error
	onSkaterStats func(playerID int64)
	abbrevs       []sqlcdb.GetSeasonTeamAbbrevsRow
	abbrevsErr    error
}

func newFakeEdgeUpserter() *fakeEdgeUpserter {
	return &fakeEdgeUpserter{errByID: make(map[int64]error)}
}

func (f *fakeEdgeUpserter) setDelay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delay = d
}

func (f *fakeEdgeUpserter) setErrFor(id int64, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errByID[id] = err
}

func (f *fakeEdgeUpserter) setOnSkaterStats(fn func(playerID int64)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onSkaterStats = fn
}

func (f *fakeEdgeUpserter) setAbbrevs(rows []sqlcdb.GetSeasonTeamAbbrevsRow, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.abbrevs, f.abbrevsErr = rows, err
}

// allCalls returns every recorded call, in order.
func (f *fakeEdgeUpserter) allCalls() []edgeUpsertCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// callsFor returns the recorded calls for one method, in order.
func (f *fakeEdgeUpserter) callsFor(method string) []edgeUpsertCall {
	var out []edgeUpsertCall
	for _, c := range f.allCalls() {
		if c.method == method {
			out = append(out, c)
		}
	}
	return out
}

// record appends a call, applies the configured delay (simulating a slow
// upsert), and returns the configured error (if any) for that ID. The
// skater-stats hook — used by the cancellation test to cancel mid-roster —
// fires only for UpsertEdgeSkaterStats, after the delay.
func (f *fakeEdgeUpserter) record(method string, id int64, params any) error {
	f.mu.Lock()
	f.calls = append(f.calls, edgeUpsertCall{method: method, id: id, params: params})
	delay := f.delay
	err := f.errByID[id]
	hook := f.onSkaterStats
	f.mu.Unlock()

	if delay > 0 {
		time.Sleep(delay)
	}
	if method == "UpsertEdgeSkaterStats" && hook != nil {
		hook(id)
	}
	return err
}

func (f *fakeEdgeUpserter) UpsertEdgeSkaterStats(_ context.Context, arg sqlcdb.UpsertEdgeSkaterStatsParams) error {
	return f.record("UpsertEdgeSkaterStats", arg.PlayerID, arg)
}

func (f *fakeEdgeUpserter) UpsertEdgeSkaterShotLocation(_ context.Context, arg sqlcdb.UpsertEdgeSkaterShotLocationParams) error {
	return f.record("UpsertEdgeSkaterShotLocation", arg.PlayerID, arg)
}

func (f *fakeEdgeUpserter) UpsertEdgeSkaterSogSummary(_ context.Context, arg sqlcdb.UpsertEdgeSkaterSogSummaryParams) error {
	return f.record("UpsertEdgeSkaterSogSummary", arg.PlayerID, arg)
}

func (f *fakeEdgeUpserter) UpsertEdgeGoalieStats(_ context.Context, arg sqlcdb.UpsertEdgeGoalieStatsParams) error {
	return f.record("UpsertEdgeGoalieStats", arg.PlayerID, arg)
}

func (f *fakeEdgeUpserter) UpsertEdgeGoalieShotLocationSummary(_ context.Context, arg sqlcdb.UpsertEdgeGoalieShotLocationSummaryParams) error {
	return f.record("UpsertEdgeGoalieShotLocationSummary", arg.PlayerID, arg)
}

func (f *fakeEdgeUpserter) UpsertEdgeGoalieShotLocation(_ context.Context, arg sqlcdb.UpsertEdgeGoalieShotLocationParams) error {
	return f.record("UpsertEdgeGoalieShotLocation", arg.PlayerID, arg)
}

func (f *fakeEdgeUpserter) UpsertEdgeTeamStats(_ context.Context, arg sqlcdb.UpsertEdgeTeamStatsParams) error {
	return f.record("UpsertEdgeTeamStats", arg.TeamID, arg)
}

func (f *fakeEdgeUpserter) UpsertEdgeTeamSogSummary(_ context.Context, arg sqlcdb.UpsertEdgeTeamSogSummaryParams) error {
	return f.record("UpsertEdgeTeamSogSummary", arg.TeamID, arg)
}

func (f *fakeEdgeUpserter) UpsertEdgeTeamShotLocation(_ context.Context, arg sqlcdb.UpsertEdgeTeamShotLocationParams) error {
	return f.record("UpsertEdgeTeamShotLocation", arg.TeamID, arg)
}

func (f *fakeEdgeUpserter) UpsertEdgeTeamZoneTimeByStrength(_ context.Context, arg sqlcdb.UpsertEdgeTeamZoneTimeByStrengthParams) error {
	return f.record("UpsertEdgeTeamZoneTimeByStrength", arg.TeamID, arg)
}

func (f *fakeEdgeUpserter) UpsertEdgeTeamShotDifferential(_ context.Context, arg sqlcdb.UpsertEdgeTeamShotDifferentialParams) error {
	return f.record("UpsertEdgeTeamShotDifferential", arg.TeamID, arg)
}

func (f *fakeEdgeUpserter) GetSeasonTeamAbbrevs(_ context.Context, _ int32) ([]sqlcdb.GetSeasonTeamAbbrevsRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.abbrevs, f.abbrevsErr
}

// =============================================================================
// Roster / detail fixture builders.
// =============================================================================

func newRosterPlayer(id int64, pos nhlapi.Position) nhlapi.RosterPlayer {
	return nhlapi.RosterPlayer{
		ID:            nhlapi.NewPlayerID(id),
		FirstName:     nhlapi.LocalizedString{Default: "Test"},
		LastName:      nhlapi.LocalizedString{Default: fmt.Sprintf("Player%d", id)},
		Position:      pos,
		ShootsCatches: nhlapi.HandednessLeft,
		BirthCountry:  "CAN",
	}
}

// rosterWithForwards builds a roster whose forwards are the given player IDs.
func rosterWithForwards(ids ...int64) *nhlapi.Roster {
	r := &nhlapi.Roster{}
	for _, id := range ids {
		r.Forwards = append(r.Forwards, newRosterPlayer(id, nhlapi.PositionCenter))
	}
	return r
}

// rosterWithGoalies builds a roster whose goalies are the given player IDs.
func rosterWithGoalies(ids ...int64) *nhlapi.Roster {
	r := &nhlapi.Roster{}
	for _, id := range ids {
		r.Goalies = append(r.Goalies, newRosterPlayer(id, nhlapi.PositionGoalie))
	}
	return r
}

// baseSkaterDetail returns a realistic, minimal EdgeSkaterDetail with one SOG
// summary entry, pinning skaterTopSpeedImperial/skaterSogLocationCode.
func baseSkaterDetail() *nhlapi.EdgeSkaterDetail {
	return &nhlapi.EdgeSkaterDetail{
		SkatingSpeed: nhlapi.EdgeSkaterSpeed{
			SpeedMax: nhlapi.EdgePercentileStatWithOverlay{Imperial: skaterTopSpeedImperial, Metric: 36.2, Percentile: 91.5},
		},
		SogSummary: []nhlapi.EdgeSkaterSogSummary{{LocationCode: skaterSogLocationCode, Shots: 12}},
	}
}

// baseGoalieDetail returns a realistic, minimal EdgeGoalieDetail pinning goalieGAAValue.
func baseGoalieDetail() *nhlapi.EdgeGoalieDetail {
	return &nhlapi.EdgeGoalieDetail{
		Stats: nhlapi.EdgeGoalieStatsSummary{
			GoalsAgainstAvg: nhlapi.EdgeGoalieStatEntry{Value: goalieGAAValue, Percentile: 40.1, LeagueAvg: 2.9},
		},
	}
}

// baseTeamDetail returns a minimal, valid EdgeTeamDetail (no sub-slices, so
// exactly one upsert call — UpsertEdgeTeamStats — results from importing it).
func baseTeamDetail() *nhlapi.EdgeTeamDetail {
	return &nhlapi.EdgeTeamDetail{}
}

// baseTeamZoneTimeDetail returns a realistic EdgeTeamZoneTimeDetails pinning
// teamZoneStrengthCode and teamShotAttemptDifferential/teamSOGDifferential.
func baseTeamZoneTimeDetail() *nhlapi.EdgeTeamZoneTimeDetails {
	return &nhlapi.EdgeTeamZoneTimeDetails{
		ZoneTimeDetails: []nhlapi.EdgeTeamZoneTimeByStrength{
			{StrengthCode: teamZoneStrengthCode, OffensiveZonePctg: 55.5, OffensiveZoneRank: 3},
		},
		ShotDifferential: &nhlapi.EdgeTeamShotDifferential{
			ShotAttemptDifferential: teamShotAttemptDifferential,
			SOGDifferential:         teamSOGDifferential,
		},
	}
}

// =============================================================================
// Seeding helpers. All resource types here have a Format method, so
// resource.WriteParsed marshals and writes in one step.
// =============================================================================

func seedSeasonRoster(t *testing.T, mem *store.MemStorage, teamAbbrev string, seasonStartYear int, roster *nhlapi.Roster) {
	t.Helper()
	res := resource.SeasonRoster{Season: seasonStartYear, TeamAbbrev: teamAbbrev}
	require.NoError(t, resource.WriteParsed(context.Background(), mem, res, roster))
}

func seedEdgeSkaterDetail(t *testing.T, mem *store.MemStorage, playerID int64, season nhlapi.Season, gameType nhlapi.GameType, detail *nhlapi.EdgeSkaterDetail) {
	t.Helper()
	res := resource.EdgeSkaterDetail{PlayerID: nhlapi.NewPlayerID(playerID), Season: season, GameType: gameType}
	require.NoError(t, resource.WriteParsed(context.Background(), mem, res, detail))
}

func seedEdgeGoalieDetail(t *testing.T, mem *store.MemStorage, goalieID int64, season nhlapi.Season, gameType nhlapi.GameType, detail *nhlapi.EdgeGoalieDetail) {
	t.Helper()
	res := resource.EdgeGoalieDetail{GoalieID: nhlapi.NewPlayerID(goalieID), Season: season, GameType: gameType}
	require.NoError(t, resource.WriteParsed(context.Background(), mem, res, detail))
}

func seedEdgeTeamDetail(t *testing.T, mem *store.MemStorage, teamID int64, season nhlapi.Season, gameType nhlapi.GameType, detail *nhlapi.EdgeTeamDetail) {
	t.Helper()
	res := resource.EdgeTeamDetail{TeamID: nhlapi.TeamID(teamID), Season: season, GameType: gameType}
	require.NoError(t, resource.WriteParsed(context.Background(), mem, res, detail))
}

func seedEdgeTeamZoneTimeDetail(t *testing.T, mem *store.MemStorage, teamID int64, season nhlapi.Season, gameType nhlapi.GameType, detail *nhlapi.EdgeTeamZoneTimeDetails) {
	t.Helper()
	res := resource.EdgeTeamZoneTimeDetails{TeamID: nhlapi.TeamID(teamID), Season: season, GameType: gameType}
	require.NoError(t, resource.WriteParsed(context.Background(), mem, res, detail))
}

// =============================================================================
// Parity tests: legacy season-wide entry points vs per-team entry points must
// produce identical recorded upsert calls for the same seeded team.
// =============================================================================

type ImportEdgeParitySuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
}

func TestImportEdgeParitySuite(t *testing.T) {
	suite.Run(t, new(ImportEdgeParitySuite))
}

func (s *ImportEdgeParitySuite) TestSkaters_LegacyMatchesPerTeam() {
	mem := store.NewMemStorage()
	scope := testScope()
	seedSeasonRoster(s.T(), mem, testEdgeTeamAbbrev, testEdgeSeasonStartYear, rosterWithForwards(testSkaterID1, testSkaterID2))
	seedEdgeSkaterDetail(s.T(), mem, testSkaterID1, scope.season, scope.gameType, baseSkaterDetail())
	seedEdgeSkaterDetail(s.T(), mem, testSkaterID2, scope.season, scope.gameType, baseSkaterDetail())

	legacyFake := newFakeEdgeUpserter()
	legacyFake.setAbbrevs([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: testEdgeTeamID, Abbrev: testEdgeTeamAbbrev}}, nil)
	legacyAct := &SeasonsActivities{Storage: mem, EdgeQueries: legacyFake}
	legacyEnv := s.NewTestActivityEnvironment()
	legacyEnv.RegisterActivity(legacyAct.ImportEdgeSkaters)
	_, err := legacyEnv.ExecuteActivity(legacyAct.ImportEdgeSkaters, FetchEdgeInput{Season: testEdgeSeasonStartYear, GameType: testEdgeGameTypeInt})
	s.Require().NoError(err)

	perTeamFake := newFakeEdgeUpserter()
	perTeamAct := &SeasonsActivities{Storage: mem, EdgeQueries: perTeamFake}
	perTeamEnv := s.NewTestActivityEnvironment()
	perTeamEnv.RegisterActivity(perTeamAct.ImportEdgeTeamSkaters)
	_, err = perTeamEnv.ExecuteActivity(perTeamAct.ImportEdgeTeamSkaters, testTeamInput())
	s.Require().NoError(err)

	s.Equal(legacyFake.allCalls(), perTeamFake.allCalls())

	statsCalls := perTeamFake.callsFor("UpsertEdgeSkaterStats")
	s.Require().Len(statsCalls, 2)
	params := statsCalls[0].params.(sqlcdb.UpsertEdgeSkaterStatsParams)
	s.Equal(float32(skaterTopSpeedImperial), params.TopSpeedImperial.Float32)

	sogCalls := perTeamFake.callsFor("UpsertEdgeSkaterSogSummary")
	s.Require().Len(sogCalls, 2)
	sogParams := sogCalls[0].params.(sqlcdb.UpsertEdgeSkaterSogSummaryParams)
	s.Equal(skaterSogLocationCode, sogParams.LocationCode)
}

func (s *ImportEdgeParitySuite) TestGoalies_LegacyMatchesPerTeam() {
	mem := store.NewMemStorage()
	scope := testScope()
	seedSeasonRoster(s.T(), mem, testEdgeTeamAbbrev, testEdgeSeasonStartYear, rosterWithGoalies(testGoalieID1, testGoalieID2))
	seedEdgeGoalieDetail(s.T(), mem, testGoalieID1, scope.season, scope.gameType, baseGoalieDetail())
	seedEdgeGoalieDetail(s.T(), mem, testGoalieID2, scope.season, scope.gameType, baseGoalieDetail())

	legacyFake := newFakeEdgeUpserter()
	legacyFake.setAbbrevs([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: testEdgeTeamID, Abbrev: testEdgeTeamAbbrev}}, nil)
	legacyAct := &SeasonsActivities{Storage: mem, EdgeQueries: legacyFake}
	legacyEnv := s.NewTestActivityEnvironment()
	legacyEnv.RegisterActivity(legacyAct.ImportEdgeGoalies)
	_, err := legacyEnv.ExecuteActivity(legacyAct.ImportEdgeGoalies, FetchEdgeInput{Season: testEdgeSeasonStartYear, GameType: testEdgeGameTypeInt})
	s.Require().NoError(err)

	perTeamFake := newFakeEdgeUpserter()
	perTeamAct := &SeasonsActivities{Storage: mem, EdgeQueries: perTeamFake}
	perTeamEnv := s.NewTestActivityEnvironment()
	perTeamEnv.RegisterActivity(perTeamAct.ImportEdgeTeamGoalies)
	_, err = perTeamEnv.ExecuteActivity(perTeamAct.ImportEdgeTeamGoalies, testTeamInput())
	s.Require().NoError(err)

	s.Equal(legacyFake.allCalls(), perTeamFake.allCalls())

	statsCalls := perTeamFake.callsFor("UpsertEdgeGoalieStats")
	s.Require().Len(statsCalls, 2)
	params := statsCalls[0].params.(sqlcdb.UpsertEdgeGoalieStatsParams)
	s.Equal(float32(goalieGAAValue), params.GaaValue.Float32)
}

func (s *ImportEdgeParitySuite) TestTeam_LegacyMatchesPerTeam() {
	mem := store.NewMemStorage()
	scope := testScope()
	seedEdgeTeamDetail(s.T(), mem, testEdgeTeamID, scope.season, scope.gameType, baseTeamDetail())
	seedEdgeTeamZoneTimeDetail(s.T(), mem, testEdgeTeamID, scope.season, scope.gameType, baseTeamZoneTimeDetail())

	legacyFake := newFakeEdgeUpserter()
	legacyFake.setAbbrevs([]sqlcdb.GetSeasonTeamAbbrevsRow{{TeamID: testEdgeTeamID, Abbrev: testEdgeTeamAbbrev}}, nil)
	legacyAct := &SeasonsActivities{Storage: mem, EdgeQueries: legacyFake}
	legacyEnv := s.NewTestActivityEnvironment()
	legacyEnv.RegisterActivity(legacyAct.ImportEdgeTeams)
	legacyEnv.RegisterActivity(legacyAct.ImportEdgeTeamZoneTimeDetails)
	legacyInput := FetchEdgeInput{Season: testEdgeSeasonStartYear, GameType: testEdgeGameTypeInt}
	_, err := legacyEnv.ExecuteActivity(legacyAct.ImportEdgeTeams, legacyInput)
	s.Require().NoError(err)
	_, err = legacyEnv.ExecuteActivity(legacyAct.ImportEdgeTeamZoneTimeDetails, legacyInput)
	s.Require().NoError(err)

	perTeamFake := newFakeEdgeUpserter()
	perTeamAct := &SeasonsActivities{Storage: mem, EdgeQueries: perTeamFake}
	perTeamEnv := s.NewTestActivityEnvironment()
	perTeamEnv.RegisterActivity(perTeamAct.ImportEdgeTeam)
	_, err = perTeamEnv.ExecuteActivity(perTeamAct.ImportEdgeTeam, testTeamInput())
	s.Require().NoError(err)

	s.Equal(legacyFake.allCalls(), perTeamFake.allCalls())

	ztCalls := perTeamFake.callsFor("UpsertEdgeTeamZoneTimeByStrength")
	s.Require().Len(ztCalls, 1)
	ztParams := ztCalls[0].params.(sqlcdb.UpsertEdgeTeamZoneTimeByStrengthParams)
	s.Equal(teamZoneStrengthCode, ztParams.StrengthCode)

	sdCalls := perTeamFake.callsFor("UpsertEdgeTeamShotDifferential")
	s.Require().Len(sdCalls, 1)
	sdParams := sdCalls[0].params.(sqlcdb.UpsertEdgeTeamShotDifferentialParams)
	s.Equal(float32(teamShotAttemptDifferential), sdParams.ShotAttemptDifferential.Float32)
	s.Equal(float32(teamSOGDifferential), sdParams.SogDifferential.Float32)
}

// =============================================================================
// Error-behavior tests.
// =============================================================================

type ImportEdgeErrorSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestActivityEnvironment
}

func (s *ImportEdgeErrorSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
}

func TestImportEdgeErrorSuite(t *testing.T) {
	suite.Run(t, new(ImportEdgeErrorSuite))
}

func (s *ImportEdgeErrorSuite) TestCorruptDetailFile_SkippedRestImport() {
	mem := store.NewMemStorage()
	scope := testScope()
	seedSeasonRoster(s.T(), mem, testEdgeTeamAbbrev, testEdgeSeasonStartYear, rosterWithForwards(testSkaterID1, testSkaterID2))
	corruptRes := resource.EdgeSkaterDetail{PlayerID: nhlapi.NewPlayerID(testSkaterID1), Season: scope.season, GameType: scope.gameType}
	s.Require().NoError(mem.Write(context.Background(), corruptRes.Path(), []byte("{not valid json")))
	seedEdgeSkaterDetail(s.T(), mem, testSkaterID2, scope.season, scope.gameType, baseSkaterDetail())

	fake := newFakeEdgeUpserter()
	act := &SeasonsActivities{Storage: mem, EdgeQueries: fake}
	s.env.RegisterActivity(act.ImportEdgeTeamSkaters)

	_, err := s.env.ExecuteActivity(act.ImportEdgeTeamSkaters, testTeamInput())
	s.Require().NoError(err)

	statsCalls := fake.callsFor("UpsertEdgeSkaterStats")
	s.Require().Len(statsCalls, 1)
	s.Equal(testSkaterID2, statsCalls[0].id)
}

func (s *ImportEdgeErrorSuite) TestUpsertError_AbortsWithKindAndPlayer() {
	mem := store.NewMemStorage()
	scope := testScope()
	seedSeasonRoster(s.T(), mem, testEdgeTeamAbbrev, testEdgeSeasonStartYear, rosterWithForwards(testSkaterID1))
	seedEdgeSkaterDetail(s.T(), mem, testSkaterID1, scope.season, scope.gameType, baseSkaterDetail())

	fake := newFakeEdgeUpserter()
	fake.setErrFor(testSkaterID1, errors.New("db exploded"))
	act := &SeasonsActivities{Storage: mem, EdgeQueries: fake}
	s.env.RegisterActivity(act.ImportEdgeTeamSkaters)

	_, err := s.env.ExecuteActivity(act.ImportEdgeTeamSkaters, testTeamInput())
	s.Require().Error(err)
	s.Contains(err.Error(), edgeKindSkater)
	s.Contains(err.Error(), fmt.Sprintf("%d", testSkaterID1))
}

func (s *ImportEdgeErrorSuite) TestMissingRoster_NilErrorZeroUpserts() {
	mem := store.NewMemStorage()
	fake := newFakeEdgeUpserter()
	act := &SeasonsActivities{Storage: mem, EdgeQueries: fake}
	s.env.RegisterActivity(act.ImportEdgeTeamSkaters)

	_, err := s.env.ExecuteActivity(act.ImportEdgeTeamSkaters, testTeamInput())
	s.Require().NoError(err)
	s.Empty(fake.allCalls())
}

func (s *ImportEdgeErrorSuite) TestGetSeasonTeamAbbrevsError_SurfacesFromLegacyEntryPoint() {
	mem := store.NewMemStorage()
	fake := newFakeEdgeUpserter()
	fake.setAbbrevs(nil, errors.New("db down"))
	act := &SeasonsActivities{Storage: mem, EdgeQueries: fake}
	s.env.RegisterActivity(act.ImportEdgeSkaters)

	_, err := s.env.ExecuteActivity(act.ImportEdgeSkaters, FetchEdgeInput{Season: testEdgeSeasonStartYear, GameType: testEdgeGameTypeInt})
	s.Require().Error(err)
	s.Contains(err.Error(), "get season teams")
}
