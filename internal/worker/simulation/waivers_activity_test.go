package simulation

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

// ============================================================================
// ProcessWaiversTestSuite
// ============================================================================

type ProcessWaiversTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env     *testsuite.TestActivityEnvironment
	queries *stubSimQueries
	tx      *stubTransactor
	acts    *Activities
}

func (s *ProcessWaiversTestSuite) SetupTest() {
	s.env = s.NewTestActivityEnvironment()
	s.queries = &stubSimQueries{}
	s.tx = &stubTransactor{queries: s.queries}
	s.acts = &Activities{Queries: s.queries, Tx: s.tx}
	s.env.RegisterActivity(s.acts.ProcessWaivers)
}

func TestProcessWaiversTestSuite(t *testing.T) {
	suite.Run(t, new(ProcessWaiversTestSuite))
}

// testWaiverRosterCapacity is a generous default so the existing
// resolution tests (which don't populate listFullRosterByAgent, so the
// winner's roster reads as empty) clear the capacity recheck. Tests
// that exercise the over-capacity path set their own roster rows + a
// tight capacity.
const testWaiverRosterCapacity = 16

func (s *ProcessWaiversTestSuite) input() ProcessWaiversInput {
	return ProcessWaiversInput{
		PoolID:         1,
		SimDate:        pgDate(s.T(), "2024-11-15"),
		RosterCapacity: testWaiverRosterCapacity,
	}
}

// claim builds a SimWaiverClaim with the common fields populated.
// dropID == 0 means "no drop" (NULL).
func claim(id, agentID int32, playerID int64, dropID int64) sqlcdb.SimWaiverClaim {
	c := sqlcdb.SimWaiverClaim{
		ID:       id,
		PoolID:   1,
		AgentID:  agentID,
		PlayerID: playerID,
		Status:   string(WaiverClaimStatusPending),
	}
	if dropID != 0 {
		c.DropPlayerID = pgtype.Int8{Int64: dropID, Valid: true}
	}
	return c
}

// priority builds a SimWaiverPriority.
func priority(agentID, p int32) sqlcdb.SimWaiverPriority {
	return sqlcdb.SimWaiverPriority{PoolID: 1, AgentID: agentID, Priority: p}
}

// setPriorities makes the stub pool hold exactly the agents of rows, ranked
// as given, so the resolution's completeness check passes.
func (s *ProcessWaiversTestSuite) setPriorities(rows ...sqlcdb.SimWaiverPriority) {
	s.queries.listPriorityRows = rows
	s.queries.listAgentsByPoolReturn = make([]sqlcdb.SimAgent, len(rows))
	for i, p := range rows {
		s.queries.listAgentsByPoolReturn[i] = sqlcdb.SimAgent{ID: p.AgentID, PoolID: p.PoolID}
	}
}

// ----------------------------------------------------------------------------
// No claims due → skip
// ----------------------------------------------------------------------------

func (s *ProcessWaiversTestSuite) TestNoDueClaims_Skips() {
	t := s.T()
	s.setPriorities(priority(1, 1), priority(2, 2))
	future, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, s.input())
	require.NoError(t, err)
	var got ProcessWaiversResult
	require.NoError(t, future.Get(&got))
	assert.True(t, got.Skipped)
	assert.Equal(t, []int32{1}, s.queries.lockPoolCalls, "the pool is locked even with nothing to resolve")
	assert.Equal(t, []int32{1}, s.queries.initPriorityCalls,
		"the first season day initializes priority whether or not a claim is due")
	assert.Empty(t, s.queries.resolveClaimCalls)
	assert.Empty(t, s.queries.updatePriorityCalls, "priority unchanged when nothing resolves")
}

// ----------------------------------------------------------------------------
// Uncontested claim — single claimant wins automatically.
// ----------------------------------------------------------------------------

func (s *ProcessWaiversTestSuite) TestUncontestedClaim_Wins() {
	t := s.T()
	in := s.input()
	s.queries.listClaimsDueRows = []sqlcdb.SimWaiverClaim{
		claim(100, 1, 8478402, 0),
	}
	s.setPriorities(
		priority(1, 1), priority(2, 2),
	)

	future, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, in)
	require.NoError(t, err)
	var got ProcessWaiversResult
	require.NoError(t, future.Get(&got))
	assert.False(t, got.Skipped)
	assert.Equal(t, 1, got.ClaimsWon)
	assert.Equal(t, 0, got.ClaimsLost)
	assert.Equal(t, 0, got.ContestedGroups, "single-claimant groups are uncontested")

	// Status update for the won claim.
	require.Len(t, s.queries.resolveClaimCalls, 1)
	assert.Equal(t, int32(100), s.queries.resolveClaimCalls[0].ID)
	assert.Equal(t, string(WaiverClaimStatusWon), s.queries.resolveClaimCalls[0].Status)

	// Roster: just an add (no drop).
	require.Len(t, s.queries.insertRosterCalls, 1)
	assert.Equal(t, int64(8478402), s.queries.insertRosterCalls[0].PlayerID)
	assert.Equal(t, string(SlotBN), s.queries.insertRosterCalls[0].Slot)
	assert.Equal(t, string(AcquiredViaFreeAgent), s.queries.insertRosterCalls[0].AcquiredVia)
	assert.Empty(t, s.queries.deleteRosterCalls)

	// Tx rows: an `add`, no `drop`.
	require.Len(t, s.queries.insertAddCalls, 1)
	assert.Empty(t, s.queries.insertDropCalls)
}

