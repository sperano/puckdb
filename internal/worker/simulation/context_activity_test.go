package simulation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/pgconv"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

// contextTestAgentID is the agent whose rows the context loaders read.
const contextTestAgentID = 7

// rosterContextQueriesFake is BuildManageRosterContext's query group:
// its standings, waiver-pool and pending-claim reads over the shared
// agent, roster and player fakes.
type rosterContextQueriesFake struct {
	*agentFake
	*rosterFake
	*playerFake

	getSimStandingsLatestDateReturn pgtype.Date
	getSimStandingsLatestDateErr    error
	listSimStandingsByDateRows      []sqlcdb.SimStanding
	listSimStandingsByDateErr       error

	// ListSimPlayersOnWaivers
	listPlayersOnWaiversArgs   []sqlcdb.ListSimPlayersOnWaiversParams
	listPlayersOnWaiversReturn []sqlcdb.ListSimPlayersOnWaiversRow
	listPlayersOnWaiversErr    error

	// ListSimWaiverClaimsPendingByAgent — agent_id → pending claims.
	listPendingByAgentArgs []sqlcdb.ListSimWaiverClaimsPendingByAgentParams
	listPendingByAgentRows map[int32][]sqlcdb.SimWaiverClaim
	listPendingByAgentErr  error
}

var _ rosterContextQueries = (*rosterContextQueriesFake)(nil)

func newRosterContextQueriesFake() *rosterContextQueriesFake {
	return &rosterContextQueriesFake{agentFake: &agentFake{}, rosterFake: &rosterFake{}, playerFake: &playerFake{}}
}

func (s *rosterContextQueriesFake) GetSimStandingsLatestDate(_ context.Context, _ int32) (pgtype.Date, error) {
	if s.getSimStandingsLatestDateErr != nil {
		return pgtype.Date{}, s.getSimStandingsLatestDateErr
	}
	return s.getSimStandingsLatestDateReturn, nil
}

func (s *rosterContextQueriesFake) ListSimStandingsByDate(_ context.Context, _ sqlcdb.ListSimStandingsByDateParams) ([]sqlcdb.SimStanding, error) {
	if s.listSimStandingsByDateErr != nil {
		return nil, s.listSimStandingsByDateErr
	}
	return s.listSimStandingsByDateRows, nil
}

func (s *rosterContextQueriesFake) ListSimPlayersOnWaivers(_ context.Context, arg sqlcdb.ListSimPlayersOnWaiversParams) ([]sqlcdb.ListSimPlayersOnWaiversRow, error) {
	s.listPlayersOnWaiversArgs = append(s.listPlayersOnWaiversArgs, arg)
	if s.listPlayersOnWaiversErr != nil {
		return nil, s.listPlayersOnWaiversErr
	}
	return s.listPlayersOnWaiversReturn, nil
}

func (s *rosterContextQueriesFake) ListSimWaiverClaimsPendingByAgent(_ context.Context, arg sqlcdb.ListSimWaiverClaimsPendingByAgentParams) ([]sqlcdb.SimWaiverClaim, error) {
	s.listPendingByAgentArgs = append(s.listPendingByAgentArgs, arg)
	if s.listPendingByAgentErr != nil {
		return nil, s.listPendingByAgentErr
	}
	return s.listPendingByAgentRows[arg.AgentID], nil
}

