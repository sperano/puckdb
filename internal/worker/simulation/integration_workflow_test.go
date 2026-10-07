//go:build integration

package simulation

// Workflow-driven integration tests: spin up a real Temporal worker
// running the full SimPoolWorkflow, with mock LLM clients that return
// deterministic tool calls. After the workflow completes, assert the
// SUM-invariants on the sim_agent_* tables.
//
// Env vars, seed helpers, and worker bootstrap live in
// integration_helpers_test.go (shared with the livellm build tag).
// Tests skip cleanly when their required services aren't reachable —
// see startTemporalForTest and connectRedisForTest there.

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Fixture seeding — the fixed 2-team / 4-player / 5-day scenario
// ============================================================================

// seedScenario seeds the full data set the workflow test needs:
//   - Two teams in the test season AND prior season (LoadDraftCandidates
//     reads prior-season stats; CollectDayStats reads test-season game
//     stats).
//   - Four players (two skaters per team) across both seasons.
//   - Prior-season club_skater_stats so the draft has candidates.
//   - Five games in the test season, one per day, with two
//     game_skater_stats rows per game (the two drafted players).
//
// Returns the slice of player IDs in scripted draft order. The mock
// LLM uses these to produce its scripted draft_player calls.
func seedScenario(t *testing.T, ctx context.Context, pool *pgxpool.Pool, testSeason int32) (playerIDs []int64) {
	t.Helper()
	priorSeason := testSeason - 10001

	// Seed seasons (the suite-level setup already inserted testSeason
	// when the smoke test ran; this is idempotent so it's fine).
	seedSeason(t, ctx, pool, testSeason)
	seedSeason(t, ctx, pool, priorSeason)

	const teamA, teamB int64 = 4001, 4002
	for _, season := range []int32{testSeason, priorSeason} {
		seedTeam(t, ctx, pool, season, teamA, "TMA")
		seedTeam(t, ctx, pool, season, teamB, "TMB")
	}

	// Two skaters per team — total 4 candidates. The mock LLM picks
	// the first two; the rest stay as free agents (proves the FA
	// pool query works for ManageRoster on day-1+).
	const p1, p2, p3, p4 int64 = 5001, 5002, 5003, 5004
	playerIDs = []int64{p1, p2, p3, p4}
	for _, id := range playerIDs {
		seedPlayer(t, ctx, pool, id, sqlcdb.PlayerPositionC)
	}

	// Prior-season club stats: pinned numbers so the ranking is
	// deterministic. P1 has the highest score (10+15=25), others
	// trail.
	seedClubSkaterStats(t, ctx, pool, priorSeason, p1, teamA, 10, 15) // 25
	seedClubSkaterStats(t, ctx, pool, priorSeason, p2, teamA, 8, 10)  // 18
	seedClubSkaterStats(t, ctx, pool, priorSeason, p3, teamB, 5, 5)   // 10
	seedClubSkaterStats(t, ctx, pool, priorSeason, p4, teamB, 3, 4)   // 7

	// Five games, one per day starting Oct 8 — TeamA vs TeamB.
	const startDate = "2024-10-08"
	gameIDs := []int64{6001, 6002, 6003, 6004, 6005}
	for i, gid := range gameIDs {
		date := offsetDate(startDate, i)
		seedGame(t, ctx, pool, gid, testSeason, date, teamA, teamB)
		// Both drafted players play every game. Goals/assists
		// vary so daily values are non-zero and distinct.
		seedGameSkaterStats(t, ctx, pool, gid, p1, teamA, 1, i%2)
		seedGameSkaterStats(t, ctx, pool, gid, p2, teamA, 0, 1)
	}

	return playerIDs
}

// ============================================================================
// Mock LLM — scripted responses for predictable workflow behavior
//
// Dispatch is by REQUEST SHAPE, not call count:
//
//   - The tool list offered on the request identifies the phase:
//     set_team_name → Phase 0, draft_player → draft, otherwise daily.
//   - A role:"tool" message in the history marks an agentloop
//     CONTINUATION round (the loop already executed our scripted tool
//     call and is asking for a follow-up) — the mock answers with a
//     plain pass so the loop terminates.
//
// This is robust against the things a call counter can't survive:
// Phase 0 team-name activities fan out in parallel, agentloop makes
// a variable number of Complete calls per turn, and the per-day agent
// processing order is shuffled.
// ============================================================================

