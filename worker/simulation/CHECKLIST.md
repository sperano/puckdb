# Hockey Pool Simulator — Implementation Checklist

## Phase 1: Foundation

### 1.1 Database Migration
- [ ] Create migration file `000010_simulation.up.sql`
- [ ] `sim_pools` table
- [ ] `sim_agents` table
- [ ] `sim_rosters` table + partial index on active slots
- [ ] `sim_agent_daily_stats` table (write-once ledger)
- [ ] `sim_agent_totals` table
- [ ] `sim_standings` table
- [ ] `sim_waiver_priority` table (pool_id, agent_id, priority)
- [ ] `sim_waiver_claims` table + pending index
- [ ] `sim_transactions` table + lookup index
- [ ] Down migration `000010_simulation.down.sql`
- [ ] Run migration, verify tables created

### 1.2 Go Types
- [ ] `types.go` — SimPool, SimAgent, SimRoster, SimStanding, SimTransaction structs
- [ ] Pool status constants (draft, running, paused, complete)
- [ ] Transaction type constants (draft_pick, add, drop, claim, lineup_set, pass, error)
- [ ] Waiver claim status constants (pending, won, lost, cancelled)
- [ ] Slot constants (C, LW, RW, D, G, Util, BN, IR)
- [ ] AgentConfig struct (provider, model, strategy, timeout, temperature, api_base)
- [ ] PoolConfig struct (season, categories, roster_positions, draft_rounds, waiver_days, agents)
- [ ] Position-to-slot validation map (which positions fit which slots, F→C/LW/RW)

### 1.3 sqlc Queries
- [ ] Insert/update/get for sim_pools
- [ ] Insert/get/update for sim_agents (by pool, including notes update)
- [ ] Insert/update/delete for sim_rosters (by agent, active-only filter)
- [ ] Upsert for sim_agent_daily_stats
- [ ] Recompute sim_agent_totals from daily stats (counting + GAA)
- [ ] Insert/get for sim_standings (by pool+date, latest date)
- [ ] Insert/get for sim_transactions (with pool/agent/date filters)
- [ ] Get available free agents (players with stats not on any roster, not on waivers)
- [ ] Insert/get waiver priority (by pool, initialize from reverse draft order)
- [ ] Update waiver priority (winner drops to bottom)
- [ ] Insert/get/update waiver claims (by pool, pending filter, by process_date)
- [ ] Get players on waivers (dropped within waiver_days, no pending winning claim)
- [ ] Get player's current team from most recent game stats
- [ ] Get today's games (by date, game_state=FINAL)
- [ ] Get games next 7 days for a team
- [ ] Get team goals-per-game averages up to a date

### 1.4 Scoring Engine
- [ ] `scoring.go` — RankCategory function
- [ ] Lower-is-better handling (GA, GAA)
- [ ] Tie-splitting (Yahoo rule: tied teams share points)
- [ ] GAA from components (SUM(ga)/SUM(toi)*3600)
- [ ] Zero goalie TOI → worst rank
- [ ] RankAllCategories — returns per-agent roto points
- [ ] Total roto points (sum across categories, fractional)
- [ ] `scoring_test.go` — tests for all above cases

## Phase 2: Agent

### 2.1 Agent Interface
- [ ] `agent.go` — Agent struct (wraps llm.Client + config)
- [ ] NewAgent factory — creates llm.Client per provider/model/timeout
- [ ] Modify llm.NewOpenAIClient / NewAnthropicClient to accept timeout parameter
- [ ] Build system prompt (with strategy, rules, F position note)
- [ ] Build draft prompt (positional needs, top players per position, draft info)
- [ ] Build daily prompt (standings with is_you, roster, free agents, schedule, analysis, your_notes)
- [ ] Tool definitions (draft_player, set_lineup, add_player, claim_player, drop_player, update_notes) as llm.Tool structs
- [ ] Parse tool call response into structured actions

### 2.2 Fuzzy Recovery
- [ ] Tool name fuzzy matching (edit distance ≤ 2)
- [ ] Lenient JSON parsing (trailing commas, unquoted keys)
- [ ] Text extraction fallback (regex for player IDs in prose)
- [ ] `agent_test.go` — tests for all recovery paths