// TestLoadRosterAndPositionsRendersPlayerColumns pins the prompt's roster
// columns: the position enum, the raw team ID, and empty strings for NULL.
func TestLoadRosterAndPositionsRendersPlayerColumns(t *testing.T) {
	t.Parallel()
	q := newRosterContextQueriesFake()
	q.listFullRosterByAgent = map[int32][]sqlcdb.SimRoster{contextTestAgentID: {
		{PlayerID: 1, Slot: string(SlotC)},
		{PlayerID: 2, Slot: string(SlotBN)},
	}}
	q.getPlayerByID = map[int64]sqlcdb.Player{
		1: {FirstName: "Sidney", LastName: "Crosby",
			Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true},
			TeamID:   pgtype.Int8{Int64: 5, Valid: true}},
		2: {FirstName: "No", LastName: "Team", TeamID: pgtype.Int8{Int64: 9}},
	}
	acts := rosterContextActivity{q: q}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(func(ctx context.Context) ([]RosterRow, error) {
		_, _, rows, err := acts.loadRosterAndPositions(ctx, 1, contextTestAgentID, PoolConfig{})
		return rows, err
	}, activity.RegisterOptions{Name: "loadRosterAndPositions"})
	result, err := env.ExecuteActivity("loadRosterAndPositions")
	require.NoError(t, err)
	var rows []RosterRow
	require.NoError(t, result.Get(&rows))

	require.Len(t, rows, 2)
	assert.Equal(t, "C", rows[0].NHLPosition)
	assert.Equal(t, "5", rows[0].Team)
	assert.Equal(t, "", rows[1].NHLPosition)
	assert.Equal(t, "", rows[1].Team)
}

func TestLoadAgentPendingClaimsRendersResolveDate(t *testing.T) {
	t.Parallel()
	q := newRosterContextQueriesFake()
	q.listPendingByAgentRows = map[int32][]sqlcdb.SimWaiverClaim{contextTestAgentID: {
		{PlayerID: 1, ProcessDate: pgtype.Date{Time: time.Date(2024, 11, 3, 0, 0, 0, 0, time.UTC), Valid: true}},
		{PlayerID: 2},
	}}
	q.getPlayerByID = map[int64]sqlcdb.Player{
		1: {FirstName: "Sidney", LastName: "Crosby"},
		2: {FirstName: "No", LastName: "Date"},
	}
	acts := rosterContextActivity{q: q}

	ids, rows, err := acts.loadAgentPendingClaims(context.Background(), 1, contextTestAgentID)
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 2}, ids)
	require.Len(t, rows, 2)
	assert.Equal(t, "2024-11-03", rows[0].ResolvesOn)
	assert.Equal(t, "", rows[1].ResolvesOn)
}

// ============================================================================
// BuildManageRosterContext — standings rank inversion + DB-error propagation
// + FA position pre-population (Findings #9, #10).
// ============================================================================

type BuildContextTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env     *testsuite.TestActivityEnvironment
	queries *rosterContextQueriesFake
	acts    rosterContextActivity
}

func (s *BuildContextTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.queries = newRosterContextQueriesFake()
	s.acts = rosterContextActivity{q: s.queries}
	s.env.RegisterActivity(s.acts.BuildManageRosterContext)
}

func TestBuildContextTestSuite(t *testing.T) {
	suite.Run(t, new(BuildContextTestSuite))
}

// minimalContextInput is a BuildManageRosterContextInput with no FA/waiver
// players and no standings, suitable as a base for focused tests.
func (s *BuildContextTestSuite) minimalInput() BuildManageRosterContextInput {
	return BuildManageRosterContextInput{
		PoolID:  1,
		AgentID: 7,
		SimDate: pgDate(s.T(), "2025-01-10"),
		PoolConfig: PoolConfig{
			Season:     20242025,
			NumTeams:   6,
			WaiverDays: 2,
			RosterPositions: map[RosterSlot]int{
				SlotC: 2, SlotLW: 2, SlotRW: 2, SlotD: 3, SlotG: 2,
				SlotUtil: 1, SlotBN: 6, SlotIR: 3,
			},
		},
	}
}

// seedStandingRow builds a SimStanding for testing.
func seedStandingRow(agentID int32, cat string, value, rotoPts float64) (sqlcdb.SimStanding, error) {
	v, err := pgconv.NumericFromFloat(value)
	if err != nil {
		return sqlcdb.SimStanding{}, err
	}
	rp, err := pgconv.NumericFromFloat(rotoPts)
	if err != nil {
		return sqlcdb.SimStanding{}, err
	}
	return sqlcdb.SimStanding{AgentID: agentID, Category: cat, Value: v, RotoPoints: rp}, nil
}