// reqOffersTool reports whether the request's tool list contains name.
func reqOffersTool(req *llm.Request, name string) bool {
	for _, tool := range req.Tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

// reqIsContinuation reports whether the request already carries a
// tool-result message — i.e., this is a follow-up agentloop round
// after the mock's scripted tool call was executed.
func reqIsContinuation(req *llm.Request) bool {
	for _, m := range req.Messages {
		if m.Role == "tool" {
			return true
		}
	}
	return false
}

// perAgentScript is the per-agent behavior description for tests
// where agents must act differently on different days.
//
// teamName doubles as the agent's stable identity: tests seed
// distinct names ("Alpha", "Bravo") and resolve agent IDs back via
// lookupAgentIDByTeamName after the workflow ran Phase 0.
//
// draftPicks[i] is the player drafted on this agent's i-th draft turn
// (one turn per round). Assign disjoint players across agents — each
// agent picks its own list regardless of the shuffled snake order.
//
// daily[i] is the scripted first-round response for sim day i+1
// (daily[0] = day 1). A nil entry — or running off the end — is a
// pass. The per-day index advances only on a daily turn's FIRST
// round; continuation rounds don't consume script entries.
type perAgentScript struct {
	teamName   string
	draftPicks []int64
	daily      []*llm.Response
}

// perAgentMockClient wraps one agent's perAgentScript. Each Agent the
// AgentFactory builds gets its OWN client (perAgentFactory keeps one
// client per agent id, so the draftIdx/dailyIdx counters advance in
// sync with that agent's actual turns).
type perAgentMockClient struct {
	script   *perAgentScript
	draftIdx atomic.Int32
	dailyIdx atomic.Int32
}

func (c *perAgentMockClient) Complete(_ context.Context, req *llm.Request) (*llm.Response, error) {
	if reqIsContinuation(req) {
		return passResponse(), nil
	}
	if reqOffersTool(req, ToolSetTeamName) {
		return teamNameResponse(c.script.teamName), nil
	}
	if reqOffersTool(req, ToolDraftPlayer) {
		idx := int(c.draftIdx.Add(1)) - 1
		if idx < len(c.script.draftPicks) {
			return draftPickResponse(c.script.draftPicks[idx]), nil
		}
		// Script exhausted — should not happen in a well-sized test;
		// pass and let the activity's no-pick handling surface it.
		return passResponse(), nil
	}
	// Daily turn, first round: consume the next day's entry.
	idx := int(c.dailyIdx.Add(1)) - 1
	if idx < len(c.script.daily) && c.script.daily[idx] != nil {
		return c.script.daily[idx], nil
	}
	return passResponse(), nil
}

// teamNameResponse builds a scripted set_team_name tool-call response.
// Both arguments are required by PickTeamName's validation (name
// non-empty, summary non-empty and <= MaxStrategySummaryChars).
func teamNameResponse(name string) *llm.Response {
	return &llm.Response{
		Content: fmt.Sprintf("Naming my team %s", name),
		ToolCalls: []llm.ToolCall{{
			ID:   "tc_teamname",
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      ToolSetTeamName,
				Arguments: fmt.Sprintf(`{"name":%q,"summary":"test strategy"}`, name),
			},
		}},
		Usage: &llm.Usage{PromptTokens: 50, CompletionTokens: 15, TotalTokens: 65},
	}
}

// passResponse is the canonical "no tool calls" reply — the workflow
// treats a response with empty ToolCalls as a pass.
func passResponse() *llm.Response {
	return &llm.Response{
		Content: "Pass.",
		Usage:   &llm.Usage{PromptTokens: 200, CompletionTokens: 10, TotalTokens: 210},
	}
}

