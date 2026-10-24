package simulation

import (
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Finding SIM-U3 — the daily prompt must surface ranked free agents with
// the exact ids add_player validates against, so the agent is shown the ids
// it may act on rather than having to invent one.
func (s *BuildContextTestSuite) TestContext_TopFreeAgentsRankedCarryIDs() {
	t := s.T()
	skaterIDs := []int64{100, 200, 300}
	goalieIDs := []int64{400}
	in := s.minimalInput()
	in.FreeAgents = append(append([]int64(nil), skaterIDs...), goalieIDs...)

	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		100: {FirstName: "Hot", LastName: "Skaters", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionLW, Valid: true}},
		200: {FirstName: "Cold", LastName: "Skater", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionD, Valid: true}},
		300: {FirstName: "Warm", LastName: "Skater", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}},
		400: {FirstName: "Standout", LastName: "Goalie", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionG, Valid: true}},
	}
	// Recent G+A+PPP composites: 100 → 6, 200 → 1, 300 → 6 (tie broken by id).
	s.queries.listFreeAgentSkaterStatsReturn = []sqlcdb.ListSimFreeAgentSkaterStatsRow{
		{PlayerID: 200, RecentGoals: 1, RecentAssists: 0, RecentPpp: 0},
		{PlayerID: 100, RecentGoals: 3, RecentAssists: 2, RecentPpp: 1},
		{PlayerID: 300, RecentGoals: 2, RecentAssists: 2, RecentPpp: 2},
	}
	s.queries.listFreeAgentGoalieStatsReturn = []sqlcdb.ListSimFreeAgentGoalieStatsRow{
		{PlayerID: 400, RecentWins: 2, RecentGa: 5, RecentToi: 3600},
	}

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	// Ranking: skaters descending by composite, ties by id ascending.
	require.Len(t, got.DailyPrompt.TopFreeAgents.Skaters, 3)
	assert.Equal(t, []int64{100, 300, 200}, []int64{
		got.DailyPrompt.TopFreeAgents.Skaters[0].ID,
		got.DailyPrompt.TopFreeAgents.Skaters[1].ID,
		got.DailyPrompt.TopFreeAgents.Skaters[2].ID,
	})
	assert.Equal(t, 6, got.DailyPrompt.TopFreeAgents.Skaters[0].RecentScore)
	assert.Equal(t, "Hot Skaters", got.DailyPrompt.TopFreeAgents.Skaters[0].Player)
	assert.Equal(t, "LW", got.DailyPrompt.TopFreeAgents.Skaters[0].NHLPosition)

	require.Len(t, got.DailyPrompt.TopFreeAgents.Goalies, 1)
	assert.Equal(t, int64(400), got.DailyPrompt.TopFreeAgents.Goalies[0].ID)
	assert.Equal(t, 2, got.DailyPrompt.TopFreeAgents.Goalies[0].RecentScore)
	assert.InDelta(t, 5.0, got.DailyPrompt.TopFreeAgents.Goalies[0].Last7.GAA, 0.001)

	// The stats queries receive exactly the partitioned skater/goalie ids.
	require.Len(t, s.queries.listFreeAgentSkaterStatsArgs, 1)
	assert.ElementsMatch(t, skaterIDs, s.queries.listFreeAgentSkaterStatsArgs[0].PlayerIds)
	require.Len(t, s.queries.listFreeAgentGoalieStatsArgs, 1)
	assert.ElementsMatch(t, goalieIDs, s.queries.listFreeAgentGoalieStatsArgs[0].PlayerIds)
}