// Finding #10 — standings rank must be 1 for the leader. In a 6-team pool
// the category leader earns 6 roto points (the max); rank must be 1, not 6.
func (s *BuildContextTestSuite) TestStandings_RankOneIsLeader() {
	t := s.T()
	// 6 agents, category G, roto points descending (agent 1 leads).
	s.queries.getSimStandingsLatestDateReturn = pgDate(t, "2025-01-10")
	var err error
	s.queries.listSimStandingsByDateRows, err = buildStandingRows(t,
		[]struct {
			agent int32
			pts   float64
		}{
			{1, 6}, {2, 5}, {3, 4}, {4, 3}, {5, 2}, {6, 1},
		},
	)
	require.NoError(t, err)
	s.queries.listAgentsByPoolReturn = []sqlcdb.SimAgent{
		{ID: 1, TeamName: "Sonnet"}, {ID: 2, TeamName: "Haiku"},
		{ID: 3, TeamName: "Opus"}, {ID: 4, TeamName: "Flash"},
		{ID: 5, TeamName: "Gemma"}, {ID: 6, TeamName: "Llama"},
	}

	in := s.minimalInput()
	in.AgentID = 1
	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	require.Len(t, got.DailyPrompt.Standings, 6, "all 6 agents must appear in standings")
	byAgent := make(map[int32]StandingRow, len(got.DailyPrompt.Standings))
	for _, row := range got.DailyPrompt.Standings {
		for _, a := range s.queries.listAgentsByPoolReturn {
			if displayName(a) == row.Agent {
				byAgent[a.ID] = row
			}
		}
	}

	assert.Equal(t, float64(1), byAgent[1].G.Rank,
		"category leader (6 roto pts) must have rank 1, not 6")
	assert.Equal(t, float64(6), byAgent[6].G.Rank,
		"last place (1 roto pt) must have rank 6")
}

// Finding #10 — tie: two agents with the same roto points get the same rank
// and both rank above the agent with fewer points.
func (s *BuildContextTestSuite) TestStandings_TiedRank() {
	t := s.T()
	// 3 agents. Agents 1 and 2 tied at 2.5 pts each (2-way tie at 2nd).
	// Agent 3 leads with 3 pts. Expected: agent 3 → rank 1; agents 1,2 → rank 2.
	s.queries.getSimStandingsLatestDateReturn = pgDate(t, "2025-01-10")
	var err error
	s.queries.listSimStandingsByDateRows, err = buildStandingRows(t,
		[]struct {
			agent int32
			pts   float64
		}{
			{3, 3}, {1, 2.5}, {2, 2.5},
		},
	)
	require.NoError(t, err)
	s.queries.listAgentsByPoolReturn = []sqlcdb.SimAgent{
		{ID: 1, TeamName: "A"}, {ID: 2, TeamName: "B"}, {ID: 3, TeamName: "C"},
	}

	in := s.minimalInput()
	in.PoolConfig.NumTeams = 3
	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	byAgent := make(map[int32]StandingRow, len(got.DailyPrompt.Standings))
	for _, row := range got.DailyPrompt.Standings {
		for _, a := range s.queries.listAgentsByPoolReturn {
			if displayName(a) == row.Agent {
				byAgent[a.ID] = row
			}
		}
	}
	assert.Equal(t, float64(1), byAgent[3].G.Rank, "leader must be rank 1")
	assert.Equal(t, float64(2), byAgent[1].G.Rank, "tied agents must share rank 2")
	assert.Equal(t, float64(2), byAgent[2].G.Rank, "tied agents must share rank 2")
}

// Finding #10 — Value is populated with the raw category stat total.
func (s *BuildContextTestSuite) TestStandings_ValueIsPopulated() {
	t := s.T()
	// Agent 1: 42 goals total (Value), earns 2 roto_points.
	// Agent 2: 10 goals total (Value), earns 1 roto_point.
	row1, err := seedStandingRow(1, string(CategoryG), 42, 2)
	require.NoError(t, err)
	row2, err := seedStandingRow(2, string(CategoryG), 10, 1)
	require.NoError(t, err)

	s.queries.getSimStandingsLatestDateReturn = pgDate(t, "2025-01-10")
	s.queries.listSimStandingsByDateRows = []sqlcdb.SimStanding{row1, row2}
	s.queries.listAgentsByPoolReturn = []sqlcdb.SimAgent{
		{ID: 1, TeamName: "Alpha"}, {ID: 2, TeamName: "Beta"},
	}

	in := s.minimalInput()
	in.PoolConfig.NumTeams = 2
	future, execErr := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, execErr)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	byAgent := make(map[int32]StandingRow, len(got.DailyPrompt.Standings))
	for _, row := range got.DailyPrompt.Standings {
		for _, a := range s.queries.listAgentsByPoolReturn {
			if displayName(a) == row.Agent {
				byAgent[a.ID] = row
			}
		}
	}

	// Value should carry the raw stat total (42 goals for agent 1).
	assert.Equal(t, float64(42), byAgent[1].G.Value,
		"Value must hold the raw category stat total, not roto_points")
	assert.Equal(t, float64(10), byAgent[2].G.Value)
}