// toolCallResponse builds a one-tool-call response with args
// marshaled from the actual *Args struct — the same types the
// executor parses back, so scripted calls can't drift from the tool
// schemas the way hand-built JSON strings can.
func toolCallResponse(toolName string, args any, content string) *llm.Response {
	b, err := json.Marshal(args)
	if err != nil {
		panic(fmt.Sprintf("marshal %s args: %v", toolName, err))
	}
	return &llm.Response{
		Content: content,
		ToolCalls: []llm.ToolCall{{
			ID:   "tc_" + toolName,
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      toolName,
				Arguments: string(b),
			},
		}},
		Usage: &llm.Usage{PromptTokens: 200, CompletionTokens: 20, TotalTokens: 220},
	}
}

// draftPickResponse builds a scripted draft_player tool-call response.
func draftPickResponse(playerID int64) *llm.Response {
	return toolCallResponse(ToolDraftPlayer, DraftPlayerArgs{PlayerID: playerID},
		fmt.Sprintf("Picking player %d", playerID))
}

// dropPlayerResponse builds a scripted drop_player tool-call response.
func dropPlayerResponse(playerID int64, reason string) *llm.Response {
	return toolCallResponse(ToolDropPlayer, DropPlayerArgs{PlayerID: playerID, Reason: reason}, reason)
}

// setLineupMovesResponse builds a scripted set_lineup tool-call
// response with an arbitrary move list. Needed on day 1: drafted
// players land on BN, and CollectDayStats scores ACTIVE slots only
// (BN/IR excluded) — a passive agent scores zero all season.
func setLineupMovesResponse(moves []LineupMoveArg, reason string) *llm.Response {
	return toolCallResponse(ToolSetLineup, SetLineupArgs{Moves: moves, Reason: reason}, reason)
}

// setLineupResponse is the single-move shorthand.
func setLineupResponse(playerID int64, slot RosterSlot, reason string) *llm.Response {
	return setLineupMovesResponse([]LineupMoveArg{{PlayerID: playerID, Slot: slot}}, reason)
}

// addPlayerResponse builds a scripted add_player tool-call response.
// dropPlayerID may be nil (roster must then have a free slot).
func addPlayerResponse(playerID int64, dropPlayerID *int64, reason string) *llm.Response {
	return toolCallResponse(ToolAddPlayer, AddPlayerArgs{PlayerID: playerID, DropPlayerID: dropPlayerID, Reason: reason}, reason)
}

// claimPlayerResponse builds a scripted claim_player tool-call
// response. dropPlayerID may be nil when the roster has a free slot;
// a full roster needs the contingent drop or ValidateClaimPlayer
// rejects the claim on the future-state capacity check.
func claimPlayerResponse(playerID int64, dropPlayerID *int64, reason string) *llm.Response {
	return toolCallResponse(ToolClaimPlayer, ClaimPlayerArgs{PlayerID: playerID, DropPlayerID: dropPlayerID, Reason: reason}, reason)
}

// ============================================================================
// Pool setup — bypass the GraphQL resolver for direct DB control
// ============================================================================

// createIntegrationPool inserts a sim_pools row + 2 sim_agents rows
// directly. Mirrors what graph/simulation_helpers.go's
// createSimPoolImpl does, minus the workflow start (the test starts
// the workflow itself with explicit options).
//
// Uses tiny roster sizes (1 active slot, 1 BN, 0 IR) and 1 draft
// round so the draft completes in 2 picks total. waiverDays is
// per-test: drop-pickup tests pick whatever fits the day window.
//
// poolName lets each test use a distinct row so leftover state from
// a previously-aborted run can be filtered (the suite-level
// MigrateDown also wipes everything, but a per-test name keeps test
// runs distinguishable in logs).
//
// startDate/endDate bound the pool's simulation window (migration
// 000016) — this replaced the old UPDATE-the-seasons-row hack, so
// tests exercise the same per-pool window path production uses.
func createIntegrationPool(t *testing.T, ctx context.Context, pool *pgxpool.Pool, season int32, waiverDays int, poolName, startDate, endDate string) (poolID int32, agent1ID, agent2ID int32) {
	t.Helper()
	q := sqlcdb.New(pool)
	cap, err := pgNumericFromFloat(200.0)
	require.NoError(t, err)
	row, err := q.InsertSimPool(ctx, sqlcdb.InsertSimPoolParams{
		Name:                 poolName,
		Season:               season,
		Status:               string(PoolStatusDraft),
		NumTeams:             2,
		WaiverDays:           int32(waiverDays),
		DraftRounds:          1,
		MaxLLMCostUsdPerPool: cap,
		Categories:           []string{"G", "A"},
		RosterC:              1,
		RosterBN:             1,
		StopAfter:            StopAfterNever.String(),
		StartDate:            mustPgDate(startDate),
		EndDate:              mustPgDate(endDate),
	})
	require.NoError(t, err)
	poolID = row.ID

	a1, err := q.InsertSimAgent(ctx, sqlcdb.InsertSimAgentParams{
		PoolID:   poolID,
		Provider: "anthropic", Model: "claude-haiku-4-5", Strategy: "balanced",
	})
	require.NoError(t, err)
	a2, err := q.InsertSimAgent(ctx, sqlcdb.InsertSimAgentParams{
		PoolID:   poolID,
		Provider: "anthropic", Model: "claude-haiku-4-5", Strategy: "aggressive",
	})
	require.NoError(t, err)
	return poolID, a1.ID, a2.ID
}