// ----------------------------------------------------------------------------
// Contested claim — highest priority (lowest priority number) wins.
// ----------------------------------------------------------------------------

func (s *ProcessWaiversTestSuite) TestContestedClaim_HighestPriorityWins() {
	t := s.T()
	in := s.input()
	// Three agents claim same player. Agent 2 has priority 1 (highest);
	// agents 1 and 3 have priority 2 and 3 respectively.
	s.queries.listClaimsDueRows = []sqlcdb.SimWaiverClaim{
		claim(100, 1, 8478402, 0),
		claim(101, 2, 8478402, 0),
		claim(102, 3, 8478402, 0),
	}
	s.setPriorities(
		priority(2, 1), priority(1, 2), priority(3, 3),
	)

	future, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, in)
	require.NoError(t, err)
	var got ProcessWaiversResult
	require.NoError(t, future.Get(&got))
	assert.Equal(t, 1, got.ClaimsWon)
	assert.Equal(t, 2, got.ClaimsLost)
	assert.Equal(t, 1, got.ContestedGroups)

	// Agent 2 wins — only their roster mutation.
	require.Len(t, s.queries.insertRosterCalls, 1)
	assert.Equal(t, int32(2), s.queries.insertRosterCalls[0].AgentID)

	// Three status updates: 1 won, 2 lost.
	require.Len(t, s.queries.resolveClaimCalls, 3)
	statusByClaimID := map[int32]string{}
	for _, c := range s.queries.resolveClaimCalls {
		statusByClaimID[c.ID] = c.Status
	}
	assert.Equal(t, string(WaiverClaimStatusWon), statusByClaimID[101])
	assert.Equal(t, string(WaiverClaimStatusLost), statusByClaimID[100])
	assert.Equal(t, string(WaiverClaimStatusLost), statusByClaimID[102])
}

// ----------------------------------------------------------------------------
// Winner-with-drop: drop_player_id removed from roster, drop tx logged.
// ----------------------------------------------------------------------------

func (s *ProcessWaiversTestSuite) TestWinnerWithDrop_AppliesDrop() {
	t := s.T()
	in := s.input()
	s.queries.listClaimsDueRows = []sqlcdb.SimWaiverClaim{
		claim(100, 1, 8478402, 8499999), // drop player 8499999
	}
	s.setPriorities(priority(1, 1))
	// Drop player is still on the roster → the plan sees it and the
	// stub's DeleteSimRosterRows reports 1.
	s.queries.listFullRosterByAgent = map[int32][]sqlcdb.SimRoster{
		1: {rosterRow(1, 8499999)},
	}

	_, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, in)
	require.NoError(t, err)

	// Roster: one add + one delete (rows-affected variant).
	require.Len(t, s.queries.insertRosterCalls, 1)
	assert.Equal(t, int64(8478402), s.queries.insertRosterCalls[0].PlayerID)
	require.Len(t, s.queries.deleteRosterRowsCalls, 1)
	assert.Equal(t, int64(8499999), s.queries.deleteRosterRowsCalls[0].PlayerID)

	// Tx rows: drop AND add. The add row references the dropped player
	// via drop_player_id, mirroring the ManageRoster add+drop path.
	require.Len(t, s.queries.insertDropCalls, 1)
	require.Len(t, s.queries.insertAddCalls, 1)
	assert.True(t, s.queries.insertAddCalls[0].DropPlayerID.Valid)
	assert.Equal(t, int64(8499999), s.queries.insertAddCalls[0].DropPlayerID.Int64)
}

// ----------------------------------------------------------------------------
// Priority demotion: winner moves to the bottom; non-winners
// retain relative order.
// ----------------------------------------------------------------------------