// Finding #10 — a transient DB error from GetSimStandingsLatestDate must
// propagate as an activity error, not be silently swallowed as "no standings".
func (s *BuildContextTestSuite) TestStandings_DBErrorPropagates() {
	t := s.T()
	s.queries.getSimStandingsLatestDateErr = errors.New("connection reset by peer")

	_, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, s.minimalInput())
	require.Error(t, err, "transient DB error must surface, not be treated as empty standings")
	assert.Contains(t, err.Error(), "connection reset by peer")
}

// Finding #10 — pgx.ErrNoRows from GetSimStandingsLatestDate means "no
// standings yet" and must not surface as an error (empty slice returned).
func (s *BuildContextTestSuite) TestStandings_NoRowsIsEmpty() {
	t := s.T()
	s.queries.getSimStandingsLatestDateErr = pgx.ErrNoRows

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, s.minimalInput())
	require.NoError(t, err, "pgx.ErrNoRows means no standings yet — must not error")
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))
	assert.Empty(t, got.DailyPrompt.Standings, "no standings yet → empty slice")
}

// Finding #9 — BuildManageRosterContext pre-populates positions for FA
// players so they can be slotted in the same turn they are added.
func (s *BuildContextTestSuite) TestContext_FAPositionsPrePopulated() {
	t := s.T()
	const faPlayerID = int64(8480039)

	// Roster is empty; FA pool has one player (MacKinnon, C position).
	in := s.minimalInput()
	in.FreeAgents = []int64{faPlayerID}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		faPlayerID: {
			ID: faPlayerID, FirstName: "Nathan", LastName: "MacKinnon",
			Position: sqlcdb.NullPlayerPosition{
				PlayerPosition: sqlcdb.PlayerPositionC, Valid: true,
			},
		},
	}

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	pos, found := got.Positions[faPlayerID]
	require.True(t, found, "FA player's position must be in the returned Positions map")
	assert.Equal(t, sqlcdb.PlayerPositionC, pos,
		"pre-populated position must match the player's NHL position")
}

// Pending waiver claims must surface in the daily prompt with player names
// and resolution dates so the agent doesn't attempt a duplicate claim.
func (s *BuildContextTestSuite) TestContext_PendingClaimsInDailyPrompt() {
	t := s.T()
	in := s.minimalInput()
	s.queries.listPendingByAgentRows = map[int32][]sqlcdb.SimWaiverClaim{
		in.AgentID: {{
			ID: 42, PoolID: in.PoolID, AgentID: in.AgentID, PlayerID: 100,
			DropPlayerID: pgtype.Int8{Int64: 200, Valid: true},
			ProcessDate:  pgDate(t, "2025-01-12"),
			Status:       string(WaiverClaimStatusPending),
		}},
	}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		100: {ID: 100, FirstName: "Claimed", LastName: "Winger"},
		200: {ID: 200, FirstName: "Dropped", LastName: "Goalie"},
	}

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	require.Len(t, got.DailyPrompt.PendingClaims, 1)
	claim := got.DailyPrompt.PendingClaims[0]
	assert.Equal(t, "Claimed Winger", claim.Player)
	assert.Equal(t, int64(100), claim.ID)
	assert.Equal(t, "2025-01-12", claim.ResolvesOn)
	assert.Equal(t, "Dropped Goalie", claim.DropPlayer)
	assert.Equal(t, []int64{100}, got.PendingClaims,
		"working-state pending set must stay populated alongside the prompt rows")
}