// ============================================================================
// TestIntegrationFullDraftAnd5Day — the headline workflow test
// ============================================================================

func TestIntegrationFullDraftAnd5Day(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	redisClient := connectRedisForTest(t)
	defer redisClient.Close()
	tc, tcCleanup := startTemporalForTest(t)
	defer tcCleanup()

	ctx := context.Background()
	const testSeason int32 = 20242025

	playerIDs := seedScenario(t, ctx, pgPool, testSeason)
	// Pool window = the 5 seeded game days (inclusive) — the per-pool
	// start_date/end_date bound the day loop.
	poolID, agent1ID, agent2ID := createIntegrationPool(t, ctx, pgPool, testSeason, 2,
		"integration_test_workflow", "2024-10-08", offsetDate("2024-10-08", 4))

	// Per-agent scripts: each agent drafts its own player, moves them
	// from BN into the active C slot on day 1 (drafted players land on
	// BN; only active slots score), then passes for the rest of the
	// 5-day window.
	scripts := map[int32]*perAgentScript{
		agent1ID: {
			teamName:   "Alpha",
			draftPicks: []int64{playerIDs[0]},
			daily: []*llm.Response{
				setLineupResponse(playerIDs[0], SlotC, "Activate my pick"),
			},
		},
		agent2ID: {
			teamName:   "Bravo",
			draftPicks: []int64{playerIDs[1]},
			daily: []*llm.Response{
				setLineupResponse(playerIDs[1], SlotC, "Activate my pick"),
			},
		},
	}
	stop := setupIntegrationWorker(t, tc, pgPool, redisClient,
		perAgentFactory(scripts))
	defer stop()

	runWorkflowToCompletion(t, ctx, tc, poolID, 60*time.Second)

	// Invariant assertions — read from the DB and check the
	// fundamental sums.
	assertTotalsEqualSumOfDailies(t, ctx, pgPool, poolID)
	assertDailyEqualsSumOfPerPlayer(t, ctx, pgPool, poolID)
	assertDraftedTwoPlayers(t, ctx, pgPool, poolID, playerIDs[:2])
	assertPoolCompleted(t, ctx, pgPool, poolID)
}

// ============================================================================
// TestIntegrationDropPickupHistoricalAttribution — agent A drops player
// X on day 3; verify days 1-2 attribution rows for that player still
// exist (the per-player attribution layer is the source of truth and
// must NOT be re-written / deleted on later drops).
// ============================================================================