// Finding SIM-U3 — the daily prompt must list the players currently on
// waivers with the ids claim_player validates against and their clears_on
// date, so the agent can file a claim against a real id.
func (s *BuildContextTestSuite) TestContext_OnWaiversInDailyPrompt() {
	t := s.T()
	in := s.minimalInput()
	s.queries.listPlayersOnWaiversReturn = []sqlcdb.ListSimPlayersOnWaiversRow{
		{PlayerID: pgtype.Int8{Int64: 500, Valid: true}, DroppedOn: pgDate(t, "2025-01-09"), ClearsOn: pgDate(t, "2025-01-11")},
	}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		500: {FirstName: "Dropped", LastName: "Winger", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionRW, Valid: true}},
	}

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	require.Len(t, got.DailyPrompt.OnWaivers, 1)
	row := got.DailyPrompt.OnWaivers[0]
	assert.Equal(t, int64(500), row.ID)
	assert.Equal(t, "Dropped Winger", row.Player)
	assert.Equal(t, "RW", row.NHLPosition)
	assert.Equal(t, "2025-01-11", row.ClearsOn)
	// Working state carries the same id so ValidateClaimPlayer accepts it.
	assert.Equal(t, []int64{500}, got.OnWaivers)
}

// Finding SIM-U3 — free agents and waiver candidates are disjoint, and the
// prompt routes each to the block matching the tool that acts on it:
// add_player reads top_free_agents, claim_player reads on_waivers.
func (s *BuildContextTestSuite) TestContext_FreeAgentsDistinctFromWaivers() {
	t := s.T()
	in := s.minimalInput()
	in.FreeAgents = []int64{100}
	s.queries.listPlayersOnWaiversReturn = []sqlcdb.ListSimPlayersOnWaiversRow{
		{PlayerID: pgtype.Int8{Int64: 500, Valid: true}, ClearsOn: pgDate(t, "2025-01-11")},
	}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		100: {FirstName: "Free", LastName: "Agent", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}},
		500: {FirstName: "Waiver", LastName: "Player", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionRW, Valid: true}},
	}
	s.queries.listFreeAgentSkaterStatsReturn = []sqlcdb.ListSimFreeAgentSkaterStatsRow{
		{PlayerID: 100, RecentGoals: 2},
	}

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	// Free agent appears only in top_free_agents, not on_waivers.
	require.Len(t, got.DailyPrompt.TopFreeAgents.Skaters, 1)
	assert.Equal(t, int64(100), got.DailyPrompt.TopFreeAgents.Skaters[0].ID)
	require.Len(t, got.DailyPrompt.OnWaivers, 1)
	assert.NotEqual(t, int64(100), got.DailyPrompt.OnWaivers[0].ID)

	// Waiver player appears only in on_waivers, not top_free_agents.
	assert.Equal(t, int64(500), got.DailyPrompt.OnWaivers[0].ID)
	for _, sk := range got.DailyPrompt.TopFreeAgents.Skaters {
		assert.NotEqual(t, int64(500), sk.ID, "waiver player must not appear as a free agent")
	}
	assert.Equal(t, []int64{100}, got.FreeAgents)
	assert.Equal(t, []int64{500}, got.OnWaivers)
}

// Finding SIM-U3 — F / NULL-position free agents are not slottable, so they
// must be dropped from top_free_agents rather than presented as pickups.
func (s *BuildContextTestSuite) TestContext_TopFreeAgentsSkipsUnslottablePositions() {
	t := s.T()
	in := s.minimalInput()
	in.FreeAgents = []int64{100, 200, 300}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		100: {FirstName: "Good", LastName: "Center", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}},
		200: {FirstName: "Forward", LastName: "Unslottable", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionF, Valid: true}},
		300: {FirstName: "Null", LastName: "Position"},
	}
	s.queries.listFreeAgentSkaterStatsReturn = []sqlcdb.ListSimFreeAgentSkaterStatsRow{
		{PlayerID: 100, RecentGoals: 1},
	}

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	require.Len(t, got.DailyPrompt.TopFreeAgents.Skaters, 1)
	assert.Equal(t, int64(100), got.DailyPrompt.TopFreeAgents.Skaters[0].ID)
}