func (s *ProcessWaiversTestSuite) TestPriorityDemotion_WinnerToBottom() {
	t := s.T()
	in := s.input()
	// Agent 1 (priority 1) wins. Expect new order: 2, 3, 1.
	s.queries.listClaimsDueRows = []sqlcdb.SimWaiverClaim{
		claim(100, 1, 8478402, 0),
	}
	s.setPriorities(
		priority(1, 1), priority(2, 2), priority(3, 3),
	)

	_, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, in)
	require.NoError(t, err)

	// 3 priority updates emitted (full restamp, even no-op rows).
	require.Len(t, s.queries.updatePriorityCalls, 3)
	priorityByAgent := map[int32]int32{}
	for _, c := range s.queries.updatePriorityCalls {
		priorityByAgent[c.AgentID] = c.Priority
	}
	assert.Equal(t, int32(1), priorityByAgent[2], "non-winner agent 2 takes priority 1")
	assert.Equal(t, int32(2), priorityByAgent[3], "non-winner agent 3 takes priority 2")
	assert.Equal(t, int32(3), priorityByAgent[1], "winner agent 1 demoted to priority 3 (bottom)")
}

// Multiple winners on the same day demote together; their relative
// pre-resolution priority order is preserved within the demoted block.
func (s *ProcessWaiversTestSuite) TestPriorityDemotion_MultipleWinners() {
	t := s.T()
	in := s.input()
	// Agents 1 and 2 each win a separate claim. Pre-resolution priority
	// order: 1, 2, 3, 4. Expected new order: 3, 4, 1, 2 (winners go
	// to tail, but agent 1 stays ahead of agent 2 because they had
	// higher priority pre-resolution).
	s.queries.listClaimsDueRows = []sqlcdb.SimWaiverClaim{
		claim(100, 1, 8478402, 0),
		claim(101, 2, 8480039, 0),
	}
	s.setPriorities(
		priority(1, 1), priority(2, 2), priority(3, 3), priority(4, 4),
	)

	_, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, in)
	require.NoError(t, err)

	priorityByAgent := map[int32]int32{}
	for _, c := range s.queries.updatePriorityCalls {
		priorityByAgent[c.AgentID] = c.Priority
	}
	assert.Equal(t, int32(1), priorityByAgent[3])
	assert.Equal(t, int32(2), priorityByAgent[4])
	assert.Equal(t, int32(3), priorityByAgent[1], "winner agent 1 (originally higher) lands ahead of agent 2 in demoted block")
	assert.Equal(t, int32(4), priorityByAgent[2])
}

// ----------------------------------------------------------------------------
// Multiple distinct players claimed on same day — each resolves
// independently, no cross-talk.
// ----------------------------------------------------------------------------

func (s *ProcessWaiversTestSuite) TestMultiplePlayers_IndependentResolution() {
	t := s.T()
	in := s.input()
	s.queries.listClaimsDueRows = []sqlcdb.SimWaiverClaim{
		claim(100, 1, 8478402, 0),
		claim(101, 2, 8480039, 0),
		claim(102, 3, 8479318, 0),
	}
	s.setPriorities(
		priority(1, 1), priority(2, 2), priority(3, 3),
	)

	_, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, in)
	require.NoError(t, err)
	require.Len(t, s.queries.insertRosterCalls, 3, "each player goes to their respective claimant")
	assert.Len(t, s.queries.resolveClaimCalls, 3, "all three claims marked won")
	for _, c := range s.queries.resolveClaimCalls {
		assert.Equal(t, string(WaiverClaimStatusWon), c.Status)
	}
}

// ----------------------------------------------------------------------------
// resolveClaims unit test (pure function — no DB)
// ----------------------------------------------------------------------------

func TestResolveClaims_DeterministicOrder(t *testing.T) {
	claims := []sqlcdb.SimWaiverClaim{
		// Out of priority order; resolveClaims should sort.
		{ID: 3, AgentID: 3, PlayerID: 100},
		{ID: 1, AgentID: 1, PlayerID: 100},
		{ID: 2, AgentID: 2, PlayerID: 100},
	}
	priorities := []sqlcdb.SimWaiverPriority{
		{AgentID: 1, Priority: 3},
		{AgentID: 2, Priority: 1}, // wins
		{AgentID: 3, Priority: 2},
	}

	resolutions := resolveClaims(claims, priorities)
	require.Len(t, resolutions, 1)
	assert.Equal(t, int32(2), resolutions[0].winner.AgentID, "agent 2 has priority 1 = wins")
	assert.Len(t, resolutions[0].losers, 2)
}