### 2.3 Tool Execution
- [ ] Validate add_player (player is free agent — not on waivers, drop_player_id required if roster full)
- [ ] Validate claim_player (player is on waivers, drop_player_id required if roster full)
- [ ] Validate drop_player (player is on agent's roster, dropped player goes on waivers)
- [ ] Validate set_lineup (slot matches player's NHL position, slot count limits)
- [ ] Auto-resolve lineup conflicts (displaced player → BN)
- [ ] Sequential move application (array order, not atomic)
- [ ] Validate update_notes (persist to sim_agents.notes)
- [ ] Process tool calls in order: adds/drops/claims first, then lineup changes, then notes

### 2.4 Draft Logic
- [ ] `draft.go` — Snake draft order generator (N teams, 18 rounds)
- [ ] Available player list: top 10 per unfilled position + top 5 BPA
- [ ] Player ranking: skaters by G+A, goalies by W (from prior/current season)
- [ ] Deterministic fallback picker (fill active slots first, prioritize empty positions)
- [ ] Draft context builder (draft_info, roster, slots_remaining, available_by_position)

## Phase 3: Workflow

### 3.1 Activities
- [ ] `activities.go` — Activity struct with DB + LLM dependencies
- [ ] DraftPickActivity — call agent LLM, fallback on 2x failure, persist pick
- [ ] ManageRosterActivity — build context, call agent, process tool calls or pass
  - [ ] Always returns success (nil error to Temporal)
  - [ ] Per-agent timeout from config
  - [ ] Log pass/error/tool_use_failure distinction in sim_transactions
- [ ] CollectDayStatsActivity — for each agent's active roster:
  - [ ] Join active roster players against game_skater_stats/game_goalie_stats for date
  - [ ] Upsert per-category values into sim_agent_daily_stats
  - [ ] Handle GAA: store goalie_ga and goalie_toi_seconds components
  - [ ] Recompute sim_agent_totals from SUM(daily_stats)
- [ ] ProcessWaiversActivity — resolve waiver claims due today:
  - [ ] Group pending claims by player_id where process_date <= today
  - [ ] Highest waiver priority wins contested players
  - [ ] Execute winner's drop_player_id (if any), add claimed player to BN
  - [ ] Winner drops to bottom of waiver priority list
  - [ ] Mark losing claims as lost, winning as won
  - [ ] Unclaimed waiver players (past process_date, no claims) become free agents
  - [ ] Log all outcomes to sim_transactions
- [ ] UpdateStandingsActivity — call scoring engine, persist to sim_standings

### 3.2 Workflow
- [ ] `workflow.go` — SimPoolWorkflow function
- [ ] SimPoolWorkflowInput struct (poolID, simDate, autoAdvance, dayCount)
- [ ] Query handlers: status, rosters, log
- [ ] Signal handlers: advance, pause, auto_advance
- [ ] Phase 1 (draft): skip if simDate is set (ContinueAsNew resume)
  - [ ] Randomize draft order
  - [ ] Snake draft loop (18 rounds × N teams)
  - [ ] Set status=paused after draft
- [ ] Phase 2 (season loop): on each advance signal
  - [ ] Call ProcessWaiversActivity (resolve claims due today)
  - [ ] Get next game day with FINAL games
  - [ ] Randomize agent processing order
  - [ ] Call ManageRosterActivity per agent
  - [ ] Call CollectDayStatsActivity
  - [ ] Call UpdateStandingsActivity
  - [ ] Update sim_date, increment dayCount
  - [ ] ContinueAsNew if dayCount >= 30
- [ ] Phase 3 (complete): set status=complete when season ends
- [ ] Auto-advance mode: loop advancing until paused or complete
- [ ] Register workflow + activities on puckdb-tasks task queue

## Phase 4: API

### 4.1 GraphQL Schema
- [ ] Add sim types to schema (SimPool, SimAgent, SimRosterEntry, SimStandingEntry, SimTransaction)
- [ ] Add queries: simPool, simPools, simTransactions, simStandingsHistory
- [ ] Add mutations: createSimPool, advanceSimDay, autoAdvanceSim, pauseSimPool
- [ ] Run gqlgen generate

### 4.2 Resolvers
- [ ] simPool resolver — query sim_pools + agents + latest standings
- [ ] simPools resolver — list all pools
- [ ] simTransactions resolver — query with pool/agent/date/limit filters
- [ ] simStandingsHistory resolver — all daily standings for a pool
- [ ] SimAgent.roster resolver — join sim_rosters with player data
- [ ] SimAgent.totalRotoPoints resolver — SUM(roto_points) from latest standings
- [ ] createSimPool mutation — insert pool + agents, start Temporal workflow
- [ ] advanceSimDay mutation — send "advance" signal to workflow
- [ ] autoAdvanceSim mutation — send "auto_advance" signal to workflow
- [ ] pauseSimPool mutation — send "pause" signal to workflow

### 4.3 CLI Commands
- [ ] `puckdb sim create` — create a pool from config JSON/flags
- [ ] `puckdb sim advance <pool-id>` — advance one day
- [ ] `puckdb sim run <pool-id>` — auto-advance until paused/complete
- [ ] `puckdb sim pause <pool-id>` — pause the simulation
- [ ] `puckdb sim status <pool-id>` — show current standings and sim_date

## Final Validation

- [ ] Full test suite passes (`go test ./...`)
- [ ] Build succeeds (`go build -o /tmp/puckdb .`)
- [ ] Create a test pool with 2-3 agents, run through draft
- [ ] Advance 5-10 days, verify stats accumulate correctly
- [ ] Verify standings match expected roto ranking
- [ ] Verify add/drop correctly preserves historical daily stats
- [ ] Verify waiver claims resolve correctly (priority order, winner drops to bottom)
- [ ] Verify dropped players go on waivers and become FA after waiver_days
- [ ] Verify contested waiver claims: highest priority wins, others rejected
- [ ] Verify ContinueAsNew works (advance past 30 days)
- [ ] Verify pause/resume works
- [ ] Verify auto-advance runs to completion