// Finding SIM-U3 follow-up — a free agent with zero regular-season games
// before sim_date has no stats row, and by PLAN.md "Free-agent ranking"
// (top N by recent composite) is intentionally excluded from top_free_agents
// rather than ranked last with zero stats. This test pins that exclusion so
// a future "show the pool's depth" change to the loaders is a conscious
// decision, not an accident.
func (s *BuildContextTestSuite) TestContext_TopFreeAgentsExcludesZeroGamePlayers() {
	t := s.T()
	in := s.minimalInput()
	in.FreeAgents = []int64{100, 200, 300}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		100: {FirstName: "Played", LastName: "Skater", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}},
		200: {FirstName: "Zero", LastName: "Skater", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionD, Valid: true}},
		300: {FirstName: "Zero", LastName: "Goalie", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionG, Valid: true}},
	}
	// Only the played skater has a stats row; the zero-game skater (200)
	// and goalie (300) are absent from their stats queries entirely.
	s.queries.listFreeAgentSkaterStatsReturn = []sqlcdb.ListSimFreeAgentSkaterStatsRow{
		{PlayerID: 100, RecentGoals: 1},
	}

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	require.Len(t, got.DailyPrompt.TopFreeAgents.Skaters, 1)
	assert.Equal(t, int64(100), got.DailyPrompt.TopFreeAgents.Skaters[0].ID)
	assert.Empty(t, got.DailyPrompt.TopFreeAgents.Goalies)
}

// Finding SIM-U3 follow-up — BuildFreeAgentPool snapshots the pool once a
// day before any agent's turn, so a later agent may be handed an id an
// earlier agent already rostered. BuildManageRosterContext must drop such
// players from both the ranking input and the working-state FreeAgents, so
// a same-day add is a clean in-turn validation error rather than a silent
// commit_rejected conflict the agent never sees.
func (s *BuildContextTestSuite) TestContext_FreeAgentsDropPlayersRosteredAfterPool() {
	t := s.T()
	in := s.minimalInput()
	in.FreeAgents = []int64{100, 200}
	// Player 100 was added by another agent after the pool snapshot.
	s.queries.listRosterByPoolReturn = []sqlcdb.SimRoster{
		{PoolID: 1, AgentID: 2, PlayerID: 100, Slot: "LW"},
	}
	s.queries.getPlayerByID = map[int64]sqlcdb.Player{
		100: {ID: 100, FirstName: "Taken", LastName: "Skater", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionLW, Valid: true}},
		200: {ID: 200, FirstName: "Still", LastName: "Free", Position: sqlcdb.NullPlayerPosition{PlayerPosition: sqlcdb.PlayerPositionC, Valid: true}},
	}
	s.queries.listFreeAgentSkaterStatsReturn = []sqlcdb.ListSimFreeAgentSkaterStatsRow{
		{PlayerID: 100, RecentGoals: 5},
		{PlayerID: 200, RecentGoals: 2},
	}

	future, err := s.env.ExecuteActivity(s.acts.BuildManageRosterContext, in)
	require.NoError(t, err)
	var got ManageRosterInput
	require.NoError(t, future.Get(&got))

	// Working state drops the rostered player so ValidateAddPlayer rejects it.
	assert.Equal(t, []int64{200}, got.FreeAgents)
	// The ranking input was filtered too: the stats query only saw 200.
	require.Len(t, s.queries.listFreeAgentSkaterStatsArgs, 1)
	assert.ElementsMatch(t, []int64{200}, s.queries.listFreeAgentSkaterStatsArgs[0].PlayerIds)
	require.Len(t, got.DailyPrompt.TopFreeAgents.Skaters, 1)
	assert.Equal(t, int64(200), got.DailyPrompt.TopFreeAgents.Skaters[0].ID)
	// The pool-wide roster probe ran against this pool.
	require.Len(t, s.queries.listRosterByPoolArgs, 1)
	assert.Equal(t, int32(1), s.queries.listRosterByPoolArgs[0])
}