// resolveClaims with multiple players returns deterministic
// player_id-ascending order so tests don't have to sort.
func TestResolveClaims_PlayerOrderingDeterministic(t *testing.T) {
	claims := []sqlcdb.SimWaiverClaim{
		{ID: 1, AgentID: 1, PlayerID: 200},
		{ID: 2, AgentID: 1, PlayerID: 100},
		{ID: 3, AgentID: 1, PlayerID: 300},
	}
	priorities := []sqlcdb.SimWaiverPriority{{AgentID: 1, Priority: 1}}

	resolutions := resolveClaims(claims, priorities)
	require.Len(t, resolutions, 3)
	assert.Equal(t, int64(100), resolutions[0].playerID)
	assert.Equal(t, int64(200), resolutions[1].playerID)
	assert.Equal(t, int64(300), resolutions[2].playerID)
}

// demoteWinners unit test
func TestDemoteWinners_PreservesNonWinnerOrder(t *testing.T) {
	priorities := []sqlcdb.SimWaiverPriority{
		{AgentID: 1, Priority: 1},
		{AgentID: 2, Priority: 2},
		{AgentID: 3, Priority: 3},
		{AgentID: 4, Priority: 4},
	}
	out := demoteWinners(priorities, []int32{2, 3})
	require.Len(t, out, 4)

	byAgent := map[int32]int32{}
	for _, p := range out {
		byAgent[p.AgentID] = p.Priority
	}
	assert.Equal(t, int32(1), byAgent[1])
	assert.Equal(t, int32(2), byAgent[4])
	assert.Equal(t, int32(3), byAgent[2], "winner 2 demoted (was ahead of 3 originally → stays ahead in demoted block)")
	assert.Equal(t, int32(4), byAgent[3])
}

func TestDemoteWinners_DedupsAgentWinningMultipleClaims(t *testing.T) {
	priorities := []sqlcdb.SimWaiverPriority{
		{AgentID: 1, Priority: 1},
		{AgentID: 2, Priority: 2},
	}
	// Agent 1 won two claims today. They should appear ONCE at the bottom.
	out := demoteWinners(priorities, []int32{1, 1})
	require.Len(t, out, 2)
	byAgent := map[int32]int32{}
	for _, p := range out {
		byAgent[p.AgentID] = p.Priority
	}
	assert.Equal(t, int32(1), byAgent[2])
	assert.Equal(t, int32(2), byAgent[1])
}

// ----------------------------------------------------------------------------
// Error propagation
// ----------------------------------------------------------------------------

func (s *ProcessWaiversTestSuite) TestListClaimsError_Aborts() {
	t := s.T()
	s.queries.listClaimsForDuePlayersErr = errors.New("connection lost")
	_, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, s.input())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list waiver claims for due players")
}

func (s *ProcessWaiversTestSuite) TestListPriorityError_Aborts() {
	t := s.T()
	s.queries.listClaimsDueRows = []sqlcdb.SimWaiverClaim{claim(1, 1, 100, 0)}
	s.queries.listPriorityErr = errors.New("statement timeout")

	_, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, s.input())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list waiver priority")
}

func (s *ProcessWaiversTestSuite) TestCommitError_Aborts() {
	t := s.T()
	s.queries.listClaimsDueRows = []sqlcdb.SimWaiverClaim{claim(1, 1, 100, 0)}
	s.setPriorities(priority(1, 1))
	s.tx.commitErr = errors.New("deadlock detected")

	_, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, s.input())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deadlock detected")
}

// ----------------------------------------------------------------------------
// B4 (2026-07-06 review) — priority read happens inside the transaction,
// not before it, so ListSimWaiverPriorityByPool's FOR UPDATE lock actually
// covers the read-modify-write. stubTransactor.beforeFn fires at InTx
// entry, before ProcessWaivers' callback runs — if the priority read had
// already happened outside InTx (the old code path), listPriorityArgs
// would be non-empty by the time beforeFn observes it.
// ----------------------------------------------------------------------------

func (s *ProcessWaiversTestSuite) TestListPriority_ReadInsideTransaction() {
	t := s.T()
	s.queries.listClaimsDueRows = []sqlcdb.SimWaiverClaim{claim(100, 1, 8478402, 0)}
	s.setPriorities(priority(1, 1))

	var argsAtTxEntry int
	s.tx.beforeFn = func() {
		argsAtTxEntry = len(s.queries.listPriorityArgs)
	}

	future, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, s.input())
	require.NoError(t, err)
	var got ProcessWaiversResult
	require.NoError(t, future.Get(&got))

	assert.Zero(t, argsAtTxEntry, "priority must not be read before InTx begins")
	assert.Len(t, s.queries.listPriorityArgs, 1, "priority read exactly once, inside the tx callback")
}