func TestIntegrationDropPickupHistoricalAttribution(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	redisClient := connectRedisForTest(t)
	defer redisClient.Close()
	tc, tcCleanup := startTemporalForTest(t)
	defer tcCleanup()

	ctx := context.Background()
	const testSeason int32 = 20242025

	playerIDs := seedScenario(t, ctx, pgPool, testSeason)
	poolID, alphaAgentID, bravoAgentID := createIntegrationPool(t, ctx, pgPool, testSeason, 2,
		"integration_test_droppickup", "2024-10-08", offsetDate("2024-10-08", 4))

	// Per-agent scripts, keyed by sim_agents.id. Phase dispatch is by
	// request shape (see perAgentMockClient); the daily slice indexes
	// by sim day:
	//   daily[0] = day 1 manage_roster (Oct 8) — activate the pick
	//              (drafted players land on BN; only active slots score)
	//   daily[1] = day 2 manage_roster (Oct 9) — pass
	//   daily[2] = day 3 manage_roster (Oct 10) — Alpha drops player A
	//   daily[3] = day 4 (Oct 11) — pass
	//   daily[4] = day 5 (Oct 12) — pass
	scripts := map[int32]*perAgentScript{
		alphaAgentID: {
			teamName:   "Alpha",
			draftPicks: []int64{playerIDs[0]},
			daily: []*llm.Response{
				setLineupResponse(playerIDs[0], SlotC, "Activate my pick"), // day 1
				nil, // day 2 pass
				dropPlayerResponse(playerIDs[0], "Drop on day 3"), // day 3 DROP
				nil, // day 4 pass
				nil, // day 5 pass
			},
		},
		bravoAgentID: {
			teamName:   "Bravo",
			draftPicks: []int64{playerIDs[1]},
			daily: []*llm.Response{
				setLineupResponse(playerIDs[1], SlotC, "Activate my pick"), // day 1
				// Days 2-5: fall through to passResponse.
			},
		},
	}

	stop := setupIntegrationWorker(t, tc, pgPool, redisClient,
		perAgentFactory(scripts))
	defer stop()

	runWorkflowToCompletion(t, ctx, tc, poolID, 60*time.Second)

	// Resolve Alpha's agent_id via the team name Phase 0 persisted.
	// This doubles as an assertion that PickTeamName committed the
	// scripted set_team_name call; it must agree with the ID we keyed
	// the script by.
	alphaID := lookupAgentIDByTeamName(t, ctx, pgPool, poolID, "Alpha")
	require.Equal(t, alphaAgentID, alphaID, "team_name Alpha should belong to the agent whose script drops")

	// Days 1 and 2 should have per-player attribution rows for the
	// dropped player under Alpha. Day 3 onward must NOT (player was
	// dropped on day 3 BEFORE that day's CollectDayStats ran).
	assertPlayerAttributionRowsForDays(t, ctx, pgPool, poolID, alphaID, playerIDs[0],
		[]string{"2024-10-08", "2024-10-09"})
	assertNoPlayerAttributionRowsForDays(t, ctx, pgPool, poolID, alphaID, playerIDs[0],
		[]string{"2024-10-10", "2024-10-11", "2024-10-12"})

	// SUM-invariants still hold post-drop — the totals = sum of
	// dailies relationship is preserved even when agent rosters
	// shrink mid-sim.
	assertTotalsEqualSumOfDailies(t, ctx, pgPool, poolID)
	assertDailyEqualsSumOfPerPlayer(t, ctx, pgPool, poolID)
}

// ============================================================================
// TestIntegrationWaiverClaimResolution — agent A drops on day 3,
// agent B claims on day 4, ProcessWaivers awards to B on day 6.
//
// Waiver timing with waiver_days=2:
//   - On-waivers window (ListSimPlayersOnWaivers): a day-3 drop is
//     claimable while drop_date > sim_date - waiver_days, i.e. on
//     days 3 and 4. Claiming on day 3 would race Alpha's drop (agent
//     order is shuffled per day), so Bravo claims on day 4.
//   - process_date = claim day + waiver_days = day 6, so the season
//     window must span 6 days for ProcessWaivers to resolve it.
// ============================================================================