// buildStandingRows is a test helper that converts a (agent, rotoPoints) list
// into SimStanding rows for the G category, using value=0 as a placeholder
// (value is not exercised by rank tests).
func buildStandingRows(t require.TestingT, entries []struct {
	agent int32
	pts   float64
}) ([]sqlcdb.SimStanding, error) {
	rows := make([]sqlcdb.SimStanding, 0, len(entries))
	for _, e := range entries {
		r, err := seedStandingRow(e.agent, string(CategoryG), 0, e.pts)
		if err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// ----------------------------------------------------------------------------
// BuildManageRosterContextActivity
// ----------------------------------------------------------------------------

// TestBuildManageRosterContext_BatchesRosterPlayerLookups guards the
// T3-B N+1 fix in loadRosterAndPositions: every roster player's name
// + position is resolved with one GetPlayersByIDs call, not one
// GetPlayer per roster row. FreeAgents/OnWaivers are left empty so
// the only batch call in this invocation is the roster one.
func (s *BuildContextTestSuite) TestBuildManageRosterContext_BatchesRosterPlayerLookups() {
	t := s.T()
	s.queries.getSimAgentByID = map[int32]sqlcdb.SimAgent{1: {ID: 1}}
	s.queries.listFullRosterByAgent = map[int32][]sqlcdb.SimRoster{
		1: {
			{PoolID: 1, AgentID: 1, PlayerID: 100, Slot: "C"},
			{PoolID: 1, AgentID: 1, PlayerID: 200, Slot: "LW"},
			{PoolID: 1, AgentID: 1, PlayerID: 300, Slot: "BN"},
		},
	}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		100: {FirstName: "A", LastName: "One", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}},
		200: {FirstName: "B", LastName: "Two", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionLW, Valid: true}},
		300: {FirstName: "C", LastName: "Three", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionD, Valid: true}},
	}

	in := BuildManageRosterContextInput{
		PoolID: 1, AgentID: 1,
		SimDate:    pgDate(t, "2024-11-15"),
		PoolConfig: PoolConfig{RosterPositions: map[RosterSlot]int{SlotC: 1, SlotLW: 1, SlotBN: 1}},
	}
	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	require.Len(t, s.queries.getPlayersByIDsCalls, 1, "one batched call for the whole roster, not one per player")
	assert.ElementsMatch(t, []int64{100, 200, 300}, s.queries.getPlayersByIDsCalls[0])
	require.Len(t, got.DailyPrompt.YourRoster, 3)
	assert.Equal(t, sqlcdb.PlayerPositionD, got.Positions[300])
}

func (s *BuildContextTestSuite) TestBuildManageRosterContext_PopulatesNotesAndRoster() {
	t := s.T()
	s.queries.getSimAgentByID = map[int32]sqlcdb.SimAgent{
		1: {ID: 1, Notes: "Watch the schedule"},
	}
	s.queries.listFullRosterByAgent = map[int32][]sqlcdb.SimRoster{
		1: {
			{PoolID: 1, AgentID: 1, PlayerID: 100, Slot: "C"},
			{PoolID: 1, AgentID: 1, PlayerID: 200, Slot: "BN"},
		},
	}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		100: {ID: 100, FirstName: "Connor", LastName: "McDavid", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}},
		200: {ID: 200, FirstName: "Leon", LastName: "Draisaitl", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}},
	}

	in := BuildManageRosterContextInput{
		PoolID: 1, AgentID: 1,
		SimDate:    pgDate(t, "2024-11-15"),
		PoolConfig: PoolConfig{RosterPositions: map[RosterSlot]int{SlotC: 2, SlotBN: 5}},
		FreeAgents: []int64{300, 400},
	}
	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	assert.Equal(t, "Watch the schedule", got.DailyPrompt.YourNotes)
	require.Len(t, got.DailyPrompt.YourRoster, 2)
	assert.Equal(t, "Connor McDavid", got.DailyPrompt.YourRoster[0].Player)
	assert.Equal(t, RosterSlot("C"), got.DailyPrompt.YourRoster[0].Slot)
	assert.Equal(t, RosterSlot("C"), got.Roster.Placements[100])
	assert.Equal(t, RosterSlot("BN"), got.Roster.Placements[200])
	assert.Equal(t, sqlcdb.PlayerPositionC, got.Positions[100])
	assert.Equal(t, []int64{300, 400}, got.FreeAgents)
}