// SIM-I7: the pool lock comes before every read of claims or priorities.
// Reading the claims first (the old code read them before the
// transaction) would let an overlapping attempt resolve a stale snapshot
// after another attempt committed.
func (s *ProcessWaiversTestSuite) TestPoolLock_PrecedesClaimAndPriorityReads() {
	t := s.T()
	s.queries.listClaimsDueRows = []sqlcdb.SimWaiverClaim{claim(100, 1, 8478402, 0)}
	s.setPriorities(priority(1, 1))

	readsBeforeLock := -1
	s.queries.lockPoolHook = func() {
		readsBeforeLock = len(s.queries.listClaimsForDuePlayersArgs) +
			len(s.queries.listPriorityArgs) + len(s.queries.initPriorityCalls)
	}

	_, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, s.input())
	require.NoError(t, err)
	assert.Equal(t, 1, s.tx.inTxCalled, "one transaction covers lock, reads and writes")
	assert.Zero(t, readsBeforeLock, "no claim or priority read may precede LockSimPool")
	assert.Len(t, s.queries.listClaimsForDuePlayersArgs, 1)
}

// ----------------------------------------------------------------------------
// resolved_at threads through to the status update.
// ----------------------------------------------------------------------------

func (s *ProcessWaiversTestSuite) TestResolvedAt_CarriesSimDate() {
	t := s.T()
	in := s.input()
	s.queries.listClaimsDueRows = []sqlcdb.SimWaiverClaim{claim(100, 1, 8478402, 0)}
	s.setPriorities(priority(1, 1))

	_, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, in)
	require.NoError(t, err)
	require.Len(t, s.queries.resolveClaimCalls, 1)
	assert.Equal(t, "2024-11-15", s.queries.resolveClaimCalls[0].ResolvedAt.Time.Format("2006-01-02"))
}

// pin: input slice not mutated by the activity (the priority restamp
// allocates new slices).
func (s *ProcessWaiversTestSuite) TestInputPriorities_NotMutated() {
	t := s.T()
	s.queries.listClaimsDueRows = []sqlcdb.SimWaiverClaim{claim(1, 1, 100, 0)}
	originalPriorities := []sqlcdb.SimWaiverPriority{
		priority(1, 1), priority(2, 2), priority(3, 3),
	}
	s.setPriorities(originalPriorities...)

	_, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, s.input())
	require.NoError(t, err)

	// The slice the stub returned should still hold the same priority numbers.
	assert.Equal(t, int32(1), originalPriorities[0].Priority)
	assert.Equal(t, int32(2), originalPriorities[1].Priority)
	assert.Equal(t, int32(3), originalPriorities[2].Priority)
}

// rosterRow is a sim_rosters fixture for the capacity-recheck tests.
func rosterRow(agentID int32, playerID int64) sqlcdb.SimRoster {
	return sqlcdb.SimRoster{PoolID: 1, AgentID: agentID, PlayerID: playerID, Slot: string(SlotBN)}
}

// ----------------------------------------------------------------------------
// Finding #5 — cross-day duplicate claim resolves cleanly: a winning
// claim cancels the loser's still-pending claim so it can't later resolve
// as a phantom uncontested win (UNIQUE roster violation that wedges the loop).
// ----------------------------------------------------------------------------

func (s *ProcessWaiversTestSuite) TestCrossDayDuplicate_CancelsOtherPendingClaims() {
	t := s.T()
	in := s.input()
	// Two agents claim the same player. Agent 1 (priority 1) is due today;
	// agent 2's claim is due later but the grouped query pulls it in too.
	c1 := claim(100, 1, 8478402, 0)
	c2 := claim(101, 2, 8478402, 0)
	s.queries.listClaimsForDuePlayersRows = []sqlcdb.SimWaiverClaim{c1, c2}
	s.setPriorities(priority(1, 1), priority(2, 2))

	_, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, in)
	require.NoError(t, err)

	// Agent 1 wins; both claims resolved in-group (101 → lost), and the
	// cancel-others query runs to void any remaining pending claims on
	// the player except the winning claim.
	require.Len(t, s.queries.insertRosterCalls, 1)
	assert.Equal(t, int32(1), s.queries.insertRosterCalls[0].AgentID)
	require.Len(t, s.queries.cancelClaimsCalls, 1)
	cc := s.queries.cancelClaimsCalls[0]
	assert.Equal(t, int64(8478402), cc.PlayerID)
	assert.Equal(t, int32(100), cc.ID, "winning claim id is excluded from the cancel sweep")
}

// ----------------------------------------------------------------------------
// Finding #11 — waiver resolution re-validates state.
// ----------------------------------------------------------------------------