func TestIntegrationWaiverClaimResolution(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	redisClient := connectRedisForTest(t)
	defer redisClient.Close()
	tc, tcCleanup := startTemporalForTest(t)
	defer tcCleanup()

	ctx := context.Background()
	const testSeason int32 = 20242025

	playerIDs := seedScenario(t, ctx, pgPool, testSeason)
	// waiverDays=2: the day-3 drop stays claimable on day 4, and the
	// day-4 claim's process_date lands on day 6 (see header comment).
	// The pool window spans 6 days — day 6 exists solely so
	// ProcessWaivers can resolve the claim.
	poolID, alphaAgentID, bravoAgentID := createIntegrationPool(t, ctx, pgPool, testSeason, 2,
		"integration_test_waiver", "2024-10-08", offsetDate("2024-10-08", 5))

	// Alpha drafts playerIDs[0], drops them on day 3.
	// Bravo drafts playerIDs[1], drops them on day 2,
	// then claims playerIDs[0] on day 4.
	//
	// Why does Bravo drop their own pick? Bravo needs to make room
	// before claiming — roster size is 2 (1 active + 1 BN), and a
	// successful claim adds a player without a corresponding drop in
	// this test (the claim_player tool's optional drop_player_id arg
	// is NOT set). With 2 players already on the roster, the
	// FUTURE-state "roster full + 1" check in ValidateClaimPlayer
	// would reject the claim. Dropping first frees a slot.
	//
	// Bravo's day-2 drop sends playerIDs[1] to waivers. Alpha doesn't
	// claim it back, so it just expires — irrelevant to the test.
	scripts := map[int32]*perAgentScript{
		alphaAgentID: {
			teamName:   "Alpha",
			draftPicks: []int64{playerIDs[0]},
			daily: []*llm.Response{
				setLineupResponse(playerIDs[0], SlotC, "Activate my pick"), // day 1
				nil, // day 2
				dropPlayerResponse(playerIDs[0], "Alpha drops X on day 3"), // day 3 DROP
				nil, // day 4
				nil, // day 5
			},
		},
		bravoAgentID: {
			teamName:   "Bravo",
			draftPicks: []int64{playerIDs[1]},
			daily: []*llm.Response{
				setLineupResponse(playerIDs[1], SlotC, "Activate my pick"), // day 1
				dropPlayerResponse(playerIDs[1], "Bravo drops Y on day 2"), // day 2 DROP (frees roster slot)
				nil, // day 3 (Alpha drops X this turn)
				claimPlayerResponse(playerIDs[0], nil, "Bravo claims X on day 4"), // day 4 CLAIM
				nil, // day 5
			},
		},
	}

	stop := setupIntegrationWorker(t, tc, pgPool, redisClient,
		perAgentFactory(scripts))
	defer stop()

	runWorkflowToCompletion(t, ctx, tc, poolID, 60*time.Second)

	// Day 6: ProcessWaivers fires (start of the day loop). Bravo's
	// day-4 claim has process_date = day 6 → resolves today. Bravo is
	// the only claimant, wins.
	bravoID := lookupAgentIDByTeamName(t, ctx, pgPool, poolID, "Bravo")
	require.Equal(t, bravoAgentID, bravoID, "team_name Bravo should belong to the claiming agent")
	assertPlayerOnRoster(t, ctx, pgPool, poolID, bravoID, playerIDs[0])

	// The claim row's status flipped to 'won'.
	assertWaiverClaimStatus(t, ctx, pgPool, poolID, bravoID, playerIDs[0], "won")
}

// ============================================================================
// Helpers used by the new tests
// ============================================================================

// perAgentFactory builds an AgentFactory that hands each agent its
// own scripted LLM client, keyed by sim_agents.id (AgentConfig no
// longer carries a Name — the factory's agentID parameter is the
// only stable identity at construction time).
func perAgentFactory(scripts map[int32]*perAgentScript) AgentFactory {
	clients := map[int32]*perAgentMockClient{}
	return func(agentID int32, cfg AgentConfig, _ map[llm.Provider]llm.ProviderConfig, _ int) (*Agent, error) {
		script, ok := scripts[agentID]
		if !ok {
			return nil, fmt.Errorf("perAgentFactory: no script for agent %d", agentID)
		}
		// One client per agent id, cached so each agent's daily
		// counter persists across factory invocations (getOrCreateAgent
		// rebuilds after an eviction or for a different pool size).
		c, ok := clients[agentID]
		if !ok {
			c = &perAgentMockClient{script: script}
			clients[agentID] = c
		}
		return &Agent{
			ID:           agentID,
			Config:       cfg,
			Client:       c,
			systemPrompt: "test system for " + script.teamName,
			draftTools:   DraftTools(),
			dailyTools:   DailyTools(),
		}, nil
	}
}

