//go:build integration

package simulation

// TestIntegrationSmallSeasonFourTeams — the hermetic full-small-season
// guarantee: 4 agents, 4 synthetic NHL teams, 35 sim days, mocked LLM.
// Proves a complete (small) season simulates cleanly end to end:
// team names → snake draft → day loop → complete, INCLUDING one
// ContinueAsNew rollover (day 30 crosses ContinueAsNewDayThreshold, so
// the progress tracker rehydrates from Redis and the day counter
// continues across executions).
//
// Scripted timeline (all other turns pass):
//
//	day  1: every agent set_lineup — 6 moves filling C/LW/RW/D/D/G
//	        (drafted players land on BN; only active slots score)
//	day 10: Alpha drops its active RW        → scoring window ENDS mid-month
//	day 12: Alpha adds a free-agent RW, contingent-dropping its bench
//	        LW (adds land on BN and the bench is still full — the
//	        day-10 drop vacated the RW slot, not a bench slot)
//	day 13: Alpha activates the added RW     → scoring window STARTS mid-month
//	day 15: Charlie drops its active LW      → on waivers days 15-16 (waiverDays=2)
//	day 16: Alpha AND Bravo claim that LW (contested). Bravo's roster
//	        is full so its claim carries a contingent BN drop; Alpha
//	        has room (7/8 after the day-12 add-with-drop) and claims
//	        without one.
//	day 18: ProcessWaivers resolves — Alpha wins on seeded priority 1,
//	        Bravo's claim is 'lost' and its contingent drop is NOT applied
//	day 20: Delta calls set_lineup for a player NOT on its roster —
//	        validation-rejected, recorded in telemetry, turn still
//	        completes as a pass (proves the feedback loop is non-fatal)
//
// End-state assertions: pool complete, expected per-agent rosters,
// team names, standings rows for all 35 days with the roto-points sum
// invariant, sim_agent_totals equal to a Go-side recomputation from
// the seeded stat lines over the exact ownership windows, waiver
// priority honored, and an exact transaction-type audit census with
// zero error rows.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Scenario constants. IDOffset must not overlap other tests in this
// suite (shared DB). Season days = 35 > ContinueAsNewDayThreshold (30)
// so the test crosses one CAN boundary.
const (
	smallSeasonTestSeason  int32 = 20262027
	smallSeasonTestStart         = "2026-01-01"
	smallSeasonTestDays          = 35
	smallSeasonTestOffset  int64 = 500
	smallSeasonWaiverDays        = 2
	smallSeasonDraftRounds       = 8 // 1C+1LW+1RW+2D+1G+2BN
)

// Scripted event days (1-based sim days).
const (
	dayLineup          = 1
	dayAlphaDrop       = 10
	dayAlphaAdd        = 12
	dayAlphaActivate   = 13
	dayCharlieDrop     = 15
	dayContestedClaims = 16
	dayDeltaRejected   = 20
)

// smallSeasonRosterSpec is the compact roster: 8 slots total.
var smallSeasonRosterSpec = map[RosterSlot]int32{
	SlotC: 1, SlotLW: 1, SlotRW: 1, SlotD: 2, SlotG: 1, SlotBN: 2,
}

// agentPicks is the deterministic draft/lineup assignment for one
// agent: everything comes from the agent's own synthetic NHL team, so
// assignments are disjoint across agents by construction.
type agentPicks struct {
	c, lw, rw, d1, d2 seededPlayer // starters
	bnC, bnLW         seededPlayer // bench
	g                 seededPlayer // starting goalie
}

// picksForAgent maps agent index → its synthetic team's players.
// Position layout per team (SkatersPerTeam=10, cycle C/LW/RW/D):
// j=0 C, 1 LW, 2 RW, 3 D, 4 C, 5 LW, 6 RW, 7 D, 8 C, 9 LW.
// j=6 (second RW) is deliberately left undrafted — it's the free
// agent Alpha adds on day 12.
func picksForAgent(data *smallSeasonData, agentIdx int) agentPicks {
	sk := func(j int) seededPlayer { return data.Skaters[agentIdx*10+j] }
	return agentPicks{
		c: sk(0), lw: sk(1), rw: sk(2), d1: sk(3), d2: sk(7),
		bnC: sk(4), bnLW: sk(5),
		g: data.Goalies[agentIdx*2],
	}
}