// Player already on some roster at process time → no win, no roster
// mutation, every claim resolves as lost (avoids the UNIQUE violation).
func (s *ProcessWaiversTestSuite) TestResolution_PlayerAlreadyRostered_ResolvesLost() {
	t := s.T()
	in := s.input()
	s.queries.listClaimsForDuePlayersRows = []sqlcdb.SimWaiverClaim{claim(100, 1, 8478402, 0)}
	s.setPriorities(priority(1, 1))
	s.queries.existsRosterPlayerByPlayer = map[int64]bool{8478402: true}

	future, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, in)
	require.NoError(t, err)
	var got ProcessWaiversResult
	require.NoError(t, future.Get(&got))

	assert.Equal(t, 0, got.ClaimsWon)
	assert.Equal(t, 1, got.ClaimsLost)
	assert.Empty(t, s.queries.insertRosterCalls, "no roster insert when player already rostered")
	assert.Empty(t, s.queries.insertAddCalls)
	require.Len(t, s.queries.resolveClaimCalls, 1)
	assert.Equal(t, string(WaiverClaimStatusLost), s.queries.resolveClaimCalls[0].Status)
}

// Designated drop player already left the roster (absent from the
// winner's roster listing) → no delete is attempted and no phantom drop
// tx row is written; the add still applies if it fits.
func (s *ProcessWaiversTestSuite) TestResolution_VanishedDropPlayer_NoPhantomDrop() {
	t := s.T()
	in := s.input()
	s.queries.listClaimsForDuePlayersRows = []sqlcdb.SimWaiverClaim{
		claim(100, 1, 8478402, 8499999), // wants to drop a player who's gone
	}
	s.setPriorities(priority(1, 1))
	// Roster holds someone else; 8499999 has vanished.
	s.queries.listFullRosterByAgent = map[int32][]sqlcdb.SimRoster{
		1: {rosterRow(1, 111)},
	}

	future, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, in)
	require.NoError(t, err)
	var got ProcessWaiversResult
	require.NoError(t, future.Get(&got))

	assert.Equal(t, 1, got.ClaimsWon, "add still fits within capacity")
	assert.Empty(t, s.queries.deleteRosterRowsCalls, "no delete attempted for a drop player the plan didn't see")
	assert.Empty(t, s.queries.insertDropCalls, "no drop tx for a vanished drop player")
	require.Len(t, s.queries.insertAddCalls, 1)
	assert.False(t, s.queries.insertAddCalls[0].DropPlayerID.Valid,
		"add tx drop_player_id is NULL when the drop didn't happen")
}

// Vanished drop AND roster already at capacity → the add can't fit, so
// the claim resolves as lost rather than overflowing the roster.
func (s *ProcessWaiversTestSuite) TestResolution_VanishedDropAtCapacity_ResolvesLost() {
	t := s.T()
	in := s.input()
	in.RosterCapacity = 2
	s.queries.listClaimsForDuePlayersRows = []sqlcdb.SimWaiverClaim{
		claim(100, 1, 8478402, 8499999),
	}
	s.setPriorities(priority(1, 1))
	// Winner already holds 2 players (at capacity) and 8499999 has
	// vanished, so nothing frees a spot; the add would make 3.
	s.queries.listFullRosterByAgent = map[int32][]sqlcdb.SimRoster{
		1: {rosterRow(1, 111), rosterRow(1, 222)},
	}

	future, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, in)
	require.NoError(t, err)
	var got ProcessWaiversResult
	require.NoError(t, future.Get(&got))

	assert.Equal(t, 0, got.ClaimsWon)
	assert.Equal(t, 1, got.ClaimsLost)
	assert.Empty(t, s.queries.insertRosterCalls, "over-capacity add is not applied")
	assert.Empty(t, s.queries.insertAddCalls)
	assert.Empty(t, s.queries.deleteRosterRowsCalls, "rejected claim never touches the roster")
}

// An agent winning two claims that share one drop_player_id. The stub's
// seeded roster is mutated by the stub's delete/insert, modelling
// read-your-writes inside the transaction: the first resolution sees the
// shared drop, removes it and logs the drop; the second no longer sees
// it, so it attempts no delete and logs no second drop tx. Roster
// evolution with capacity 3: {111, drop} → {111} → {111, P1} → {111, P1,
// P2}; the second add fits without a drop.
func (s *ProcessWaiversTestSuite) TestResolution_TwoClaimsShareDropPlayer_NoDoubleDrop() {
	t := s.T()
	in := s.input()
	in.RosterCapacity = 3
	s.queries.listClaimsForDuePlayersRows = []sqlcdb.SimWaiverClaim{
		claim(100, 1, 8478402, 8499999),
		claim(101, 1, 8480039, 8499999),
	}
	s.setPriorities(priority(1, 1))
	s.queries.listFullRosterByAgent = map[int32][]sqlcdb.SimRoster{
		1: {rosterRow(1, 111), rosterRow(1, 8499999)},
	}

	future, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, in)
	require.NoError(t, err)
	var got ProcessWaiversResult
	require.NoError(t, future.Get(&got))

	assert.Equal(t, 2, got.ClaimsWon, "both players awarded (capacity allows)")
	require.Len(t, s.queries.insertRosterCalls, 2, "both added to BN")
	require.Len(t, s.queries.deleteRosterRowsCalls, 1, "shared drop deleted once")
	require.Len(t, s.queries.insertDropCalls, 1, "shared drop logged once (no double-drop)")
	require.Len(t, s.queries.insertAddCalls, 2)
	assert.True(t, s.queries.insertAddCalls[0].DropPlayerID.Valid, "first add references the drop")
	assert.False(t, s.queries.insertAddCalls[1].DropPlayerID.Valid, "second add has no drop to reference")
}