// lookupAgentIDByTeamName returns the sim_agents.id matching the given
// (pool, team_name). The scripted set_team_name responses seed distinct
// names ("Alpha", "Bravo") during Phase 0, so tests can resolve the
// human-friendly script identity back to the int32 IDs the DB queries
// want. (sim_agents has no name column — team_name IS the identity.)
func lookupAgentIDByTeamName(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32, teamName string) int32 {
	t.Helper()
	var id int32
	err := pgPool.QueryRow(ctx, `SELECT id FROM sim_agents WHERE pool_id=$1 AND team_name=$2`, poolID, teamName).Scan(&id)
	require.NoErrorf(t, err, "lookup agent team_name=%q in pool %d", teamName, poolID)
	return id
}

// assertPlayerAttributionRowsForDays asserts at least one
// sim_agent_daily_player_stats row exists for the (pool, agent,
// player) triple on EACH of the given dates.
func assertPlayerAttributionRowsForDays(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID, agentID int32, playerID int64, dates []string) {
	t.Helper()
	for _, d := range dates {
		var n int
		err := pgPool.QueryRow(ctx, `
			SELECT COUNT(*) FROM sim_agent_daily_player_stats
			WHERE pool_id=$1 AND agent_id=$2 AND player_id=$3 AND date=$4::DATE
		`, poolID, agentID, playerID, d).Scan(&n)
		require.NoError(t, err)
		assert.Greaterf(t, n, 0,
			"expected sim_agent_daily_player_stats rows for agent=%d player=%d date=%s — historical attribution must persist after a drop",
			agentID, playerID, d)
	}
}

// assertNoPlayerAttributionRowsForDays asserts NO
// sim_agent_daily_player_stats row exists for the (pool, agent,
// player) triple on ANY of the given dates.
func assertNoPlayerAttributionRowsForDays(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID, agentID int32, playerID int64, dates []string) {
	t.Helper()
	for _, d := range dates {
		var n int
		err := pgPool.QueryRow(ctx, `
			SELECT COUNT(*) FROM sim_agent_daily_player_stats
			WHERE pool_id=$1 AND agent_id=$2 AND player_id=$3 AND date=$4::DATE
		`, poolID, agentID, playerID, d).Scan(&n)
		require.NoError(t, err)
		assert.Equalf(t, 0, n,
			"expected NO sim_agent_daily_player_stats for agent=%d player=%d date=%s — player was dropped before that day's scoring",
			agentID, playerID, d)
	}
}