// PendingClaims for the agent flow into the context so the daily turn's
// ValidateClaimPlayer can reject a duplicate pending claim cleanly.
func (s *BuildContextTestSuite) TestBuildManageRosterContext_PopulatesPendingClaims() {
	t := s.T()
	s.queries.getSimAgentByID = map[int32]sqlcdb.SimAgent{1: {ID: 1}}
	s.queries.listFullRosterByAgent = map[int32][]sqlcdb.SimRoster{1: {}}
	s.queries.listPendingByAgentRows = map[int32][]sqlcdb.SimWaiverClaim{
		1: {claim(50, 1, 8478550, 0), claim(51, 1, 8480039, 0)},
	}

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, BuildManageRosterContextInput{
		PoolID: 1, AgentID: 1,
		SimDate: pgDate(t, "2024-11-15"),
	})
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	assert.ElementsMatch(t, []int64{8478550, 8480039}, got.PendingClaims)
	require.Len(t, s.queries.listPendingByAgentArgs, 1)
	assert.Equal(t, int32(1), s.queries.listPendingByAgentArgs[0].AgentID)
}

func (s *BuildContextTestSuite) TestBuildManageRosterContext_EmptyStandingsBeforeFirstGame() {
	t := s.T()
	// No latest standings date yet — pre-first-game state.
	s.queries.getSimStandingsLatestDateReturn = pgtype.Date{Valid: false}
	s.queries.listFullRosterByAgent = map[int32][]sqlcdb.SimRoster{1: {}}
	s.queries.getSimAgentByID = map[int32]sqlcdb.SimAgent{1: {ID: 1}}

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, BuildManageRosterContextInput{
		PoolID: 1, AgentID: 1,
		SimDate: pgDate(t, "2024-10-08"),
	})
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))
	assert.Empty(t, got.DailyPrompt.Standings, "no standings yet → empty slice, not error")
}

func (s *BuildContextTestSuite) TestBuildManageRosterContext_StandingsTagIsYou() {
	t := s.T()
	s.queries.getSimAgentByID = map[int32]sqlcdb.SimAgent{1: {ID: 1, TeamName: "Alpha"}, 2: {ID: 2, TeamName: "Bravo"}}
	s.queries.listAgentsByPoolReturn = []sqlcdb.SimAgent{
		{ID: 1, TeamName: "Alpha"}, {ID: 2, TeamName: "Bravo"},
	}
	s.queries.getSimStandingsLatestDateReturn = pgtype.Date{Valid: true}
	s.queries.listSimStandingsByDateRows = []sqlcdb.SimStanding{
		{AgentID: 1, Category: "G", RotoPoints: numericFromIntForTest(t, 5)},
		{AgentID: 2, Category: "G", RotoPoints: numericFromIntForTest(t, 3)},
	}
	s.queries.listFullRosterByAgent = map[int32][]sqlcdb.SimRoster{1: {}, 2: {}}

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, BuildManageRosterContextInput{
		PoolID: 1, AgentID: 1,
	})
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	require.Len(t, got.DailyPrompt.Standings, 2)
	byAgent := map[string]bool{}
	for _, r := range got.DailyPrompt.Standings {
		byAgent[r.Agent] = r.IsYou
	}
	assert.True(t, byAgent["Alpha"], "Alpha is the requested agent → is_you=true")
	assert.False(t, byAgent["Bravo"], "Bravo is not requested → is_you=false")
}

// numericFromIntForTest is a tiny helper for test fixtures —
// converts an int to pgtype.Numeric without dragging strconv into
// the test imports section.
func numericFromIntForTest(t require.TestingT, v int) pgtype.Numeric {
	n, err := pgconv.NumericFromFloat(float64(v))
	require.NoError(t, err)
	return n
}