// ----------------------------------------------------------------------------
// Conditional roster-loss (2026-09-17 review) — a valid designated drop
// followed by an over-capacity rejection must leave the roster unchanged:
// no delete, no drop tx, no add. Before the fix the drop was applied
// before the capacity check, so the rejected claim still removed the
// player.
// ----------------------------------------------------------------------------

func (s *ProcessWaiversTestSuite) TestResolution_ValidDropButOverCapacity_RosterUntouched() {
	t := s.T()
	in := s.input()
	in.RosterCapacity = 2
	s.queries.listClaimsForDuePlayersRows = []sqlcdb.SimWaiverClaim{
		claim(100, 1, 8478402, 8499999),
		claim(101, 2, 8478402, 0), // in-group loser
	}
	s.setPriorities(priority(1, 1), priority(2, 2))
	// Winner holds 3 (already over the capacity of 2, e.g. capacity was
	// lowered after filing); dropping one still leaves 2, so the add
	// would make 3 — the claim must be rejected without touching the roster.
	s.queries.listFullRosterByAgent = map[int32][]sqlcdb.SimRoster{
		1: {rosterRow(1, 111), rosterRow(1, 222), rosterRow(1, 8499999)},
	}

	future, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, in)
	require.NoError(t, err)
	var got ProcessWaiversResult
	require.NoError(t, future.Get(&got))

	assert.Equal(t, 0, got.ClaimsWon)
	assert.Equal(t, 2, got.ClaimsLost, "winner and in-group loser both resolve lost")
	assert.Empty(t, s.queries.deleteRosterRowsCalls, "designated drop is NOT applied on rejection")
	assert.Empty(t, s.queries.insertDropCalls, "no drop event on rejection")
	assert.Empty(t, s.queries.insertRosterCalls)
	assert.Empty(t, s.queries.insertAddCalls)
	assert.Len(t, s.queries.listFullRosterByAgent[1], 3, "roster unchanged")
	for _, c := range s.queries.resolveClaimCalls {
		assert.Equal(t, string(WaiverClaimStatusLost), c.Status)
	}
}

// A drop that fits exactly: roster at capacity, valid drop frees the one
// spot the add needs. Drop and add are both applied (atomic win path).
func (s *ProcessWaiversTestSuite) TestResolution_ValidDropFreesSpot_Wins() {
	t := s.T()
	in := s.input()
	in.RosterCapacity = 2
	s.queries.listClaimsForDuePlayersRows = []sqlcdb.SimWaiverClaim{
		claim(100, 1, 8478402, 8499999),
	}
	s.setPriorities(priority(1, 1))
	s.queries.listFullRosterByAgent = map[int32][]sqlcdb.SimRoster{
		1: {rosterRow(1, 111), rosterRow(1, 8499999)},
	}

	future, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, in)
	require.NoError(t, err)
	var got ProcessWaiversResult
	require.NoError(t, future.Get(&got))

	assert.Equal(t, 1, got.ClaimsWon)
	require.Len(t, s.queries.deleteRosterRowsCalls, 1)
	require.Len(t, s.queries.insertDropCalls, 1)
	require.Len(t, s.queries.insertRosterCalls, 1)
	assert.Len(t, s.queries.listFullRosterByAgent[1], 2, "roster stays at capacity after drop+add")
}