// assertPlayerOnRoster asserts a sim_rosters row exists for the
// (pool, agent, player) triple. Used by the waiver test after
// ProcessWaivers should have moved the claimed player.
func assertPlayerOnRoster(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID, agentID int32, playerID int64) {
	t.Helper()
	var n int
	err := pgPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM sim_rosters
		WHERE pool_id=$1 AND agent_id=$2 AND player_id=$3
	`, poolID, agentID, playerID).Scan(&n)
	require.NoError(t, err)
	assert.Equalf(t, 1, n, "expected player %d on agent %d's roster", playerID, agentID)
}

// assertWaiverClaimStatus asserts the most-recent sim_waiver_claims
// row for the (pool, agent, player) triple has the given status
// ('won' / 'lost' / 'pending').
func assertWaiverClaimStatus(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID, agentID int32, playerID int64, expected string) {
	t.Helper()
	var status string
	err := pgPool.QueryRow(ctx, `
		SELECT status FROM sim_waiver_claims
		WHERE pool_id=$1 AND agent_id=$2 AND player_id=$3
		ORDER BY id DESC LIMIT 1
	`, poolID, agentID, playerID).Scan(&status)
	require.NoErrorf(t, err, "lookup waiver claim agent=%d player=%d", agentID, playerID)
	assert.Equal(t, expected, status)
}

// ============================================================================
// Invariant assertions
// ============================================================================

// assertTotalsEqualSumOfDailies verifies the contract:
//
//	sim_agent_totals.value = SUM(sim_agent_daily_stats.value)
//	  per (pool, agent, category)
//
// for every counting category (i.e., NOT GAA — GAA is computed from
// goalie_ga / goalie_toi_seconds components, not from value sums).
func assertTotalsEqualSumOfDailies(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32) {
	t.Helper()
	rows, err := pgPool.Query(ctx, `
		SELECT
			t.agent_id,
			t.category,
			t.value::TEXT AS total_value,
			COALESCE(SUM(d.value), 0)::TEXT AS sum_of_dailies
		FROM sim_agent_totals t
		LEFT JOIN sim_agent_daily_stats d
			ON d.pool_id = t.pool_id AND d.agent_id = t.agent_id AND d.category = t.category
		WHERE t.pool_id = $1 AND t.category <> 'GAA'
		GROUP BY t.agent_id, t.category, t.value
	`, poolID)
	require.NoError(t, err)
	defer rows.Close()

	checked := 0
	for rows.Next() {
		var agentID int32
		var category string
		var totalValue, sumOfDailies string
		require.NoError(t, rows.Scan(&agentID, &category, &totalValue, &sumOfDailies))
		assert.Equalf(t, totalValue, sumOfDailies,
			"INVARIANT: sim_agent_totals.value (%s) != SUM(sim_agent_daily_stats.value) (%s) for agent=%d category=%s",
			totalValue, sumOfDailies, agentID, category)
		checked++
	}
	require.NoError(t, rows.Err())
	assert.Greater(t, checked, 0, "expected at least one (agent, category) row to verify")
}

// assertDailyEqualsSumOfPerPlayer verifies the contract:
//
//	sim_agent_daily_stats.value = SUM(sim_agent_daily_player_stats.value)
//	  per (pool, agent, date, category)
func assertDailyEqualsSumOfPerPlayer(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32) {
	t.Helper()
	rows, err := pgPool.Query(ctx, `
		SELECT
			d.agent_id,
			d.date,
			d.category,
			d.value::TEXT AS daily_value,
			COALESCE(SUM(p.value), 0)::TEXT AS sum_of_per_player
		FROM sim_agent_daily_stats d
		LEFT JOIN sim_agent_daily_player_stats p
			ON p.pool_id = d.pool_id AND p.agent_id = d.agent_id
			AND p.date = d.date AND p.category = d.category
		WHERE d.pool_id = $1 AND d.category <> 'GAA'
		GROUP BY d.agent_id, d.date, d.category, d.value
	`, poolID)
	require.NoError(t, err)
	defer rows.Close()

	checked := 0
	for rows.Next() {
		var agentID int32
		var date pgtype.Date
		var category, dailyValue, sumOfPerPlayer string
		require.NoError(t, rows.Scan(&agentID, &date, &category, &dailyValue, &sumOfPerPlayer))
		assert.Equalf(t, dailyValue, sumOfPerPlayer,
			"INVARIANT: sim_agent_daily_stats.value (%s) != SUM(sim_agent_daily_player_stats.value) (%s) for agent=%d date=%s category=%s",
			dailyValue, sumOfPerPlayer, agentID, date.Time.Format("2006-01-02"), category)
		checked++
	}
	require.NoError(t, rows.Err())
	assert.Greater(t, checked, 0, "expected at least one (agent, date, category) row to verify")
}

// assertDraftedTwoPlayers checks that the draft phase actually wrote
// the two scripted picks to sim_rosters. Without this the
// SUM-invariants might trivially hold via empty tables.
func assertDraftedTwoPlayers(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32, expected []int64) {
	t.Helper()
	rows, err := pgPool.Query(ctx, `SELECT player_id FROM sim_rosters WHERE pool_id=$1 ORDER BY player_id`, poolID)
	require.NoError(t, err)
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var pid int64
		require.NoError(t, rows.Scan(&pid))
		got = append(got, pid)
	}
	require.NoError(t, rows.Err())
	assert.ElementsMatch(t, expected, got, "drafted players")
}