func (p agentPicks) draftOrder() []int64 {
	return []int64{p.c.ID, p.lw.ID, p.rw.ID, p.d1.ID, p.d2.ID, p.g.ID, p.bnC.ID, p.bnLW.ID}
}

func (p agentPicks) day1Lineup() []LineupMoveArg {
	return []LineupMoveArg{
		{PlayerID: p.c.ID, Slot: SlotC},
		{PlayerID: p.lw.ID, Slot: SlotLW},
		{PlayerID: p.rw.ID, Slot: SlotRW},
		{PlayerID: p.d1.ID, Slot: SlotD},
		{PlayerID: p.d2.ID, Slot: SlotD},
		{PlayerID: p.g.ID, Slot: SlotG},
	}
}

func TestIntegrationSmallSeasonFourTeams(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	redisClient := connectRedisForTest(t)
	defer redisClient.Close()
	tc, tcCleanup := startTemporalForTest(t)
	defer tcCleanup()

	ctx := context.Background()
	q := sqlcdb.New(pgPool)

	// ------------------------------------------------------------------
	// Seed the synthetic season. Draft demand: 4 agents × 8 rounds = 32;
	// 4 teams × (10 skaters + 2 goalies) = 48 candidates.
	// ------------------------------------------------------------------
	cfg := smallSeasonConfig{
		Season:         smallSeasonTestSeason,
		StartDate:      smallSeasonTestStart,
		Days:           smallSeasonTestDays,
		GamesPerDay:    2, // 4 teams → every team plays every day
		NumTeams:       4,
		SkatersPerTeam: 10,
		GoaliesPerTeam: 2,
		Seed:           20260101,
		IDOffset:       smallSeasonTestOffset,
	}
	const numAgents = 4
	data := seedSmallSeason(t, ctx, pgPool, cfg, numAgents*smallSeasonDraftRounds)

	// ------------------------------------------------------------------
	// Pool + 4 agents. The pool's own start_date/end_date bound the
	// simulated window to exactly the 35 seeded game days — the season
	// row's standings bounds are much wider (seedSmallSeason seeds a
	// year), so the day loop's start AND termination both come from the
	// per-pool window (migration 000016).
	// ------------------------------------------------------------------
	capUSD, err := pgNumericFromFloat(200.0)
	require.NoError(t, err)
	poolRow, err := q.InsertSimPool(ctx, sqlcdb.InsertSimPoolParams{
		Name:                 "integration_small_season",
		Season:               smallSeasonTestSeason,
		Status:               string(PoolStatusDraft),
		NumTeams:             numAgents,
		WaiverDays:           smallSeasonWaiverDays,
		DraftRounds:          smallSeasonDraftRounds,
		MaxLLMCostUsdPerPool: capUSD,
		Categories:           []string{"G", "A", "+/-", "PIM", "PPP", "SOG", "W", "GA", "GAA"},
		RosterC:              smallSeasonRosterSpec[SlotC],
		RosterLW:             smallSeasonRosterSpec[SlotLW],
		RosterRW:             smallSeasonRosterSpec[SlotRW],
		RosterD:              smallSeasonRosterSpec[SlotD],
		RosterG:              smallSeasonRosterSpec[SlotG],
		RosterBN:             smallSeasonRosterSpec[SlotBN],
		StopAfter:            StopAfterNever.String(),
		StartDate:            mustPgDate(smallSeasonTestStart),
		EndDate:              mustPgDate(offsetDate(smallSeasonTestStart, smallSeasonTestDays-1)),
	})
	require.NoError(t, err)
	poolID := poolRow.ID

	agentIDs := make([]int32, numAgents)
	for i := 0; i < numAgents; i++ {
		a, err := q.InsertSimAgent(ctx, sqlcdb.InsertSimAgentParams{
			PoolID:   poolID,
			Provider: "anthropic",
			Model:    "claude-haiku-4-5",
			Strategy: "balanced",
		})
		require.NoError(t, err)
		agentIDs[i] = a.ID
	}
	alphaID, bravoID, charlieID, deltaID := agentIDs[0], agentIDs[1], agentIDs[2], agentIDs[3]

	// Waiver priorities: production never seeds sim_waiver_priority
	// (see seedWaiverPriority), so the test assigns Alpha the best
	// priority to make the contested claim deterministic.
	for i, id := range agentIDs {
		seedWaiverPriority(t, ctx, pgPool, poolID, id, int32(i+1))
	}

	// ------------------------------------------------------------------
	// Scripts.
	// ------------------------------------------------------------------
	picks := make([]agentPicks, numAgents)
	for i := range picks {
		picks[i] = picksForAgent(data, i)
	}
	alphaFA := data.Skaters[0*10+6] // team 0's undrafted RW — Alpha's day-12 add
	notOnDeltaRoster := data.Skaters[0*10+9]

	dailyFor := func(fill func(daily []*llm.Response)) []*llm.Response {
		daily := make([]*llm.Response, smallSeasonTestDays)
		fill(daily)
		return daily
	}
	teamNames := []string{"Alpha", "Bravo", "Charlie", "Delta"}
	scripts := map[int32]*perAgentScript{
		alphaID: {
			teamName:   "Alpha",
			draftPicks: picks[0].draftOrder(),
			daily: dailyFor(func(d []*llm.Response) {
				d[dayLineup-1] = setLineupMovesResponse(picks[0].day1Lineup(), "Set opening lineup")
				d[dayAlphaDrop-1] = dropPlayerResponse(picks[0].rw.ID, "Drop slumping RW")
				d[dayAlphaAdd-1] = addPlayerResponse(alphaFA.ID, &picks[0].bnLW.ID, "Add FA RW, drop bench LW")
				d[dayAlphaActivate-1] = setLineupResponse(alphaFA.ID, SlotRW, "Activate new RW")
				d[dayContestedClaims-1] = claimPlayerResponse(picks[2].lw.ID, nil, "Claim Charlie's LW")
			}),
		},
		bravoID: {
			teamName:   "Bravo",
			draftPicks: picks[1].draftOrder(),
			daily: dailyFor(func(d []*llm.Response) {
				d[dayLineup-1] = setLineupMovesResponse(picks[1].day1Lineup(), "Set opening lineup")
				d[dayContestedClaims-1] = claimPlayerResponse(picks[2].lw.ID, &picks[1].bnC.ID, "Claim Charlie's LW, drop bench C")
			}),
		},
		charlieID: {
			teamName:   "Charlie",
			draftPicks: picks[2].draftOrder(),
			daily: dailyFor(func(d []*llm.Response) {
				d[dayLineup-1] = setLineupMovesResponse(picks[2].day1Lineup(), "Set opening lineup")
				d[dayCharlieDrop-1] = dropPlayerResponse(picks[2].lw.ID, "Drop LW to shake things up")
			}),
		},
		deltaID: {
			teamName:   "Delta",
			draftPicks: picks[3].draftOrder(),
			daily: dailyFor(func(d []*llm.Response) {
				d[dayLineup-1] = setLineupMovesResponse(picks[3].day1Lineup(), "Set opening lineup")
				// Not on Delta's roster — validation-rejected on purpose.
				d[dayDeltaRejected-1] = setLineupResponse(notOnDeltaRoster.ID, SlotLW, "Confused lineup call")
			}),
		},
	}

	stop := setupIntegrationWorker(t, tc, pgPool, redisClient, perAgentFactory(scripts))
	defer stop()

	// 35 mocked days with 4 agents each — well under the 2-minute
	// runtime target, but leave slack for slow CI disks.
	runWorkflowToCompletion(t, ctx, tc, poolID, 4*time.Minute)

	// On any assertion failure, the turn telemetry is the first thing
	// to read — dump the non-accepted tool calls.
	defer func() {
		if t.Failed() {
			dumpTurnTelemetry(t, ctx, pgPool, poolID)
		}
	}()

	// ------------------------------------------------------------------
	// 1. Pool completed; every agent has its scripted team name.
	// ------------------------------------------------------------------
	assertPoolCompleted(t, ctx, pgPool, poolID)
	for i, id := range agentIDs {
		require.Equal(t, id, lookupAgentIDByTeamName(t, ctx, pgPool, poolID, teamNames[i]),
			"agent %d team name", i)
	}

	// ------------------------------------------------------------------
	// 2. End-state rosters, exact per agent.
	// ------------------------------------------------------------------
	assertRosterExactly(t, ctx, pgPool, poolID, alphaID, map[int64]RosterSlot{
		picks[0].c.ID:   SlotC,
		picks[0].lw.ID:  SlotLW,
		alphaFA.ID:      SlotRW, // added day 12, activated day 13
		picks[0].d1.ID:  SlotD,
		picks[0].d2.ID:  SlotD,
		picks[0].g.ID:   SlotG,
		picks[0].bnC.ID: SlotBN,
		picks[2].lw.ID:  SlotBN, // waiver win lands on BN (bnLW left via the day-12 add's contingent drop)
	})
	assertRosterExactly(t, ctx, pgPool, poolID, bravoID, map[int64]RosterSlot{
		picks[1].c.ID:    SlotC,
		picks[1].lw.ID:   SlotLW,
		picks[1].rw.ID:   SlotRW,
		picks[1].d1.ID:   SlotD,
		picks[1].d2.ID:   SlotD,
		picks[1].g.ID:    SlotG,
		picks[1].bnC.ID:  SlotBN, // lost claim → contingent drop NOT applied
		picks[1].bnLW.ID: SlotBN,
	})
	assertRosterExactly(t, ctx, pgPool, poolID, charlieID, map[int64]RosterSlot{
		picks[2].c.ID:    SlotC,
		picks[2].rw.ID:   SlotRW, // LW slot empty since the day-15 drop
		picks[2].d1.ID:   SlotD,
		picks[2].d2.ID:   SlotD,
		picks[2].g.ID:    SlotG,
		picks[2].bnC.ID:  SlotBN,
		picks[2].bnLW.ID: SlotBN,
	})
	assertRosterExactly(t, ctx, pgPool, poolID, deltaID, map[int64]RosterSlot{
		picks[3].c.ID:    SlotC,
		picks[3].lw.ID:   SlotLW,
		picks[3].rw.ID:   SlotRW,
		picks[3].d1.ID:   SlotD,
		picks[3].d2.ID:   SlotD,
		picks[3].g.ID:    SlotG,
		picks[3].bnC.ID:  SlotBN,
		picks[3].bnLW.ID: SlotBN,
	})

	// ------------------------------------------------------------------
	// 3. Waiver contest: Alpha (priority 1) won, Bravo lost.
	// ------------------------------------------------------------------
	assertWaiverClaimStatus(t, ctx, pgPool, poolID, alphaID, picks[2].lw.ID, "won")
	assertWaiverClaimStatus(t, ctx, pgPool, poolID, bravoID, picks[2].lw.ID, "lost")

	// ------------------------------------------------------------------
	// 4. Standings: rows for every (day, agent), and the roto-points
	// sum invariant — tie-splitting preserves the per-category total
	// N(N+1)/2, so each day sums to categories × 10 = 90.
	// ------------------------------------------------------------------
	assertStandingsRotoSums(t, ctx, pgPool, poolID, numAgents, smallSeasonTestDays, 9)

	// ------------------------------------------------------------------
	// 5. Attribution: sim_agent_totals per counting category equals a
	// Go-side recomputation from the seeded lines over the exact
	// ownership windows (this is what the mid-month add/drop makes
	// meaningful). GAA is excluded — it's stored as components, and
	// the totals-vs-dailies invariant helpers already pin its shape.
	// ------------------------------------------------------------------
	expected := map[int32]map[Category]float64{}
	for i, id := range agentIDs {
		expected[id] = expectedAgentTotals(data, activeWindowsForAgent(picks, alphaFA, i))
	}
	for i, id := range agentIDs {
		assertAgentTotals(t, ctx, pgPool, poolID, id, expected[id], teamNames[i])
	}

	// The generic SUM invariants must hold too (totals = Σ dailies,
	// dailies = Σ per-player).
	assertTotalsEqualSumOfDailies(t, ctx, pgPool, poolID)
	assertDailyEqualsSumOfPerPlayer(t, ctx, pgPool, poolID)

	// ------------------------------------------------------------------
	// 6. Transaction audit census — exactly the scripted actions, the
	// per-turn markers, and zero error rows.
	// ------------------------------------------------------------------
	//   draft_pick:      4 agents × 8 rounds
	//   lineup_set:      4 day-1 lineups + Alpha's day-13 activation
	//   drop:            Alpha day 10, Charlie day 15 (Alpha's day-12
	//                    add-with-drop logs only an `add` row with a
	//                    drop_player_id column, not a separate drop;
	//                    Bravo's lost claim never applies its drop)
	//   add:             Alpha day 12 + Alpha's day-18 waiver win
	//   claim:           Alpha + Bravo on day 16
	//   daily_turn_done: 4 agents × 35 days
	//   pass:            every daily turn with no accepted action —
	//                    140 turns minus 10 action turns (4 lineups,
	//                    drop, add, activate, drop, 2 claims). Delta's
	//                    rejected day-20 call still ends as a pass.
	assertTransactionCensus(t, ctx, pgPool, poolID, map[string]int{
		"draft_pick":      numAgents * smallSeasonDraftRounds,
		"lineup_set":      numAgents + 1,
		"drop":            2,
		"add":             2,
		"claim":           2,
		"daily_turn_done": numAgents * smallSeasonTestDays,
		"pass":            numAgents*smallSeasonTestDays - 10,
		"error":           0,
	})

	// ------------------------------------------------------------------
	// 7. The validation feedback loop: Delta's day-20 set_lineup was
	// recorded as validation_rejected in telemetry, and the turn still
	// completed (it's part of the pass census above).
	// ------------------------------------------------------------------
	var rejected int
	require.NoError(t, pgPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM sim_agent_tool_calls tc
		JOIN sim_agent_turns tn ON tn.id = tc.turn_id
		WHERE tn.pool_id=$1 AND tn.agent_id=$2 AND tn.sim_date=$3::DATE
		  AND tc.outcome='validation_rejected'
	`, poolID, deltaID, offsetDate(smallSeasonTestStart, dayDeltaRejected-1)).Scan(&rejected))
	assert.Equal(t, 1, rejected, "Delta's day-20 lineup call should be validation_rejected in telemetry")
}

// ============================================================================
// Expected-value computation from the scripted timeline
// ============================================================================

// activeWindow is one player's contiguous run of ACTIVE (scoring)
// days, inclusive 1-based sim-day bounds.
type activeWindow struct {
	player   seededPlayer
	from, to int
}

// activeWindowsForAgent encodes the scripted ownership timeline. A
// player scores on day N iff they are in an active slot when
// CollectDayStats runs — mutations happen in ManageRoster BEFORE
// scoring, so a day-10 drop means the last scoring day is 9, and a
// day-13 activation means the first scoring day is 13.
func activeWindowsForAgent(picks []agentPicks, alphaFA seededPlayer, agentIdx int) []activeWindow {
	p := picks[agentIdx]
	windows := []activeWindow{
		{p.c, dayLineup, smallSeasonTestDays},
		{p.lw, dayLineup, smallSeasonTestDays},
		{p.rw, dayLineup, smallSeasonTestDays},
		{p.d1, dayLineup, smallSeasonTestDays},
		{p.d2, dayLineup, smallSeasonTestDays},
		{p.g, dayLineup, smallSeasonTestDays},
	}
	switch agentIdx {
	case 0: // Alpha: RW dropped day 10; FA RW active from day 13.
		windows[2].to = dayAlphaDrop - 1
		windows = append(windows, activeWindow{alphaFA, dayAlphaActivate, smallSeasonTestDays})
	case 2: // Charlie: LW dropped day 15.
		windows[1].to = dayCharlieDrop - 1
	}
	return windows
}

// expectedAgentTotals recomputes the eight counting categories from
// the seeded stat lines over the agent's active windows.
func expectedAgentTotals(data *smallSeasonData, windows []activeWindow) map[Category]float64 {
	totals := map[Category]float64{
		CategoryG: 0, CategoryA: 0, CategoryPM: 0, CategoryPIM: 0,
		CategoryPPP: 0, CategorySOG: 0, CategoryW: 0, CategoryGA: 0,
	}
	for _, w := range windows {
		for day := w.from; day <= w.to; day++ {
			date := offsetDate(data.Config.StartDate, day-1)
			if w.player.Position == sqlcdb.PlayerPositionG {
				// Goalies rotate by day — no line on days the other
				// team goalie played.
				if line, ok := data.GoalieLines[date][w.player.ID]; ok {
					if line.Win {
						totals[CategoryW]++
					}
					totals[CategoryGA] += float64(line.GoalsAgainst)
				}
				continue
			}
			if line, ok := data.SkaterLines[date][w.player.ID]; ok {
				totals[CategoryG] += float64(line.Goals)
				totals[CategoryA] += float64(line.Assists)
				totals[CategoryPM] += float64(line.PlusMinus)
				totals[CategoryPIM] += float64(line.PIM)
				totals[CategoryPPP] += float64(line.PPP)
				totals[CategorySOG] += float64(line.SOG)
			}
		}
	}
	return totals
}

// ============================================================================
// Assertions
// ============================================================================

// assertRosterExactly asserts the agent's sim_rosters rows are
// EXACTLY the given player→slot map (no missing, no extra players).
func assertRosterExactly(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID, agentID int32, want map[int64]RosterSlot) {
	t.Helper()
	rows, err := pgPool.Query(ctx,
		`SELECT player_id, slot FROM sim_rosters WHERE pool_id=$1 AND agent_id=$2`,
		poolID, agentID)
	require.NoError(t, err)
	defer rows.Close()

	got := map[int64]RosterSlot{}
	for rows.Next() {
		var pid int64
		var slot string
		require.NoError(t, rows.Scan(&pid, &slot))
		got[pid] = RosterSlot(slot)
	}
	require.NoError(t, rows.Err())
	assert.Equalf(t, want, got, "agent %d end-state roster", agentID)
}

// assertStandingsRotoSums asserts sim_standings has one row per
// (day, agent, category) for every scoring day, and that each day's
// roto points sum to categories × N(N+1)/2 — the tie-splitting rule
// preserves the per-category positional-points total, so any
// deviation means points were lost or double-awarded.
func assertStandingsRotoSums(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32, numAgents, days, categories int) {
	t.Helper()
	perDayTotal := float64(categories * numAgents * (numAgents + 1) / 2)
	for day := 1; day <= days; day++ {
		date := offsetDate(smallSeasonTestStart, day-1)
		var n int
		var sum float64
		require.NoError(t, pgPool.QueryRow(ctx, `
			SELECT COUNT(*), COALESCE(SUM(roto_points), 0)
			FROM sim_standings WHERE pool_id=$1 AND date=$2::DATE
		`, poolID, date).Scan(&n, &sum))
		assert.Equalf(t, numAgents*categories, n, "standings rows on day %d (%s)", day, date)
		assert.InDeltaf(t, perDayTotal, sum, 0.001, "roto points sum on day %d (%s)", day, date)
	}
}

// assertAgentTotals compares sim_agent_totals counting-category rows
// against the Go-side expected map. GAA is skipped (component-stored).
func assertAgentTotals(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID, agentID int32, expected map[Category]float64, label string) {
	t.Helper()
	rows, err := pgPool.Query(ctx, `
		SELECT category, value FROM sim_agent_totals
		WHERE pool_id=$1 AND agent_id=$2 AND category <> 'GAA'
	`, poolID, agentID)
	require.NoError(t, err)
	defer rows.Close()

	got := map[Category]float64{}
	for rows.Next() {
		var cat string
		var value float64
		require.NoError(t, rows.Scan(&cat, &value))
		got[Category(cat)] = value
	}
	require.NoError(t, rows.Err())

	require.Len(t, got, len(expected), "%s: counting-category totals row count", label)
	for cat, want := range expected {
		assert.InDeltaf(t, want, got[cat], 0.001,
			"%s: total %s should equal the recomputation from seeded lines over ownership windows",
			label, cat)
	}
}

// assertTransactionCensus asserts the pool's sim_transactions type
// counts are EXACTLY the given census (types absent from the map must
// have zero rows).
func assertTransactionCensus(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32, want map[string]int) {
	t.Helper()
	rows, err := pgPool.Query(ctx,
		`SELECT type, COUNT(*) FROM sim_transactions WHERE pool_id=$1 GROUP BY type`, poolID)
	require.NoError(t, err)
	defer rows.Close()

	got := map[string]int{}
	for rows.Next() {
		var typ string
		var n int
		require.NoError(t, rows.Scan(&typ, &n))
		got[typ] = n
	}
	require.NoError(t, rows.Err())

	for typ, n := range want {
		assert.Equalf(t, n, got[typ], "transaction census: type %q", typ)
	}
	for typ, n := range got {
		if _, ok := want[typ]; !ok {
			assert.Failf(t, "unexpected transaction type", "type %q has %d rows but is not in the expected census", typ, n)
		}
	}
}