// The plan saw the drop player, but the delete affects 0 rows — the
// roster changed underneath the transaction and the capacity verdict is
// void. The activity must fail (rolling the tx back) rather than commit
// an add the roster may not fit.
func (s *ProcessWaiversTestSuite) TestResolution_DropVanishesAfterPlan_Aborts() {
	t := s.T()
	in := s.input()
	s.queries.listClaimsForDuePlayersRows = []sqlcdb.SimWaiverClaim{
		claim(100, 1, 8478402, 8499999),
	}
	s.setPriorities(priority(1, 1))
	s.queries.listFullRosterByAgent = map[int32][]sqlcdb.SimRoster{
		1: {rosterRow(1, 8499999)},
	}
	// Override the stub's stateful delete: report 0 rows despite the listing.
	s.queries.deleteRosterRowsByPlayer = map[int64]int64{8499999: 0}

	_, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, in)
	require.Error(t, err)
	// The activity test env serialises the error into a Temporal
	// ApplicationError, which drops Go error identity, so errors.Is
	// can't cross the boundary — match on the sentinel's message.
	assert.ErrorContains(t, err, errWaiverDropVanished.Error())
	assert.Empty(t, s.queries.insertDropCalls, "no drop tx when the delete removed nothing")
	assert.Empty(t, s.queries.insertRosterCalls, "add is not attempted after the invariant failure")
}

// ----------------------------------------------------------------------------
// Missing-priority claimants sort last, deterministically.
// ----------------------------------------------------------------------------

func TestResolveGroup_MissingPrioritySortsLast(t *testing.T) {
	group := []sqlcdb.SimWaiverClaim{
		{ID: 1, AgentID: 9, PlayerID: 100}, // no priority row
		{ID: 2, AgentID: 2, PlayerID: 100}, // priority 2
	}
	priorities := []sqlcdb.SimWaiverPriority{{AgentID: 2, Priority: 2}}

	r := resolveGroup(group, priorities)
	assert.Equal(t, int32(2), r.winner.AgentID, "seeded agent beats a claimant with no priority row")
	require.Len(t, r.losers, 1)
	assert.Equal(t, int32(9), r.losers[0].AgentID)
}

func TestResolveGroup_TwoMissingPriorities_TieBreakByClaimID(t *testing.T) {
	group := []sqlcdb.SimWaiverClaim{
		{ID: 7, AgentID: 8, PlayerID: 100},
		{ID: 3, AgentID: 9, PlayerID: 100},
	}
	r := resolveGroup(group, nil)
	assert.Equal(t, int32(3), r.winner.ID, "lowest claim ID wins among equally-unranked claimants")
}

// ============================================================================
// Low-5: same-day contested waiver groups must resolve with live
// priority rotation — the winner of group 1 drops to bottom before
// group 2 is evaluated, not after all groups resolve.
// ============================================================================

// Two contested groups on the same day. Pre-resolution priority: 1, 2, 3.
// Group 1: agents 1 and 2 claim player A → agent 1 (priority 1) wins,
// demoted to bottom → live order for group 2 is [2, 3, 1].
// Group 2: agents 1 and 3 claim player B → agent 3 (priority 2 in the
// live order, ahead of agent 1 who is now at priority 3) wins.
//
// Without the fix: group 2 would use the original snapshot [1, 2, 3],
// agent 1 would win group 2 as well.
func (s *ProcessWaiversTestSuite) TestContestedGroups_WinnerRotatesBeforeSecondGroup() {
	t := s.T()
	in := s.input()

	// Group 1: player 8478402 contested by agents 1 and 2.
	// Group 2: player 8480039 contested by agents 1 and 3.
	s.queries.listClaimsForDuePlayersRows = []sqlcdb.SimWaiverClaim{
		claim(100, 1, 8478402, 0),
		claim(101, 2, 8478402, 0),
		claim(102, 1, 8480039, 0),
		claim(103, 3, 8480039, 0),
	}
	s.setPriorities(
		priority(1, 1), priority(2, 2), priority(3, 3),
	)

	future, err := s.env.ExecuteActivity(s.acts.ProcessWaivers, in)
	require.NoError(t, err)
	var got ProcessWaiversResult
	require.NoError(t, future.Get(&got))

	require.Equal(t, 2, got.ClaimsWon)
	require.Len(t, s.queries.insertRosterCalls, 2)

	// Build a map of player_id → winning agent_id from the roster inserts.
	winnerByPlayer := map[int64]int32{}
	for _, rc := range s.queries.insertRosterCalls {
		winnerByPlayer[rc.PlayerID] = rc.AgentID
	}

	// Agent 1 wins group 1 (priority 1 before any rotation).
	assert.Equal(t, int32(1), winnerByPlayer[8478402],
		"group 1: agent 1 (priority 1) must win player 8478402")

	// After agent 1 is demoted, live order for group 2 is [2, 3, 1].
	// Agent 3 (position 2 in the rotated list) beats agent 1 (position 3).
	assert.Equal(t, int32(3), winnerByPlayer[8480039],
		"group 2: agent 3 must win player 8480039 because agent 1 was demoted to bottom after winning group 1")
}
