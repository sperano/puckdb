# Hockey Pool Simulator — Design Plan

## Overview

A Temporal-orchestrated simulation where AI agents (each backed by a different LLM) compete in a fantasy hockey roto pool, replaying a real NHL season day-by-day. Advancement is signal-driven. State is persisted to Postgres. Observable via GraphQL.

## Rules (from Crapettes 2025)

### Scoring: Rotisserie (9 categories)

| Skater (6)         | Goalie (3)            |
| ------------------ | --------------------- |
| G, A, +/-, PIM     | W                     |
| PPP, SOG           | GA (lower is better)  |
|                    | GAA (lower is better) |

Teams are ranked 1→N per category (N teams = N points for 1st, 1 for last). Lower is better for GA and GAA.

**Tie-breaking:** Tied teams split points (Yahoo rule). Total roto points can be fractional.

**Algorithm (reference spec):** sort the N teams by category value (descending for higher-is-better; ascending for GA/GAA where lower is better). The team at sorted position P earns `N + 1 - P` points (so 1st earns N, last earns 1). For a run of K teams sharing the same value, each tied team gets the average of the points their K consecutive positions would have earned individually — equivalently, `N + 1 - P - (K - 1)/2` where P is the first position in the tie group.

**Worked examples (5-team pool, points scale 5→1):**

1. **Two teams tied for 3rd, higher-is-better (G).** Values: 50, 45, 30, 30, 20. Sorted positions 1–5; positions 3 and 4 share value 30. Tied pair earns `(3 + 2) / 2 = 2.5` each. Final points: `5, 4, 2.5, 2.5, 1`. (Total = 15, matches `1+2+3+4+5`.)

2. **Three teams tied for 2nd, higher-is-better (PPP).** Values: 25, 18, 18, 18, 10. Positions 2, 3, 4 share value 18. Tied trio earns `(4 + 3 + 2) / 3 = 3` each. Final points: `5, 3, 3, 3, 1`. (Total = 15.)

3. **All five tied — day 1 of the sim, no games yet.** Values: 0, 0, 0, 0, 0. All five share position 1. Each earns `(5 + 4 + 3 + 2 + 1) / 5 = 3`. Final points: `3, 3, 3, 3, 3`. (Total = 15.) This is the steady state until the first game day produces non-zero category values.

4. **Two teams tied for 1st in a lower-is-better category (GA).** Values: 20, 20, 30, 35, 40 (lower = better). Sorted ascending, positions 1 and 2 share value 20. Tied pair earns `(5 + 4) / 2 = 4.5` each. Final points: `4.5, 4.5, 3, 2, 1`. (Total = 15.)

5. **GAA edge case — agent with zero goalie TOI.** GAA is undefined for an agent who never had an active goalie. Per "Rate stats" below, that agent is assigned the worst rank in GAA — i.e., they sort last in the ascending order (position N) and receive 1 point regardless of other agents' values. If multiple agents have zero TOI, they tie at the bottom and split the lowest positions.

**Rate stats (GAA):** GAA is computed from cumulative components: `(total_goals_against / total_toi_seconds) * 3600`. Never summed per-game. If an agent has zero goalie TOI for the season, they receive the worst rank in GAA.

### Category Data Sources

| Category | Source Table       | Column(s)                                    |
| -------- | ------------------ | -------------------------------------------- |
| G        | game_skater_stats  | `goals`                                      |
| A        | game_skater_stats  | `assists`                                    |
| +/-      | game_skater_stats  | `plus_minus`                                 |
| PIM      | game_skater_stats  | `penalty_minutes`                            |
| PPP      | game_skater_stats  | `power_play_points`                          |
| SOG      | game_skater_stats  | `shots_on_goal`                              |
| W        | game_goalie_stats  | `decision = 'W'` (see "Goalie decision semantics" below) |
| GA       | game_goalie_stats  | `goals_against`                              |
| GAA      | game_goalie_stats  | `goals_against / toi_seconds * 3600` (derived) |

**Goalie decision semantics.** The `game_goalie_stats.decision` column is a Postgres ENUM with exactly five possible states (verified against migration `000001_core_schema.up.sql:23` and `sqlcdb/models.go:208`):

| Value | Meaning | Counts toward W? | Counts toward GA / TOI? |
|---|---|---|---|
| `'W'` | Win | **Yes** | Yes |
| `'L'` | Regulation loss | No | Yes |
| `'OTL'` | Overtime / shootout loss | No | Yes |
| `'T'` | Tie (pre-2005-06 lockout era only; never appears in modern sims) | No | Yes |
| `NULL` | Backup goalie or pulled mid-game; no decision awarded | No | Yes |

The W category increments **only when `decision = 'W'`** — never on `L`, `OTL`, `T`, or NULL. GA and TOI accumulate from every row a goalie has, including NULL-decision rows (a pulled starter still let in goals and used TOI before being pulled).

**Two active goalies in the same game** (rare: starter pulled, backup finishes): the box score has two `game_goalie_stats` rows, one per goalie, each with their own `goals_against` and `toi_seconds`. Both contribute to GA and TOI naturally because the per-player attribution table (`sim_agent_daily_player_stats` from H1 fix) writes one row per player per category. If both goalies are on the same agent's active roster slots that day, the agent gets both rows' contributions; on different agents, each agent gets their own goalie's row. This is handled implicitly by the existing schema design — no special case needed.

**Shootout losses** are encoded as `decision = 'OTL'` (the NHL API returns `OTL`; the inbound parser also accepts `'O'` as an alias but the canonical stored value is `'OTL'`). Per the table above, OTL goalies score zero W but their GA + TOI still accumulate.

### Stat Accumulation

A player earns stats for an agent only when ALL of these are true:
1. The player is in an **active roster slot** (not BN/IR)
2. The player has a row in `game_skater_stats` or `game_goalie_stats` for a game on that date (i.e., they actually played — not a healthy scratch, not a team off day)

Players whose team has a day off, or who are healthy scratches, contribute nothing regardless of slot.

### Roster Slots

| Active (12)                        | Inactive (9)  |
| ---------------------------------- | ------------- |
| 2C, 2LW, 2RW, 3D, 1Util, 2G      | 6BN, 3IR      |

**Util slot:** Accepts any skater (C, LW, RW, D). Does not accept goalies.

**IR slots (simplified for V1):** Any player can be placed on IR at any time. No injury designation required. This is a known simplification vs Yahoo rules, where IR requires an active injury status. Future improvement: check NHL injury reports for IR eligibility.

**Active slots are not required to be filled.** An agent may intentionally leave any active slot empty — including all of them — and bench every player. This is a valid "punting" strategy: leaving the G slot empty scores 0 in W/GA/GAA but frees roster spots for skaters where the agent wants to dominate. The simulator does not enforce a minimum-active-slot count. Stat accrual already encodes the consequence: only active-slot players who appear in a boxscore earn category points (per Stat Accumulation rules), so an empty slot simply earns nothing for that day.

### Draft

Snake draft. 18 rounds x N teams. Order is randomized at pool creation.

**Post-draft roster:** 18 players drafted into 21 available slots. Agents start with 3 empty slots (typically BN) that they can fill via free agency during the season. `roster_full` is based on actual player count vs total slots (21), not draft round count.

### Transactions

Free agent add/drop via waiver claims. No trades (for now). No transaction limits per day/week (V1 simplification — consider adding `max_adds_per_week` in the future).

**add_player / claim_player capacity:** Acquired players always land in BN. The roster after removing `drop_player_id` and adding the player to BN must keep BN within its limit (`checkAcquisitionCapacity` in `validate.go`); total roster size is not the test, since IR or active vacancies cannot hold a bench add. With BN full, the drop must therefore be a BN player: an active-slot or IR drop is rejected (`ErrDropLeavesBenchFull`), and a missing drop with `ErrRosterFullNeedsDrop`. Waiver resolution re-applies the same check on the process date against the pool's per-slot limits. A rejected action is logged; the agent does not get a retry.

### Free Agents vs Waivers

Two separate player pools, modeled after Yahoo:

**Free agents** — players who have never been rostered in this pool (or were dropped more than `waiver_days` ago). Picked up instantly via `add_player`. First-processed agent wins (randomized daily order).

**Waivers** — players recently dropped by a pool team. When a player is dropped, they go on waivers for `waiver_days` (configurable, default: 2, matching crapettes). During this window, any agent can file a **waiver claim** via `claim_player`. When the window closes, highest waiver priority wins.

### Waiver System

**Waiver priority:** Starts as reverse draft order (last draft pick = highest priority). When an agent successfully wins a waiver claim, they drop to the bottom of the priority list.

The first `ProcessWaivers` of a pool (season day 1, after the draft) creates one `sim_waiver_priority` row per agent from `sim_agents.draft_position` (`InitSimWaiverPriority`, a no-op once the pool has rows), and every run checks that the rows rank each agent exactly once as 1..N before resolving anything. A pool without a recorded draft order fails resolution rather than falling back to claim order.

**Per-pool serialization:** `ProcessWaivers` runs as one transaction that first locks the pool row (`LockSimPool`, `FOR NO KEY UPDATE`), then reads and locks the pending claims and the priority rows. An overlapping attempt (a Temporal retry after a start-to-close timeout while the first attempt is still running) waits on that lock and then sees the first attempt's committed result. Claim status changes only from `pending` (`ResolveSimWaiverClaim`); a claim found already resolved fails the transaction instead of being rewritten.

**Waiver claim flow:**
1. Agent A drops Player X on day 5 → Player X goes on waivers until day 7
2. During days 5-7, agents can call `claim_player(player_id, drop_player_id?)` to file a claim
3. On day 7, before agent turns, claims are processed:
   - Group pending claims by player_id
   - For each contested player: highest waiver priority wins, all other claims are rejected
   - Winner's `drop_player_id` is executed (dropped from roster)
   - Claimed player is added to winner's roster in BN slot
   - Winner drops to bottom of waiver priority list
   - Log all outcomes (won/lost) to sim_transactions
4. If no one claims Player X, they become a free agent after day 7

**The agent cannot slot a pending-claim player into the lineup** until the claim clears. The player appears on the roster only after processing.

**Drop is instant** — the player is immediately removed from the agent's roster and placed on waivers.

### Player Pool Summary

A player's status in the sim:

| Status | How to acquire | Resolution |
| ------ | -------------- | ---------- |
| Free agent (never owned, or cleared waivers) | `add_player` | Instant, first-processed agent wins |
| On waivers (recently dropped) | `claim_player` | After `waiver_days`, highest priority wins |
| Rostered | N/A | Already owned |
| Pending claim target | `claim_player` | Can still file competing claims |

### Free Agent Pool

A player is a free agent if:
- They have at least one `game_skater_stats` or `game_goalie_stats` row in the current season up to the sim's current date (proves they are active in the NHL)
- They are not on any agent's roster in this pool
- They are not currently on waivers (dropped within the last `waiver_days`)
- They are not the target of any pending waiver claim

For the draft, the pool is all players with stats in the season. Mid-season callups become available once they appear in a boxscore.

**Query shape** (sqlc-generated; runs once per `(pool_id, sim_date)`, shared across all agents that day):

```sql
WITH active_players AS (
    SELECT DISTINCT gss.player_id
    FROM game_skater_stats gss
    JOIN games g ON gss.game_id = g.id
    WHERE g.season = $1 AND g.game_type = 2 AND g.game_date <= $2
    UNION
    SELECT DISTINCT ggs.player_id
    FROM game_goalie_stats ggs
    JOIN games g ON ggs.game_id = g.id
    WHERE g.season = $1 AND g.game_type = 2 AND g.game_date <= $2
)
SELECT player_id FROM active_players
EXCEPT SELECT player_id FROM sim_rosters WHERE pool_id = $3
EXCEPT SELECT player_id FROM sim_waiver_claims WHERE pool_id = $3 AND status = 'pending'
EXCEPT SELECT player_id FROM sim_transactions
       WHERE pool_id = $3 AND type = 'drop' AND date > $2 - $4 * INTERVAL '1 day'  -- $4 = waiver_days
;
```

**Performance characterization.** Bounded scans, not whole-table scans:
- `games` filter uses the existing `idx_games_season_game_type` index → returns ~1,230 game rows for a full season.
- `game_skater_stats` JOIN uses the PK `(game_id, player_id)` for range scans → ~49K rows total per season (1,230 games × ~40 skaters), not the 2.26M table-wide count.
- `game_goalie_stats` similarly returns ~7K rows per season.
- The three EXCEPTs hit small per-pool tables (sim_rosters ≤ 105 rows for 5 agents × 21 slots; sim_waiver_claims pending is tiny).
- Hash-aggregate for DISTINCT runs over ~56K rows → ~800 distinct player_ids.

Expected plan cost: 50–200ms wall-time per call. Verify with `EXPLAIN ANALYZE` during implementation; if it's worse than expected, the most likely culprit is missing stats for the planner — `ANALYZE games; ANALYZE game_skater_stats;` after the season's data is imported.

**Caching strategy.** No materialized table. The candidate set is computed once per `(pool_id, sim_date)` at the top of the day loop (in a new `BuildFreeAgentPoolActivity` or inlined in the per-day setup) and passed to each agent's `ManageRosterActivity` for the day. This collapses N agent calls per day → 1 DB query per day, removing the reviewer's "every day for every agent" multiplier.

The candidate set (active_players CTE) actually depends only on `(season, sim_date)` and could be shared across multiple concurrent pools at the same sim_date — out of scope for V1 (one pool at a time) but worth not precluding in the activity signature.

### Player Team Assignment

A player's current team is derived from their most recent `game_skater_stats.team_id` or `game_goalie_stats.team_id` as of the sim's current date — not from `players.team_id` which reflects today's real-world value. This correctly handles mid-season trades after the player has played their first game on the new team.

**Known V1 limitation (accepted, not fixed).** The "most recent stats row" rule has no fallback for three boundary cases: (1) sim day 1, before any current-season stats exist; (2) the gap between a mid-season trade date and the player's first game on the new team — typically 1–5 days where the rule still reports the old team; (3) a rookie callup before their first NHL game. In all three, `team` may be NULL or stale, and downstream context fields (`plays_today`, `games_next_7_days`) are correspondingly misleading or unavailable for affected players. For a full season this is a small fraction of player-days and acceptable for V1; properly fixing it would require either a fallback chain through `season_rosters` and `players.team_id` (improves availability but not trade-gap correctness) or a real `player_team_history` materialization (correctness but real data-engineering work).

### Position Eligibility

**Five positions, no multi-eligibility.** The simulator recognizes exactly five `players.position` values: `C`, `LW`, `RW`, `D`, `G`. Verified against the live DB: every NHL-imported player carries one of these (the Yahoo importer at `store/yahoo_player.go:120` already strips Yahoo's redundant `F` shorthand; the NHL API uses `F` only for pre-modern historical data and produces zero rows in the current puckdb dataset). The simulator therefore does **not** add a defensive `F`/NULL filter — the position-eligibility validator simply enforces the 5-value table below. If a non-conforming player ever appears, validation surfaces it as a loud error rather than silently dropping the player. This is a deliberate V1 simplification; future work could add `F` → C/LW/RW mapping or Yahoo-style multi-position eligibility (e.g., `C/LW`).

**Source of truth: `players.position`.** Single canonical value per player, always populated, doesn't change with trades. Chosen over `season_rosters.position` because `season_rosters` may not exist for mid-season callups until they appear on a roster, while the sim needs position data from day 1.

**Slot eligibility table:**

| `players.position` | Eligible active slots | Bench/IR |
|---|---|---|
| `C` | C, Util | BN, IR |
| `LW` | LW, Util | BN, IR |
| `RW` | RW, Util | BN, IR |
| `D` | D, Util | BN, IR |
| `G` | G | BN, IR (not Util) |

The Util slot accepts any skater (C, LW, RW, D); never goalies. Validation in `set_lineup` rejects any move that violates this table.

---

## Configuration

**Season scope.** Each pool's config carries a required `season` field (integer, e.g., `20252026`). Simulations operate on a **single regular season only** — no preseason, no playoffs. Every game-data query in the workflow filters by `game_type = 2` AND `game_state = 'FINAL'`. The configured season is assumed to be fully imported before a sim is started — the FK on `sim_pools.season → seasons(id)` enforces existence at INSERT time.

**Config is immutable after `createSimPool`.** No edit-pool mutation is exposed: pool-level fields (`waiver_days`, `num_teams`, `categories`, `roster_positions`, `draft_rounds`, `season`) and per-agent fields (`provider`, `model`, `strategy`, `timeout_seconds`, `temperature`, `api_base`) are fixed for the lifetime of the sim. This is a deliberate choice: mid-sim swapping of, say, an agent's model would invalidate the sim as a model-vs-model comparison. To change settings, cancel the pool and create a new one. The only state that mutates during the sim is what evolves naturally from agent decisions: rosters (`sim_rosters`), waivers (`sim_waiver_claims`/`sim_waiver_priority`), daily stats / rollups / standings, transactions, and each agent's persistent `notes` field (updated via the `update_notes` tool — see Agent LLM Interface).

```jsonc
{
  "season": 20252026,
  "num_teams": 5,
  "categories": ["G", "A", "+/-", "PIM", "PPP", "SOG", "W", "GA", "GAA"],
  "roster_positions": {
    "C": 2, "LW": 2, "RW": 2, "D": 3, "Util": 1, "G": 2, "BN": 6, "IR": 3
  },
  "waiver_days": 2,
  "draft_rounds": 18,
  "max_llm_cost_usd_per_pool": 200.0,    // safety cap; sim auto-pauses when reached. See "Cost cap" below.
  "agents": [
    {"name": "Claude",  "provider": "anthropic", "model": "claude-sonnet-4-20250514", "strategy": "...", "timeout_seconds": 60},
    {"name": "GPT",     "provider": "openai",    "model": "gpt-4o",               "strategy": "...", "timeout_seconds": 60},
    {"name": "Gemini",  "provider": "openai",    "model": "gemini-2.5-flash",     "strategy": "...", "api_base": "https://generativelanguage.googleapis.com/v1beta/openai", "timeout_seconds": 60},
    {"name": "Llama",   "provider": "ollama",    "model": "llama3.3:70b",         "strategy": "...", "timeout_seconds": 120},
    {"name": "Claude2", "provider": "anthropic",  "model": "claude-haiku-4-5-20251001", "strategy": "...", "timeout_seconds": 60}
  ]
}
```

---

## Database Schema (new migration)

```sql
-- sim_pools: one row per simulation run
CREATE TABLE sim_pools (
    id           SERIAL PRIMARY KEY,
    name         TEXT NOT NULL,
    season       INT NOT NULL REFERENCES seasons(id),
    status       TEXT NOT NULL DEFAULT 'draft',  -- draft|running|paused|complete|cancelled
    sim_date     DATE,                           -- current simulation date (avoids SQL reserved word "current_date")
    config       JSONB NOT NULL,
    total_llm_cost_usd NUMERIC NOT NULL DEFAULT 0,  -- running cumulative LLM spend; checked against config.max_llm_cost_usd_per_pool. See "Cost cap" in Cost Estimate section.
    -- Derived from id: 'sim-pool-{id}'. See "Workflow ID & Lifecycle" in Temporal Workflow section.
    -- Materialized as a generated column so it's set atomically with the INSERT (no race
    -- window between pool-row creation and Temporal start) and remains queryable for ops.
    workflow_id  TEXT GENERATED ALWAYS AS ('sim-pool-' || id::text) STORED,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- sim_agents: one per AI team manager
CREATE TABLE sim_agents (
    id             SERIAL PRIMARY KEY,
    pool_id        INT NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    draft_position INT NOT NULL,
    provider       TEXT NOT NULL,
    model          TEXT NOT NULL,
    strategy       TEXT NOT NULL,
    notes          TEXT NOT NULL DEFAULT ''
                   CHECK (octet_length(notes) <= 50000),  -- agent-managed persistent notes (strategic memory); 50KB cap so a verbose model can't blow up the per-day prompt
    agent_config   JSONB NOT NULL DEFAULT '{}',  -- temperature, api_base, max_tokens, timeout_seconds
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- sim_rosters: current roster state per agent
CREATE TABLE sim_rosters (
    pool_id      INT NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    agent_id     INT NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    player_id    BIGINT NOT NULL REFERENCES players(id),  -- nhl player ID
    slot         TEXT NOT NULL,                   -- roster slot: C/LW/RW/D/G/Util/BN/IR
    acquired_at  DATE NOT NULL,
    acquired_via TEXT NOT NULL DEFAULT 'draft',   -- draft|free_agent
    PRIMARY KEY (pool_id, agent_id, player_id),
    -- Defense-in-depth: a player can only be on one agent's roster per pool.
    -- Without this, a race during waiver processing could place the same
    -- player on multiple rosters and silently corrupt the simulation.
    UNIQUE (pool_id, player_id)
);

CREATE INDEX idx_sim_rosters_active ON sim_rosters(pool_id, slot)
    WHERE slot NOT IN ('BN', 'IR');

-- sim_waiver_priority: tracks waiver claim order per agent
-- Initialized as reverse draft order. Winner of a claim drops to bottom (highest number).
CREATE TABLE sim_waiver_priority (
    pool_id   INT NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    agent_id  INT NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    priority  INT NOT NULL,              -- lower number = higher priority (1 = first pick)
    PRIMARY KEY (pool_id, agent_id)
);

-- sim_waiver_claims: pending and resolved waiver claims
CREATE TABLE sim_waiver_claims (
    id              SERIAL PRIMARY KEY,
    pool_id         INT NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    agent_id        INT NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    player_id       BIGINT NOT NULL REFERENCES players(id),  -- player being claimed
    drop_player_id  BIGINT REFERENCES players(id),            -- player to drop if claim succeeds (NULL if roster not full)
    filed_date      DATE NOT NULL,       -- sim date when claim was filed
    process_date    DATE NOT NULL,       -- sim date when claim will be processed (filed_date + waiver_days)
    status          TEXT NOT NULL DEFAULT 'pending',  -- pending|won|lost|cancelled
    resolved_at     DATE,                -- sim date when resolved
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_sim_waiver_claims_pending ON sim_waiver_claims(pool_id, process_date)
    WHERE status = 'pending';

-- Defense-in-depth: an agent may have at most one pending claim per player.
-- Self-cleaning via the partial predicate: once a claim resolves to
-- won/lost/cancelled it leaves the index, freeing the triple for a future
-- claim. Catches both user-error (file twice) and workflow retry duplication.
CREATE UNIQUE INDEX ux_sim_waiver_claims_pending_one_per_agent_player
    ON sim_waiver_claims (pool_id, agent_id, player_id)
    WHERE status = 'pending';

-- sim_agent_daily_player_stats: per-player attribution layer (source of truth).
-- Each row is one player's contribution in one category on one day, while on
-- the agent's active roster. This is what enables diagnostics like "which
-- player on Claude's roster scored those 3 goals on Nov 15?" — without it,
-- per-player attribution is lost forever once the daily rollup is computed.
-- For GAA: store raw components per goalie per day; season GAA aggregates them.
CREATE TABLE sim_agent_daily_player_stats (
    pool_id            INT NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    agent_id           INT NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    date               DATE NOT NULL,
    player_id          BIGINT NOT NULL REFERENCES players(id),
    category           TEXT NOT NULL,
    value              NUMERIC NOT NULL DEFAULT 0,
    goalie_ga          INT,              -- raw goals against for this day (GAA rows only)
    goalie_toi_seconds INT,              -- raw TOI seconds for this day (GAA rows only)
    PRIMARY KEY (pool_id, agent_id, date, player_id, category)
);

CREATE INDEX idx_sim_agent_daily_player_stats_player
    ON sim_agent_daily_player_stats (pool_id, player_id, date);

-- sim_agent_daily_stats: per-agent rollup derived from sim_agent_daily_player_stats.
-- Kept as a separate table (not a view) so the totals computation reads a small,
-- pre-aggregated dataset. Each day's rollup is computed from the per-player rows
-- written in the same DB transaction. Rerunning a day uses INSERT ... ON CONFLICT
-- DO UPDATE on both tables so a Temporal retry overwrites in place atomically
-- (the row set is deterministic across retries because the day's roster is
-- locked before scoring runs — see Day Loop step 4).
-- For GAA: stores raw components per day. Season GAA = SUM(goalie_ga) / SUM(goalie_toi_seconds) * 3600.
CREATE TABLE sim_agent_daily_stats (
    pool_id            INT NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    agent_id           INT NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    date               DATE NOT NULL,
    category           TEXT NOT NULL,
    value              NUMERIC NOT NULL DEFAULT 0,
    goalie_ga          INT,              -- raw goals against for this day (GAA rows only)
    goalie_toi_seconds INT,              -- raw TOI seconds for this day (GAA rows only)
    PRIMARY KEY (pool_id, agent_id, date, category)
);

-- sim_agent_totals: materialized totals per agent (recomputed from daily_stats each day)
-- For counting stats: SUM(daily value). For GAA: computed from SUM(goalie_ga)/SUM(goalie_toi_seconds)*3600.
CREATE TABLE sim_agent_totals (
    pool_id            INT NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    agent_id           INT NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    category           TEXT NOT NULL,
    value              NUMERIC NOT NULL DEFAULT 0,
    goalie_ga          INT,
    goalie_toi_seconds INT,
    PRIMARY KEY (pool_id, agent_id, category)
);

-- sim_standings: daily roto standings snapshot
CREATE TABLE sim_standings (
    pool_id     INT NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    date        DATE NOT NULL,
    agent_id    INT NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    category    TEXT NOT NULL,
    value       NUMERIC NOT NULL,
    roto_points NUMERIC NOT NULL,       -- fractional: supports tie-splitting (e.g., 3.5)
    PRIMARY KEY (pool_id, date, agent_id, category)
);

-- sim_transactions: full log of every agent decision with reasoning.
-- Fully typed columns — no JSONB. Type-specific fields are nullable;
-- application-layer construction populates the appropriate subset per type.
-- The only list-shaped payload (lineup_set's moves array) lives in
-- sim_lineup_moves below.
CREATE TABLE sim_transactions (
    id             SERIAL PRIMARY KEY,
    pool_id        INT NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    agent_id       INT NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    date           DATE NOT NULL,
    type           TEXT NOT NULL,           -- draft_pick|add|claim|drop|lineup_set|pass|error|cost_cap_reached
    player_id      BIGINT REFERENCES players(id),  -- the primary player for this tx (added/dropped/drafted/claimed); NULL for lineup_set/pass/error/cost_cap_reached
    reasoning      TEXT NOT NULL DEFAULT '',    -- agent's natural-language explanation; populated for all LLM-driven types
    -- draft_pick fields:
    round          INT,
    pick           INT,
    -- add / claim fields (the player getting dropped to make room, if any):
    drop_player_id BIGINT REFERENCES players(id),
    -- error fields:
    error_kind     TEXT,                        -- 'tool_use_failure' | 'llm_error' | 'validation'
    error_detail   TEXT,
    -- cost_cap_reached fields (see L3):
    cost_usd       NUMERIC,                     -- cumulative spend at the moment the cap fired
    cap_usd        NUMERIC,                     -- the configured cap value
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_sim_transactions_lookup ON sim_transactions(pool_id, date, agent_id);

-- sim_lineup_moves: child rows for lineup_set transactions.
-- One row per individual slot move within a single set_lineup tool call.
-- `sequence` preserves the agent-supplied ordering (set_lineup applies moves
-- in array order, not atomically — see "Agent Turn — Single LLM Call").
-- displaced_player_id is non-null when the destination slot was already
-- occupied and the simulator auto-resolved by moving the displaced player to BN.
CREATE TABLE sim_lineup_moves (
    transaction_id      INT  NOT NULL REFERENCES sim_transactions(id) ON DELETE CASCADE,
    sequence            INT  NOT NULL,             -- 0-based index within the set_lineup call
    player_id           BIGINT NOT NULL REFERENCES players(id),
    from_slot           TEXT NOT NULL,
    to_slot             TEXT NOT NULL,
    displaced_player_id BIGINT REFERENCES players(id),  -- NULL if no displacement
    PRIMARY KEY (transaction_id, sequence)
);
```

**Per-type column population (the contract for the GraphQL resolver and analytics):**

| `type`              | populated columns                                                    | child rows in sim_lineup_moves |
|---------------------|----------------------------------------------------------------------|--------------------------------|
| `draft_pick`        | player_id, reasoning, round, pick                                    | —                              |
| `add`               | player_id, reasoning, drop_player_id (NULL without a replacement drop; the dropped player also gets its own `drop` row, which is the waiver marker) | —                              |
| `claim`             | player_id, reasoning, drop_player_id (NULL if roster wasn't full)    | —                              |
| `drop`              | player_id, reasoning                                                 | —                              |
| `lineup_set`        | reasoning                                                            | one row per move in agent's array |
| `pass`              | reasoning                                                            | —                              |
| `error`             | reasoning (best-effort), error_kind, error_detail                    | —                              |
| `cost_cap_reached`  | cost_usd, cap_usd                                                    | —                              |

Display strings (player names, etc.) are produced by the GraphQL resolver via JOINs to `players` — never stored denormalized in the transaction row.

### Totals Computation

`sim_agent_totals` is recomputed from `sim_agent_daily_stats` after each day:

```sql
-- Counting stats (G, A, +/-, PIM, PPP, SOG, W, GA):
INSERT INTO sim_agent_totals (pool_id, agent_id, category, value)
SELECT pool_id, agent_id, category, SUM(value)
FROM sim_agent_daily_stats
WHERE pool_id = $1
GROUP BY pool_id, agent_id, category
ON CONFLICT (pool_id, agent_id, category)
DO UPDATE SET value = EXCLUDED.value;

-- GAA: computed from raw components
INSERT INTO sim_agent_totals (pool_id, agent_id, category, value, goalie_ga, goalie_toi_seconds)
SELECT pool_id, agent_id, 'GAA',
    CASE WHEN SUM(goalie_toi_seconds) > 0
         THEN (SUM(goalie_ga)::NUMERIC / SUM(goalie_toi_seconds)) * 3600
         ELSE 0 END,
    SUM(goalie_ga),
    SUM(goalie_toi_seconds)
FROM sim_agent_daily_stats
WHERE pool_id = $1 AND category = 'GAA'
GROUP BY pool_id, agent_id
ON CONFLICT (pool_id, agent_id, category)
DO UPDATE SET value = EXCLUDED.value,
             goalie_ga = EXCLUDED.goalie_ga,
             goalie_toi_seconds = EXCLUDED.goalie_toi_seconds;
```

`totalRotoPoints` for the GraphQL API is computed at query time: `SUM(roto_points)` from `sim_standings` for the latest date, grouped by agent_id.

---

## Temporal Workflow

### Workflow ID & Lifecycle

**ID format.** Every pool's workflow has a deterministic ID: `sim-pool-{pool_id}`. Computed application-side from `sim_pools.id` and also materialized in the `workflow_id` generated column. The format is greppable in Temporal UI/CLI and reversible to a pool ID for ops.

**Start (in `createSimPool` mutation).** A single DB transaction inserts the `sim_pools` row (the generated `workflow_id` populates atomically), then `client.ExecuteWorkflow` is called with `ID: sim-pool-{pool_id}` and `WorkflowIDReusePolicy: WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE`. The reuse policy makes start idempotent: if the API call fails between INSERT and `ExecuteWorkflow` and the client retries, Temporal returns `WorkflowExecutionAlreadyStarted` cleanly — no orphaned pool rows, no double-started workflows. There is no race window because the workflow_id is a function of the row's primary key, not a separately-assigned field.

**Cancel (via `cancelSimPool` mutation).** Cooperative cancel via Temporal's `RequestCancelWorkflow(ctx, workflowID, "")`. The workflow's current activity finishes, the workflow handles `workflow.Context` cancellation, writes `sim_pools.status='cancelled'`, and exits. Distinct from `TerminateWorkflow` (forced kill, reserved for the truly stuck case as an ops escape hatch — not a user-facing mutation).

```
SimPoolWorkflow(input: SimPoolWorkflowInput)
│
│  Input struct:
│    poolID       int
│    simDate      *Date     -- nil on first run, set on ContinueAsNew
│    autoAdvance  bool      -- recovered from signal state, carried across CAN
│    paused       bool      -- carried across CAN; if true, Phase 2 waits for
│                              "advance"/"auto_advance" before processing the
│                              next day. Required so a pause that lands just
│                              before CAN survives the boundary.
│    dayCount     int       -- reset to 0 on each ContinueAsNew
│
├── Queries (split for payload size — see "Query split rationale" below):
│   ├── "summary"               → {status, simDate, dayCount, paused, autoAdvance, totalLlmCostUsd}
│   ├── "latest_standings"      → standings rows for current sim_date only (~45 rows)
│   ├── "standings_for_date"    → standings rows for a specific date (input: date)
│   ├── "rosters"               → all agent rosters
│   └── "log"                   → recent transactions (input: limit, agent_id?)
│
├── Signals:
│   ├── "advance"      → process exactly ONE game day; valid in any state.
│   ├── "pause"        → sets paused=true AND autoAdvance=false (halts the auto loop).
│   └── "auto_advance" → sets autoAdvance=true AND paused=false (resumes the auto loop).
│
│   Signal interaction matrix (state machine):
│   - pause when running:        sets paused, clears autoAdvance. In-flight day completes
│                                  (idempotency requires it); next iteration honors paused.
│   - pause when already paused: no-op.
│   - advance when paused:       processes one day, leaves paused=true after (single-step
│                                  recovery; does NOT re-enable autoAdvance).
│   - advance when running:      queues; processed in order with other advances.
│   - auto_advance when paused:  clears paused, sets autoAdvance, resumes auto loop.
│   - auto_advance when running: idempotent (already auto-advancing) — but ensures
│                                  paused=false in case it leaked true somehow.
│
│   Why pause clears autoAdvance: lets "pause to inspect, then advance manually" stay
│   single-step. Without this, a stale autoAdvance flag would silently re-trigger
│   auto mode the moment the workflow reached a non-paused state.
│
│   Serial-processing guarantee: Temporal workflow goroutines are single-threaded
│   for replay determinism (see "Determinism & Replay"). Signal handlers MUST NOT
│   spawn goroutines, MUST NOT call activities, and MUST NOT block. They mutate
│   flags only. The day-loop consumes the channel one signal at a time, so
│   day-ordering cannot be corrupted by concurrent signals.
│
│   Phase 1 signal handling: only "pause" is meaningful during the draft
│   (pre-arms the paused flag — Phase 2 honors it on entry, no mid-draft
│   halt). "advance"/"auto_advance" are dropped silently. Mid-draft abort
│   is the cancelSimPool mutation's job, not a signal.
│
├── ContinueAsNew: every 30 game days to keep history bounded
│   (carries forward full SimPoolWorkflowInput with updated simDate, paused,
│    autoAdvance, and dayCount=0; see "ContinueAsNew + Signal Drain" below
│    for the drain protocol that prevents lost signals at the CAN boundary)
│
├── Phase 1: DRAFT (skipped if simDate is set, i.e., ContinueAsNew resume)
│   ├── Randomize draft order via workflow.NewRandom(ctx).Shuffle (deterministic on replay)
│   ├── shared.InitTracker (or LoadReportTracker on CAN resume) — see "Progress tracking"
│   ├── tracker.StartGroup(ctx, 0)  -- "Draft"
│   ├── For each pick (18 rounds x N teams, snake):
│   │   └── DraftPickActivity(agentID, availablePlayers, existingRoster)
│   │       → agent LLM picks a player
│   │       → fallback: if LLM fails 2x, pick best available by G+A (skaters) or W (goalies)
│   │       → persist to sim_rosters + sim_transactions
│   │       → tracker.IncrementBar(ctx, 0, 0)
│   ├── tracker.CompleteGroup(ctx, 0, "Draft complete in <elapsed>")
│   └── Set status = "paused" (wait for first "advance" signal)
│
├── Phase 2: SEASON (loop over CALENDAR days in the regular season — no-game days included)
│   ├── On entry (including CAN resume): if input.paused, block on
│   │   "advance"/"auto_advance" signal before processing next day. This is
│   │   what makes the paused flag load-bearing across the CAN boundary.
│   ├── tracker.StartGroup(ctx, 1)  -- "Season"  (no-op on CAN resume, group already started)
│   └── On each "advance" signal (or auto-advance loop):
│       ├── Advance simDate by one calendar day (no skipping — see "Off-day handling" below)
│       ├── ProcessWaiversActivity(date)
│       │   → runs every calendar day (waiver clock is calendar days, not game days)
│       │   → resolve claims with process_date <= today
│       │   → highest priority wins contested players
│       │   → winner drops to bottom of priority list
│       │   → unclaimed players become free agents
│       ├── BuildFreeAgentPoolActivity(pool_id, date)
│       │   → ONE DB query per day (not per agent) — see "Free Agent Pool" > Query shape
│       │   → result is shared across all 5 agents' ManageRosterActivity calls below
│       ├── Randomize agent processing order for this day via workflow.NewRandom(ctx).Shuffle
│       ├── For each agent (in randomized order):
│       │   └── ManageRosterActivity(agentID, date, faPool, context)  -- runs every calendar day
│       │       → agent sees: most-recent game day's results, standings, roster, free
│       │         agents, schedule (incl. plays_today=false on no-game days), waiver results
│       │       → agent returns: lineup changes + add/drop/claim (or pass)
│       │       → persist changes
│       ├── If date has FINAL regular-season games (game_type=2 AND game_state=FINAL):
│       │   ├── CollectDayStatsActivity(date)
│       │   │   → query game_skater_stats + game_goalie_stats for this date
│       │   │   → compute stats earned by each agent's ACTIVE roster only
│       │   │   → INSERT per-player rows into sim_agent_daily_player_stats (source of truth)
│       │   │   → aggregate UP into sim_agent_daily_stats (per-agent rollup, same tx)
│       │   │   → recompute sim_agent_totals from SUM(daily_stats)
│       │   │   → idempotency: INSERT ... ON CONFLICT DO UPDATE on both tables (upsert in place)
│       │   └── UpdateStandingsActivity(date)
│       │       → rank all agents per category with tie-splitting
│       │       → persist to sim_standings
│       │  (No-game days: both activities skipped — no stats to collect, standings unchanged)
│       ├── Set sim_date = date, dayCount++
│       ├── tracker.IncrementBar(ctx, 1, 0)  -- season-bar tick (every calendar day)
│       └── If dayCount >= 30: drain pending signals (see below),
│           then ContinueAsNew(poolID, simDate, autoAdvance, paused, dayCount=0)
│
└── Phase 3: COMPLETE
    ├── tracker.CompleteGroup(ctx, 1, "Season complete in <elapsed>")
    ├── (optional) tracker.report.Message = "Final standings: <leader> wins"
    └── Final standings, set status = "complete"
```

### Progress tracking

Reuses **`worker/shared.ProgressReport` / `ReportTracker`** — the standard puckdb pattern every long-running workflow follows (FetchSeasonWorkflow, FetchEdgeWorkflow, etc.; see `worker/shared/progress.go` and the "Progress Tracking Pattern" section of the puckdb CLAUDE.md). No new types or query plumbing.

**Report shape:**

- **Group 0 — "Draft":** one bar with `Total = num_teams * draft_rounds` (e.g., 5 × 18 = 90). Incremented after each `DraftPickActivity` completes.
- **Group 1 — "Season":** one bar with `Total = season_calendar_days` (computed at workflow start from `MIN(game_date)..MAX(game_date)` for the configured season's regular-season games — typically ~185). Incremented at the bottom of each day-loop iteration after `UpdateStandingsActivity` (or after the day's no-op skip on off-days).

A final-standings string can be published via `report.Message` on Phase 3 entry rather than allocating a third group; the existing `CompleteGroup(ctx, 1, "Season complete in X")` call carries the elapsed-time message naturally.

**Lifecycle (matches the existing pattern):**

```go
// First run (Phase 1 entry):
tracker, err := shared.InitTracker(ctx, simulation.NewProgressReport(numAgents, draftRounds, totalDays))

// ContinueAsNew resume:
tracker, err := shared.LoadReportTracker(ctx)  // rehydrates from Redis; bar continues at the dayCount it left off
```

`LoadReportTracker(ctx)` is the load-bearing detail that makes progress survive CAN. Without it, every 30-day boundary would reset the season bar to 0% and the UI would jump back to the start. With it, the bar smoothly continues because the report is persisted to Redis at every group/bar mutation by `tracker.Save(ctx)`.

**GraphQL exposure:** add `simPoolProgress(poolId: Int!): ProgressReport` to `### Queries` below — mirrors the `<workflow>Progress` convention (per puckdb CLAUDE.md GraphQL API section). The resolver reads the report from Redis at `progress:sim-pool-{pool_id}` (the standard tracker key) — no Temporal round-trip needed for the polled-often progress UI.

### Query split rationale

The original "status" query bundled `{status, simDate, standings}`. With 5 agents × 9 categories × 180 days that's ~8,100 standings rows by season-end — JSON-encoded ≈600KB, uncomfortably close to Temporal's default ~2MB query response limit, and wasteful when most callers only want the small summary. Split:

- **`summary`** — small, polled often (every few seconds by the GraphQL UI to update the day counter and pause-state indicator). Returns `{status, simDate, dayCount, paused, autoAdvance, totalLlmCostUsd}` — fixed-size, well under 1KB.
- **`latest_standings`** — current-day standings only (~45 rows, ~3KB). What the standings UI panel renders by default.
- **`standings_for_date(date)`** — same shape as latest_standings, for any historical date. Used when the user scrubs through history.
- **`rosters`** — unchanged (~105 rows max for 5 agents × 21 slots).
- **`log(limit, agent_id?)`** — recent sim_transactions, paginated.

The L2 / config-immutability decision implies most callers really want `summary` — the fixed config doesn't need re-fetching, only the live state does. Bundling `summary` with standings would have hidden that asymmetry and made every poll pay the standings cost.

### Determinism & Replay

Temporal workflows are replayed from event history every time the worker restarts or fails over. For replay to converge on the same state the original execution reached, **every operation in the workflow goroutine must be deterministic** — given the same event history, the workflow code must produce the same Commands. Three sources of non-determinism would break this and silently corrupt the simulation (or throw `non-determinism error` and fail the workflow execution outright):

**1. Randomness.** The plan calls for two randomized choices:
- "Randomize draft order" — once, at the start of Phase 1.
- "Randomize agent processing order for this day" — every day, in Phase 2.

Both MUST use Temporal's deterministic PRNG, NOT `math/rand` or `crypto/rand`:

```go
// Pseudocode — pattern only, not final code.
prng := workflow.NewRandom(ctx)              // seeded deterministically per workflow execution
prng.Shuffle(len(agents), func(i, j int) {   // safe to use across the day loop
    agents[i], agents[j] = agents[j], agents[i]
})
```

`workflow.NewRandom(ctx)` returns a `*rand.Rand` whose seed comes from Temporal's deterministic side-effect API. Replay produces the same sequence. The PRNG resets on ContinueAsNew (new workflow → fresh deterministic seed); this is not a bug — across-CAN order changes are acceptable because each workflow execution is internally consistent.

For one-shot non-deterministic captures that aren't randomness (e.g., a UUID, an external token), use `workflow.SideEffect(ctx, fn)`, which records the result in event history so replay reuses it.

**2. Wall-clock time.** Use `workflow.Now(ctx)`, NOT `time.Now()`, anywhere wall-clock time is consulted in the workflow goroutine — for logging, timeout computation, deadline arithmetic, anything. The simulation's tracked `sim_date` is an in-band value (input field or activity result) and is already deterministic; the wall-clock risk is in incidental code (e.g., "log started at X").

**3. Direct I/O.** No DB calls, no HTTP, no filesystem reads in the workflow goroutine. All side-effecting reads/writes must be in **activities** — activity results are captured in event history and replayed verbatim. This invariant is already implicit in the plan (every Postgres touch is in `*Activity`), but it's worth naming so a future contributor doesn't add a "quick" `db.QueryRow(...)` to the workflow function.

**Map of where each primitive applies in this workflow:**

| Site | Primitive |
|------|-----------|
| Phase 1 draft-order shuffle | `workflow.NewRandom(ctx).Shuffle` |
| Phase 2 per-day agent-order shuffle | reuse same `*rand.Rand` (or call `workflow.NewRandom(ctx)` again per day; both are deterministic) |
| `process_date <= today` comparison | uses `simDate` (input/activity-derived) — already deterministic, no time call needed |
| Any wall-clock logging in workflow code | `workflow.Now(ctx)` |
| Reading game data, writing rosters, etc. | always inside an activity |

### ContinueAsNew + Signal Drain

ContinueAsNew (CAN) is a workflow-completion variant: the current workflow ends, a fresh one starts with the input you pass to `workflow.NewContinueAsNewError(...)`. Two things go subtly wrong if implemented naively:

**1. Signals can be lost at the CAN boundary.** Any signal that arrived but hasn't been handled at the moment CAN is returned does NOT carry over to the new workflow — it's silently dropped. The fix is a drain loop: before returning the CAN error, process every pending signal already on the channel.

```go
// Pseudocode — pattern only, not final code.
for selector.HasPending() {
    selector.Select(ctx)        // pull and dispatch one queued signal
}
return workflow.NewContinueAsNewError(ctx, SimPoolWorkflow, nextInput)
```

The drain is cheap: signal handlers in this workflow are non-blocking (they just mutate flags like `autoAdvance`, `paused`). They never call activities or wait. So draining is bounded by the number of queued signals, not by activity latency.

**2. State that lives only in the workflow goroutine is lost across CAN.** The Go workflow function's local variables disappear when CAN returns; only `nextInput` survives. So every flag that drives behavior on the next workflow's start must be in the input struct:

- `autoAdvance` — already there.
- `paused` — added by this fix. Required for the case where a user pauses just before the CAN trigger fires; without it, the resumed workflow would forget the pause and immediately process the next day on the auto-advance loop.

**On entry, before the day loop, the workflow MUST consult `input.paused`:** if true, block on `advance` or `auto_advance` signal before processing. Otherwise behave as a normal start.

**3. Daycount-trigger interaction with auto-advance.** If `autoAdvance` is true and `dayCount >= 30`, the drain runs, CAN fires, and the new workflow resumes auto-advancing on entry — `paused=false` carries through, the auto-advance loop reattaches. No special handling needed beyond carrying both flags forward.

### Idempotency

All activities must be idempotent. If the worker crashes mid-day and Temporal retries, neither double-counting (stats) nor duplicate LLM spend / divergent agent moves (roster management) may occur.

**Stat activities — `CollectDayStatsActivity`:** Computes that day's stat contribution from the current roster (at lock time), writes per-player rows to `sim_agent_daily_player_stats` (source of truth), and aggregates UP into `sim_agent_daily_stats` (per-agent rollup) — both within the same DB transaction. Both writes use `INSERT ... ON CONFLICT (<pk>) DO UPDATE SET value = EXCLUDED.value, goalie_ga = EXCLUDED.goalie_ga, goalie_toi_seconds = EXCLUDED.goalie_toi_seconds` so a Temporal retry overwrites the day's rows in place. This is correct for retry idempotency because the row set is deterministic across attempts: the day's `sim_rosters` is locked before scoring runs (Day Loop step 4), so the (player_id, category) keys produced by attempt N+1 are identical to attempt N. Totals in `sim_agent_totals` are recomputed as `SUM()` over the rollup. **Caveat:** ON CONFLICT does not clean up rows from a prior attempt that are absent from the new attempt's row set — irrelevant for Temporal retry but a manual rerun (e.g., NHL data correction) would need an explicit cleanup query for the affected day.

This is correct even after roster changes: if an agent drops Player A on day 50, Player A's per-player attribution rows from days 1-49 remain in `sim_agent_daily_player_stats` because those rows were written when Player A was on the active roster. Day 50 onward, Player A is no longer active, so new rows won't include them. The totals correctly reflect the historical ownership, and per-player attribution is preserved for diagnostics ("which player on Claude's roster scored those 3 goals on Nov 15?").

**LLM activities — `ManageRosterActivity` and `DraftPickActivity`:** LLM calls cost real money and produce non-deterministic output, so a Temporal retry after the worker crashed mid-activity must NOT re-invoke the model.

Idempotency key: `(pool_id, agent_id, sim_date)` for `ManageRosterActivity`, `(pool_id, agent_id, draft_pick_number)` for `DraftPickActivity`. The key is also passed to Temporal as the activity ID for an extra layer of dedup at the framework level.

Recipe (executed inside the activity, in order):

1. **Pre-flight check.** Open a short DB read: query `sim_transactions` for any row with the idempotency-key columns set. If one exists, return success immediately — the previous attempt already landed and a retry would be a wasted LLM call.
2. **LLM call.** No DB transaction held during this call (would starve the pgx pool for ~60s).
3. **Atomic commit.** Open a single DB transaction. Within it, write all roster mutations (`sim_rosters` upsert/delete) AND insert the corresponding `sim_transactions` row(s) including the idempotency-key columns. Commit. If any step fails the whole batch rolls back, leaving the pre-flight check accurate for any subsequent retry.

The pre-flight is racy in theory (two concurrent retries could both pass it before either commits), but Temporal's activity-ID deduplication makes true concurrency vanishingly rare in practice, and the worst case is one duplicate LLM call rather than corrupted state.

---

## Day Loop — Detailed Flow

Each "advance" signal processes one game day. The order of operations matters:

```
Day N advances:
┌─────────────────────────────────────────────────┐
│ 1. PROCESS WAIVERS: Resolve claims due today     │
│    - Claims with process_date <= today           │
│    - Contested players: highest priority wins    │
│    - Winner gets player (BN slot), drops player  │
│    - Winner moves to bottom of priority list     │
│    - Losers' claims marked as lost               │
│    - Unclaimed waiver players become free agents │
│                                                  │
│ 2. REPORT: Yesterday's results available         │
│    - Stats earned by active players yesterday    │
│    - Updated roto standings                      │
│    - Waiver results (who won/lost claims)        │
│    - Notable free agent performances             │
│                                                  │
│ 3. AGENT TURNS: Set lineup for TODAY             │
│    - Agent order RANDOMIZED each day             │
│    - Agent can: set lineup, add free agents,     │
│      file waiver claims, drop players,           │
│      update notes, or do nothing                 │
│    - Free agent adds are instant (first agent    │
│      in randomized order wins contested FAs)     │
│    - Waiver claims are queued for processing     │
│    - 1 LLM call per agent, all tools at once     │
│                                                  │
│ 4. LOCK: Rosters lock for the day                │
│                                                  │
│ 5. SCORE: Collect real stats from today's games  │
│    (skipped on no-game days)                     │
│    - Only active-slot players who appear in the  │
│      boxscore earn stats (team off / healthy     │
│      scratch = nothing)                          │
│    - Filter to game_type=2 AND game_state=FINAL  │
│    - Upsert into sim_agent_daily_stats           │
│    - Recompute sim_agent_totals from SUM(daily)  │
│                                                  │
│ 6. UPDATE: Recalculate roto standings            │
│    (skipped on no-game days)                     │
│    - Rank per category with tie-splitting        │
│    - GAA computed from SUM(components)            │
└─────────────────────────────────────────────────┘
```

Agent processing order is randomized each day to prevent first-mover advantage in free agency. The first agent processed for the day gets first pick of available free agents.

### Off-day handling

The day loop walks **every calendar day** in the regular season, not just days with games. On no-game days (all-star break, between-series gaps, scheduling lulls):

- **ProcessWaiversActivity** still runs — the waiver clock is calendar days, not game days. A claim filed Monday with `waiver_days=2` resolves Wednesday whether Wednesday has games or not.
- **ManageRosterActivity** still runs for every agent. Real Yahoo lets you set lineups daily even on no-game days, and the strategic value is real: agents can stake free-agent pickups before busy stretches. Cost impact is ~5–10% extra LLM calls per pool (~30 off-days × 5 agents in a typical season).
- **CollectDayStatsActivity** and **UpdateStandingsActivity** are **skipped** — no games means no stats to collect and no rank changes to record. `sim_agent_daily_player_stats` simply has no rows for that date, which is the correct steady state. The ON CONFLICT idempotency rule is a no-op because the activity writes zero rows on these days.

The agent's daily context distinguishes game-day vs no-game-day via the `plays_today` boolean in the schedule block.

### Agent Turn — Single LLM Call

Each agent gets one LLM call per day with all tools available. The agent can return multiple tool calls or none.

**Tool call execution order:** adds/drops are processed first, then lineup changes. This lets an agent add a free agent and slot them into the active lineup in the same turn.

**`set_lineup` moves are applied in array order, not atomically.** A swap of two active players works by displacement: move A to slot_of_B (B displaced to BN), then move B to slot_of_A.

**If the agent does nothing:** No tool calls in the response = pass. The agent's reasoning text is still logged. This is a legitimate strategy — not every day requires a move.

**Lineup conflict auto-resolution:** If the agent moves a player to a slot that is already occupied, the displaced player is automatically moved to BN. The agent's intent is clear; the conflict is bookkeeping. The displaced player is recorded in the matching `sim_lineup_moves.displaced_player_id`.

### Error Handling & Fuzzy Recovery

**If the agent messes up:**
- Invalid tool call (bad player ID, position violation) → action skipped, error logged
- Agent returns no tool calls (just text) → treated as pass
- Agent hallucinates a tool name → attempt fuzzy match (edit distance ≤ 2), otherwise ignore
- Malformed JSON in tool arguments → attempt lenient parse (strip trailing commas, fix unquoted keys)
- Text contains implicit action (e.g., "I'd pick player 8476453") → attempt regex extraction as fallback
- LLM API error (rate limit, timeout) → retry up to 2x, then treat as pass

**Per-pool counters (derived from sim_transactions row counts, not separate state):**
- `tool_use_failures` — `COUNT(*) WHERE type='error' AND error_kind='tool_use_failure'`
- `strategic_passes` — `COUNT(*) WHERE type='pass'`
- `llm_errors` — `COUNT(*) WHERE type='error' AND error_kind='llm_error'`

This distinction lets us evaluate whether a model lost because of bad strategy vs bad tool use.

**Cross-pool Prometheus metrics (registered on the existing Worker registry, scraped at `/metrics`:8788):**

| Metric | Type | Labels | Purpose |
|--------|------|--------|---------|
| `puckdb_sim_llm_call_duration_seconds` | Histogram | `provider`, `model`, `agent_name` | Per-call latency. Diagnostic for slow models; correlates with per-agent timeout config. |
| `puckdb_sim_llm_failures_total` | Counter | `provider`, `model`, `reason` | Failure rate by model. `reason` ∈ {timeout, api_error, tool_use_failure, parse_error}. Catches a degrading provider mid-sim. |
| `puckdb_sim_day_duration_seconds` | Histogram | `pool_id` | End-to-end day-loop wall-time. Tells the operator whether `auto_advance` is making progress or wedged. |

These sit alongside the existing `puckdb_activity_duration_seconds` — no new endpoint, no new registry. Token-level metrics (input/output, cached vs uncached) are covered by the prompt-caching work in the H4 follow-up; their telemetry is what makes the cache-hit verification checklist item meaningful.

### Timeout & Failure Handling

```
ManageRosterActivity:
  - Context timeout: configurable per agent (default 60s, 120s for Ollama)
  - Must flow through to llm.Client HTTP timeout at construction time
  - Note: existing llm package hardcodes httpTimeout=120s as a ceiling;
    per-agent timeouts via context.WithTimeout work if <= 120s
  - Temporal retry policy: max 2 attempts
  - If both attempts fail → treat as pass, log the failure
  - The simulation continues regardless
```

A slow or broken model doesn't block the other agents or the day. It just loses a turn — which is a realistic penalty (an absent manager in a real pool misses their moves too).

The activity **always returns success** (nil error to Temporal). LLM failures, invalid moves, and deliberate passes are all valid outcomes logged as data in sim_transactions. Only infrastructure failures (DB down, etc.) should cause Temporal activity retries.

### What the Agent Sees (context message)

```json
{
  "day": "2025-11-15",
  "standings": [
    {"agent": "Claude", "is_you": true, "G": {"value": 45, "rank": 1}, "A": {"value": 82, "rank": 3}, "+/-": {"value": 12, "rank": 2}, "PIM": {"value": 60, "rank": 4}, "PPP": {"value": 20, "rank": 3}, "SOG": {"value": 190, "rank": 2}, "W": {"value": 8, "rank": 4}, "GA": {"value": 30, "rank": 5}, "GAA": {"value": 2.50, "rank": 3}, "total_roto_pts": 38.5},
    {"agent": "GPT", "is_you": false, "G": {"value": 41, "rank": 2}, "total_roto_pts": 35}
  ],
  "your_roster": [
    {"player": "Nikita Kucherov", "id": 8476453, "nhl_position": "RW", "slot": "RW",
     "team": "TBL", "plays_today": true, "games_next_7_days": 3,
     "last_7": {"G": 3, "A": 5, "+/-": 4, "PIM": 2, "PPP": 4, "SOG": 28},
     "season": {"G": 12, "A": 22, "+/-": 8, "PIM": 14, "PPP": 15, "SOG": 95}},
    {"player": "Anthony Stolarz", "id": 8476932, "nhl_position": "G", "slot": "G",
     "team": "TOR", "plays_today": false, "games_next_7_days": 3,
     "last_7": {"W": 1, "GA": 8, "GAA": 2.67},
     "season": {"W": 5, "GA": 30, "GAA": 2.50}}
  ],
  "todays_schedule": [
    {"game": "TOR vs MTL", "home_goals_per_game": 3.1, "away_goals_per_game": 2.8},
    {"game": "TBL vs FLA", "home_goals_per_game": 3.3, "away_goals_per_game": 3.0}
  ],
  "top_free_agents": {
    "skaters": [
      {"player": "Jake DeBrusk", "id": 8479337, "nhl_position": "LW", "team": "VAN",
       "recent_score": 4,    // G+A+PPP over last 7 days; ranking key
       "plays_today": true, "games_next_7_days": 4,
       "last_7": {"G": 2, "A": 1, "+/-": -1, "PIM": 4, "PPP": 1, "SOG": 18},
       "season": {"G": 10, "A": 15, "+/-": 3, "PIM": 20, "PPP": 8, "SOG": 85}}
      // ... up to 20 skaters total, descending recent_score
    ],
    "goalies": [
      // ... up to 5 goalies, descending W (tie-break: lower GA)
    ]
  },
  "your_notes": "Punting GA/GAA — going all-in on offense. Stop picking up goalies unless elite. Focus on PPP and SOG where I'm climbing. Watch for Draisaitl if he gets dropped.",
  "your_analysis": {
    "strong_categories": ["G (rank 1)", "SOG (rank 2)"],
    "weak_categories": ["GA (rank 5)", "W (rank 4)"],
    "bench_players_playing_today": ["Player X", "Player Y"],
    "active_players_not_playing_today": ["Player Z"],
    "open_roster_spots": 0,
    "roster_full": true
  }
}
```

### Context Building Notes

**Free-agent ranking (in `top_free_agents`):** the FA pool is large (~700+ skaters + goalies once mid-season), so the agent context shows the top **20 skaters + 5 goalies** by recent composite, sorted descending. Composite is computed over the **last 7 calendar days** of regular-season game stats — same window as the rostered-player `last_7` block, so the same query path and Go struct can be reused. Formulas:

- **Skater composite:** `G + A + PPP` (sum of recent goals + assists + power-play points). Mirrors the draft fallback metric (skaters by G+A) and adds PPP since it's a roto category in this pool.
- **Goalie composite:** `W` (recent wins). Tie-break by lower `GA`.

Each row includes the composite score (`recent_score` field) so the agent can calibrate gap sizes — "DeBrusk: 6 over 14 days; the next-best LW: 5" is more actionable than rank order alone. Skaters and goalies are presented as separate sub-arrays in `top_free_agents` to make positional needs easy to scan.

- `games_next_7_days`: Count regular-season (`game_type = 2`) games from the `games` table where `game_date` between sim_date and sim_date+7, for the player's current team. Works because we're replaying a past season (all games already exist with final states).
- `home_goals_per_game` / `away_goals_per_game`: Aggregated from `games` table (home/away team scores) for all regular-season (`game_type = 2`) FINAL games up to sim_date for the respective teams.
- All 6 skater roto categories (G, A, +/-, PIM, PPP, SOG) included in `last_7` and `season` blocks.
- All 3 goalie roto categories (W, GA, GAA) included in goalie `last_7` and `season` blocks.

### Example Day

```
Day: 2025-11-15

Agent "Claude" (anthropic / claude-sonnet-4-20250514):
  Rank 1 in G, rank 5 in GA. Stolarz (G) not playing today,
  but Swayman is a free agent and plays tonight.

  Tool calls:
    1. add_player(Swayman, drop=benchPlayer3)
    2. set_lineup([{Swayman → G}, {Stolarz → BN}])
  Reasoning: "Swayman plays tonight vs a weak offense.
              Need to improve GA where I'm rank 5."

Agent "GPT" (openai / gpt-4o):
  Strong across the board, no obvious holes.

  Tool calls: (none)
  Reasoning: "Roster is performing well. No urgent changes.
              Will reassess when schedule gets busier."

Agent "Llama" (ollama / llama3.3:70b):
  Weak in PPP, has a high-PPP player on bench who plays today.

  Tool calls:
    1. set_lineup([{Draisaitl → Util}])
  Reasoning: "Moving Draisaitl to Util, he has 8 PPP
              in last 7 days and plays tonight."
```

### Cost Estimate

Per day: 1 LLM call per agent. With 5 agents and ~185 calendar days in the regular season (~165 game days + ~20 off-days; agents get a turn every calendar day per "Off-day handling" above):
- ~925 total LLM calls for a full season
- Context per call: ~2-3K tokens in (split: ~1.5–2K cacheable prefix + ~0.5–1K per-day context), ~200 tokens out
- Roughly ~2.5M input tokens, ~180K output tokens total
- At Sonnet pricing (~$3/M in, $15/M out): ~$10 per agent's full season

**With Anthropic prompt caching (see "Prompt Caching" above):** the cacheable prefix costs ~10% on cache hits. After the first call per agent, ~75% of input tokens become cache reads at $0.30/M instead of $3/M. Net savings: roughly 30–40% on input cost.

- Without caching: ~$30–50 per 5-agent sim depending on model mix.
- With caching: ~$20–35.
- Worst case (no caching, retries on 5% of calls): ~$80–150 — see also the safety cap suggestion in the review.

Haiku and local Llama models are much cheaper and should dominate any cost-sensitive pool.

### Cost cap

Per-pool safety cap to bound runaway spend (e.g., a model in a retry loop, or a cost estimate that turns out to be optimistic). Defaults to **$200 USD per pool** in `config.max_llm_cost_usd_per_pool` — comfortably above the realistic worst case (~$80–150) but below catastrophic. Operator can set it lower for cost-sensitive pools or higher to disable the cap effectively.

**Persistence.** `sim_pools.total_llm_cost_usd NUMERIC NOT NULL DEFAULT 0` accumulates spend across the lifetime of the sim. Survives ContinueAsNew, worker crashes, and restarts. Visible to GraphQL (`SimPool.totalLlmCostUsd`).

**Pricing table.** Keyed by `(provider, model)`, hardcoded in the simulation package. Anthropic Sonnet ~$3/M input + $15/M output, Anthropic Haiku ~$1/M + $5/M, OpenAI GPT-4o ~$2.50/M + $10/M, Gemini Flash ~$0.075/M + $0.30/M. **Cache reads** (Anthropic, via the H4 prompt-caching usage fields) priced at the model's cache-read rate (~$0.30/M for Sonnet, ~$0.10/M for Haiku) — significantly cheaper than fresh input. Models not in the table (Ollama and other local providers) cost $0. The pricing table is documented as a known-stale source and reviewed quarterly.

**Cost computation.** After each `Complete` call returns, the activity computes `cost = input_tokens * input_rate + cache_read_tokens * cache_read_rate + output_tokens * output_rate` and atomically `UPDATE sim_pools SET total_llm_cost_usd = total_llm_cost_usd + $cost WHERE id = $pool_id`. Done in the same transaction as the activity's persistent writes (transaction row + roster mutations) so retries don't double-count.

**Enforcement.** At the top of `ManageRosterActivity` and `DraftPickActivity` — after the idempotency pre-flight check (skip-if-already-applied) and BEFORE the LLM call — read `total_llm_cost_usd`. If it has reached or exceeded `config.max_llm_cost_usd_per_pool`:

1. Log a `cost_cap_reached` transaction row with the current spend.
2. Set `sim_pools.status = 'paused'` (not `cancelled` — the operator may want to bump the cap and resume).
3. Send a `pause` signal back to the workflow via `client.SignalWorkflow(ctx, workflowID, "pause", nil)`. This reuses the existing pause-signal handler — the workflow drains the signal, sets `paused=true`, and the next day-loop iteration stops processing. No new workflow-side code path is needed.
4. Return success without invoking the LLM.

The check is deliberately *before* the next $0.05 spend rather than after, so the cap is not exceeded by one extra call's worth of cost.

---

## Draft Phase — Detailed Design

### Available Player Presentation

Do NOT send all ~800 NHL players per pick. Instead, compute server-side:

1. Determine the agent's **positional needs** based on current roster (unfilled active slots)
2. Send **top 10 available players per unfilled position**, ranked by **prior-season** stats (see Draft Ranking below for the no-future-info rationale)
3. Send **top 5 best-available overall** regardless of position (for BPA strategy)
4. Total: ~50-80 players per pick

### Draft Ranking for Available Players

Players are ranked by a simple composite for presentation and fallback:
- **Skaters:** `goals + assists` from the **prior season**
- **Goalies:** `wins` from the **prior season**

**Always prior season, never current.** Even though the sim replays a season whose game data already exists in Postgres, using current-season stats would leak future information into the draft — the fallback (and the presentation ranking that feeds the LLM) would silently cherry-pick the players who happen to perform well in the season being replayed. That distorts agent comparison. Prior-season-only restores the realistic information horizon a real fantasy manager would have at draft time.

Source: `player_season_totals` filtered to `season = config.season - 1`, regular season (`game_type = 2`). Sims assume `player_season_totals` for season-1 is populated (consistent with the broader assumption that all configured-season data is fully imported before a sim is started).

This ranking is deliberately simple — the LLM agent applies its own strategy on top. The ranking is just for sorting the presentation and for the deterministic fallback.

### Draft Context Per Pick

```json
{
  "draft_info": {
    "round": 3,
    "pick": 27,
    "overall_pick": 27,
    "total_picks": 90,
    "next_pick_in": 8,
    "snake_direction": "ascending"
  },
  "your_roster": [
    {"player": "Nathan MacKinnon", "id": 8477492, "position": "C", "slot": "C", "pick": 1},
    {"player": "Cale Makar", "id": 8480069, "position": "D", "slot": "D", "pick": 14}
  ],
  "slots_remaining": {"C": 1, "LW": 2, "RW": 2, "D": 2, "G": 2, "Util": 1, "BN": 6},
  "available_by_position": {
    "C": [{"player": "...", "id": 123, "last_season": {"G": 30, "A": 50, ...}}, ...],
    "LW": [...],
    "D": [...],
    "G": [...]
  },
  "best_available_overall": [...]
}
```

### Draft Memory

During the draft, the agent also has the `update_notes` tool. After each pick, the agent can record its draft plan and reasoning. This gives the model continuity across 18 rounds — e.g., "Locked up two elite centers. Targeting goalies in rounds 5-6. Ignoring PIM category."

If the agent doesn't call `update_notes` during the draft, notes remain empty.

### Draft Fallback

If the LLM fails twice for a draft pick, use a deterministic fallback:
- Pick the highest-scoring available player at the agent's most-needed position
- Scoring: skaters by `G+A`, goalies by `W` (from prior season)
- Priority: fill active slots before bench, prioritize positions with 0 filled slots
- This prevents a broken model from stalling the entire draft

---

## Agent LLM Interface

### System Prompt

```
You are {name}, an AI fantasy hockey manager in a {N}-team rotisserie pool.
Your strategy: {strategy}

Rules:
- 9 roto categories: G, A, +/-, PIM, PPP, SOG, W, GA (lower=better), GAA (lower=better)
- Roster: 2C, 2LW, 2RW, 3D, 1Util, 2G active | 6BN bench | 3IR
- Only active players who appear in a game's boxscore accumulate stats
- Players can fill slots matching their NHL position (C, LW, RW, D, or G), or Util (any skater), or BN/IR
- Goalies can only fill G, BN, or IR slots (no Util)
- Active slots are NOT required to be filled. You may leave any active slot empty (or bench every player) as a punting strategy — empty slots simply earn 0 in their categories that day. No minimum-active-slot rule is enforced.

You will be shown the current game state each day. You may adjust your lineup,
add/drop free agents, or do nothing if you're satisfied. If you have no changes
to make, just explain your reasoning — no tool calls needed.

Tool calls are processed in order: adds/drops/claims first, then lineup changes, then notes.
Lineup moves within set_lineup are applied in array order (not atomically).
If you move a player to an occupied slot, the displaced player goes to bench automatically.

Free agents (never owned or cleared waivers) can be picked up instantly with add_player.
Recently dropped players are on waivers — use claim_player to file a claim. Claims are
resolved after the waiver period; highest waiver priority wins contested claims.
Dropped players go on waivers and become free agents if unclaimed.

You have a persistent notes field that carries over between days. Use update_notes to
record strategic observations, plans, or reminders for yourself. Your current notes
(if any) are shown in the context under "your_notes". Keep notes under 50,000 bytes
(roughly 50,000 ASCII characters) — the entire field is included in every daily prompt,
so verbose notes increase your input cost on every call. Treat it as scratchpad, not
journal: prefer terse, current strategy over long history.
```

### Tools

**Draft phase:** `draft_player(player_id: int)` — draft a player from the available pool

**Daily management:**
- `set_lineup(moves: [{player_id: int, slot: string}])` — move players between slots. Slot is one of: C, LW, RW, D, G, Util, BN, IR
- `add_player(player_id: int, drop_player_id: int?)` — instantly pick up a free agent into BN. Must provide `drop_player_id` (a BN player) if BN is full; the dropped player goes on waivers like any drop. Only works for free agents (never owned or cleared waivers).
- `claim_player(player_id: int, drop_player_id: int?)` — file a waiver claim on a recently-dropped player. Claim is queued and resolved after the waiver period. Must provide `drop_player_id` (a BN player) if BN is full (drop happens only if claim succeeds).
- `drop_player(player_id: int)` — release a player. They go on waivers for `waiver_days`, then become a free agent if unclaimed.
- `update_notes(notes: string)` — replace your persistent notes. Use this to record strategy, observations, or plans that should inform future decisions. Notes persist across days. Hard cap: 50,000 bytes (DB-enforced; oversized writes are rejected and the call returns an error).

### Prompt Caching (Anthropic agents)

The system prompt and tool definitions are **identical across every call for an agent** (~1.5–2K tokens). Anthropic's `cache_control: {"type": "ephemeral"}` marker reduces that segment's input cost by ~10× after the first call. Across ~900 calls per pool this is a meaningful saving and — equally important — forces the prompt to actually stay stable, catching accidental per-day mutation (e.g., "include today's date in the system prompt") that would silently invalidate the cache.

**Cacheable prefix (per-agent, stable for the whole simulation):**
1. System prompt (rules block + agent name + strategy)
2. Tool definitions

**Non-cacheable suffix (changes every call):** the per-day context message — yesterday's results, current standings, roster, free agents, schedule, waiver status, agent's notes.

The cache marker goes on the LAST cacheable block (the final tool, or the last system block if tools come earlier in the request). Anthropic caches everything up to and including the marker.

**Prerequisite:** `llm.AnthropicClient` does NOT currently support `cache_control`. Today the wire type uses `system` as a plain string and tools without per-tool fields. Adding caching requires:
- Switch `anthropicRequest.System` from `string` to `[]anthropicSystemBlock` (each `{type: "text", text, cache_control?}`).
- Add optional `CacheControl` to `anthropicTool`.
- Surface a per-block `Cacheable bool` (or equivalent) in `llm.Request` / `llm.Tool` so callers can opt-in.
- Anthropic client translates `Cacheable=true` to `cache_control: {"type": "ephemeral"}`; non-Anthropic clients (OpenAI-compatible, Ollama) silently drop the hint.
- Track usage: `anthropicResponse.Usage` should expose `cache_creation_input_tokens` and `cache_read_input_tokens` so we can verify caching actually fires and feed cost telemetry.

This work happens once in the `llm` package and is reused by Maurice as well; it's a prerequisite for the simulation cost estimate to hold.

### Multi-Provider Support

The simulation talks to all providers through the **single unified `llm` package interface**: `llm.Client.Complete(ctx, *llm.Request)` returning `*llm.Response`. Tool definitions are `[]llm.Tool` (OpenAI-shaped, the package's canonical form); tool calls in the response are `[]llm.ToolCall` of the same shape. The simulation code never touches provider-specific wire types.

**Provider abstraction (verified in the `llm` package today):**
- Request side: `anthropicClient.translateRequest` (anthropic.go) maps `llm.Tool` → Anthropic's `tools` array, extracts `system` messages out of the message stream, coalesces consecutive same-role messages (Anthropic requires alternating).
- Response side: `anthropicResponse.toResponse` maps Anthropic's `tool_use` content blocks back to `llm.ToolCall` and merges `text` blocks into `llm.Response.Content`.
- Tool-result messages: `translateMessage` handles the `tool` role for both directions.
- OpenAI-compatible providers (Ollama, custom `api_base`) use the canonical shape directly.

**Known boundary:** the abstraction is request/response only — no streaming, no partial-tool-call handling. That's irrelevant for the sim's use case (one-shot daily decision per agent) but worth naming so a future contributor adding streaming knows where the abstraction stops.

Each agent gets its own `llm.Client` instance with per-agent config:
- Provider routing via `llm.NewClientForProvider()` (or direct `NewOpenAIClient` for custom `api_base`)
- Per-agent timeout from `agent_config.timeout_seconds` — must be passed into client constructor to set `http.Client.Timeout`
- Per-agent temperature from `agent_config.temperature`

The existing `llm` package supports:
- **Anthropic** — native Messages API (Claude models)
- **OpenAI** — OpenAI-compatible endpoint (GPT models)
- **Ollama** — OpenAI-compatible endpoint (Llama, etc.)
- **Google Gemini** — via OpenAI-compatible endpoint with custom `api_base`

Note: the `llm` package currently hardcodes `httpTimeout = 120s`. Per-agent timeouts require passing the timeout into the client constructor (minor change to `NewOpenAIClient` / `NewAnthropicClient` signatures).

### Sharing with Maurice

Maurice already wraps `llm.Client` with a tool-call execution loop (`maurice/service.go::Chat`, line 88+). The simulation needs the same orchestration shape: build `llm.Request`, call `Complete`, dispatch any tool calls, append results to history, loop until no more tool calls or a round cap is hit. Reimplementing it inside `agent.go` would duplicate ~80 lines of subtle control flow (round counting, termination conditions, tool-result message construction, log-truncation patterns).

**Plan: extract a shared `llm/agentloop` helper.** Pulled out of Maurice's `Chat` method, it'd take:

- An `llm.Client` and `[]llm.Tool` (already canonical).
- An initial `[]llm.Message` (Maurice: system + history + new user message; sim: system + per-day context).
- A tool-execution callback: `func(ctx, llm.ToolCall) (resultJSON string, err error)`. Maurice maps to MCP; sim maps to in-process roster mutations.
- Caps: `maxToolRounds`, `maxTokens`.

Returns: final `*llm.Response`, the audit trail of tool calls + results, total tokens used. **Persistence-agnostic, MCP-agnostic, conversation-shape-agnostic.** Maurice keeps writing to `maurice_messages`; sim keeps writing to `sim_transactions`; the helper writes nothing.

**Refactor sequencing:**
1. Land `llm/agentloop` as part of the simulation prerequisites (alongside the H4 prompt-caching extension).
2. Sim's `agent.go` consumes it directly from day one.
3. Maurice's `Chat` is refactored to call the helper in a separate follow-up — the existing Maurice tests guard the behavior change. Not a sim blocker.

**Bonus payoffs to Maurice from sim work that lands in the `llm` package:**
- Prompt caching (H4 prerequisite): Maurice's per-conversation system prompt becomes cacheable for free.
- Per-call timeout via constructor (already noted above): Maurice currently can't tune timeouts per-conversation; the sim work makes it possible.
- Cross-caller Prometheus metrics (M8 fix): if `puckdb_sim_llm_failures_total{provider, model, reason}` is generalized to `puckdb_llm_failures_total{caller, provider, model, reason}`, Maurice gets dashboarding without a parallel metric.

These are coordination opportunities, not sim blockers — the sim can use the shared helper from day one and Maurice can adopt it on its own schedule.

---

## GraphQL API

### Types

```graphql
type SimPool {
  id: Int!
  name: String!
  season: String!
  status: String!
  simDate: String
  totalLlmCostUsd: Float!  # cumulative LLM spend; sim auto-pauses when this reaches config.max_llm_cost_usd_per_pool
  agents: [SimAgent!]!
  standings: [SimStandingEntry!]!
}

type SimAgent {
  id: Int!
  name: String!
  provider: String!
  model: String!
  draftPosition: Int!
  roster: [SimRosterEntry!]!
  totalRotoPoints: Float!
}

type SimRosterEntry {
  playerId: Int!
  playerName: String!
  slot: String!
  nhlPosition: String!
  nhlTeam: String!
}

type SimStandingEntry {
  agentId: Int!
  agentName: String!
  category: String!
  value: Float!
  rotoPoints: Float!
}

type SimTransaction {
  id: Int!
  agentName: String!
  date: String!
  type: String!                # draft_pick|add|claim|drop|lineup_set|pass|error|cost_cap_reached
  playerName: String           # joined from sim_transactions.player_id
  reasoning: String!           # agent's natural-language explanation; "" for non-LLM types
  # type-specific fields (nullable; populated per "Per-type column population" table):
  round: Int                   # draft_pick
  pick: Int                    # draft_pick
  dropPlayerName: String       # add/claim — joined from drop_player_id
  errorKind: String            # error
  errorDetail: String          # error
  costUsd: Float               # cost_cap_reached
  capUsd: Float                # cost_cap_reached
  lineupMoves: [SimLineupMove!]! # always returned; empty for non-lineup_set types
}

type SimLineupMove {
  sequence: Int!
  playerName: String!          # joined from player_id
  fromSlot: String!
  toSlot: String!
  displacedPlayerName: String  # joined from displaced_player_id; null if no displacement
}
```

### Queries

```graphql
simPool(id: Int!): SimPool
simPools: [SimPool!]!
simTransactions(poolId: Int!, agentId: Int, date: String, limit: Int): [SimTransaction!]!
simStandingsHistory(poolId: Int!): [[SimStandingEntry!]!]!
simPoolProgress(poolId: Int!): ProgressReport   # uses worker/shared.ReportTracker; resolver reads Redis at progress:sim-pool-{pool_id}
```

**Resolver-to-Temporal-query mapping** (see "Query split rationale" in the workflow section). GraphQL resolvers call the appropriate split Temporal queries lazily based on which SimPool fields the client requests:

- `SimPool.{id, name, season, status, simDate, totalLlmCostUsd}` → workflow `summary` query (cheap, always called).
- `SimPool.standings` → workflow `latest_standings` query (only if requested).
- `SimPool.agents[].roster` → workflow `rosters` query.
- `simStandingsHistory` → workflow `standings_for_date` invoked per date in range.
- `simTransactions` → DB read directly (sim_transactions / sim_lineup_moves are durably stored, no Temporal involvement needed).

Clients that only need the day-counter / pause-state pay only the `summary` round-trip; clients rendering the standings panel pay one extra `latest_standings`. The bloated all-in-one query the review flagged is avoided by the resolver layer matching the Temporal-side split.

### Mutations

```graphql
createSimPool(input: CreateSimPoolInput!): SimPool!
advanceSimDay(poolId: Int!): SimPool!
autoAdvanceSim(poolId: Int!): SimPool!
pauseSimPool(poolId: Int!): SimPool!
cancelSimPool(poolId: Int!): SimPool!  # cooperative cancel via Temporal RequestCancelWorkflow; sets status=cancelled
# Note: there is intentionally no editSimPool / updateSimAgent mutation — pool and agent
# config are immutable after createSimPool (see Configuration > "Config is immutable").
# To change settings, cancel and create a new pool.
```

---

## Package Layout

```
worker/simulation/
├── workflow.go          # SimPoolWorkflow + signal/query handlers + ContinueAsNew + drain + tracker
├── activities.go        # BuildFreeAgentPool, DraftPick, CollectDayStats, ManageRoster, ProcessWaivers, UpdateStandings — all with idempotency + cost-cap pre-flights
├── agent.go             # LLM agent interface (build prompt, parse tool calls); consumes llm/agentloop
├── scoring.go           # Roto scoring engine (rank per category, tie-splitting per closed-form formula, rate stats)
├── draft.go             # Draft logic (available player selection, fallback picker — prior-season stats only)
├── pricing.go           # Per-(provider, model) pricing table + cost computation helper (incl. cache-read discount)
├── context.go           # Build agent context payload (standings, roster, free agents w/ composite ranking, schedule)
├── progress.go          # Sim-side ProgressReport factory (NewProgressReport with Group 0 Draft + Group 1 Season)
├── types.go             # Shared types (AgentConfig, RosterSlot, SimLineupMove, transaction-type constants, etc.)
├── scoring_test.go      # Worked-examples + property test for tie-splitting (sum(points) == N(N+1)/2)
├── pricing_test.go      # Table-driven tests covering each priced model + cache-read discount + unknown-model-is-free fallback
├── agent_test.go        # Fuzzy tool recovery (edit distance, lenient JSON, regex extraction) + lineup conflict resolution
├── activities_test.go   # Idempotency tests: CollectDayStats double-call, ManageRoster pre-flight skip, DraftPick pre-flight skip, cost-cap pre-flight signals pause
├── workflow_test.go     # testsuite.WorkflowTestSuite: replay determinism, signal state machine, CAN+drain, cancel handling, Phase 1 signal handling
└── integration_test.go  # Build tag `integration` — full draft + 5-day run with mock-LLM agents; invariants at every checkpoint
```

---

## Implementation Order

### Phase 0: `llm`-package prerequisites (out of `worker/simulation/`, reused by Maurice)
0a. Prompt caching extension — `cache_control` plumbing on system blocks and tools, `Cacheable bool` opt-in, `cache_creation/read_input_tokens` in Usage. (H4)
0b. Per-call timeout via constructor — accept timeout parameter in `NewOpenAIClient` / `NewAnthropicClient`. (M-priority)
0c. `llm/agentloop` shared helper — extracted from Maurice's `Chat`, DB/MCP-agnostic; sim consumes from day one, Maurice migrates later. (L7)

### Phase 1: Foundation
1. Database migration — create all `sim_*` tables (incl. `sim_agent_daily_player_stats`, `sim_lineup_moves`, generated `workflow_id` column) with indexes
2. Go types — structs matching the DB schema, including `SimLineupMove`
3. Scoring engine — roto ranking with tie-splitting (worked-examples + property test), rate stat handling

### Phase 2: Agent
4. Agent interface — prompt construction (cacheable system + tools, non-cacheable per-day context), tool definitions, response parsing
5. Pricing table + cost computation helper — per-(provider, model) rates incl. cache-read discount; `pricing_test.go` (L3)
6. Fuzzy recovery — lenient JSON parsing, tool name matching, text extraction fallback
7. Tool execution — validate and apply agent actions with auto-conflict resolution
8. Draft logic — available player selection, positional need calculation, fallback picker (prior-season stats only)

### Phase 3: Workflow
9. Activities — BuildFreeAgentPool, DraftPick, CollectDayStats (idempotent via daily ledger), ManageRoster, ProcessWaivers, UpdateStandings — all with idempotency pre-flights and (LLM activities) cost-cap pre-flights
10. Workflow — SimPoolWorkflow with signal/query handlers, signal interaction matrix, signal-drain protocol, ContinueAsNew every 30 days carrying `paused`/`autoAdvance` forward, cooperative cancel honoring `workflow.Context`
11. Progress tracking — wire `worker/shared.ReportTracker`; Group 0 "Draft" + Group 1 "Season"
12. Prometheus metrics — `puckdb_sim_llm_call_duration_seconds`, `puckdb_sim_llm_failures_total`, `puckdb_sim_day_duration_seconds`
13. Tests — `workflow_test.go` (testsuite.WorkflowTestSuite), `activities_test.go` (idempotency + cost-cap), `integration_test.go` (build tag `integration`, full draft + 5-day run with invariants)

### Phase 4: API
14. GraphQL schema — types (incl. `SimLineupMove`, `totalLlmCostUsd`) + queries (incl. `simPoolProgress`) + mutations (incl. `cancelSimPool`)
15. Resolvers — split summary/standings calls per "Resolver-to-Temporal-query mapping"; player-name JOINs for transaction display strings
16. CLI commands — `puckdb sim {create,advance,run,pause,cancel,status}` (see "CLI Commands" below for the full spec)

---

## CLI Commands

Scoped to what's needed for Phase 1–3 development (bootstrap + observe a sim). Power-user commands (`list`, `destroy`, `agent-log`) are out of V1 scope; they don't block any earlier phase and can be added later without rework.

| Command | Purpose | Implementation |
|---|---|---|
| `puckdb sim create --config <path>` | Read a pool config from a JSON file (the shape documented in "Configuration" above), POST it to the `createSimPool` GraphQL mutation, print the new `pool_id` and `workflow_id` to stdout. | One GraphQL call. Validation happens server-side. |
| `puckdb sim advance <pool-id>` | Send the `advance` signal — process exactly one game day. | One GraphQL call (`advanceSimDay`). Returns immediately; check progress via `status`. |
| `puckdb sim run <pool-id>` | Send the `auto_advance` signal. Returns immediately (does not block). The user can poll `status` or open the GraphQL UI to watch. | One GraphQL call (`autoAdvanceSim`). |
| `puckdb sim pause <pool-id>` | Send the `pause` signal — halt the auto-loop, in-flight day completes. | One GraphQL call (`pauseSimPool`). |
| `puckdb sim cancel <pool-id>` | Cooperative cancel via `cancelSimPool` — terminates the workflow, sets status=cancelled. | One GraphQL call. |
| `puckdb sim status <pool-id>` | Print `summary` (status, simDate, dayCount, paused/autoAdvance flags, totalLlmCostUsd) + a one-line progress bar from `simPoolProgress` + the latest standings table (agent name, rank, total_roto_pts). The dev's primary observe-the-sim view. | Three GraphQL calls (summary, progress, latest_standings). |

**Config file (`--config <path>`) is the only supported input shape for `create`** — no inline flags. Rationale: the agent array has nested per-agent fields (provider/model/strategy/timeout) where inline flags would need a repeating `--agent key=val,key=val` syntax that's hard to validate and easy to typo. A JSON file matches the schema already documented in "Configuration" and is what the reviewer / dev will iterate on anyway.

**Out of V1 scope** (deliberate; not blocking any phase):
- `puckdb sim list` — list all pools with status. Achievable today via the GraphQL playground (`{ simPools { id name status simDate } }`).
- `puckdb sim destroy <pool-id>` — DELETE FROM sim_pools cascades through the FK chain; can be done with a one-line SQL today.
- `puckdb sim agent-log <pool> <agent>` — agent-specific transaction log. Today: filter `simTransactions(poolId, agentId)` in the playground.

If any of these become friction during Phase 1–3 dev, lift them in then; otherwise defer.

---

## Known Simplifications (V1)

These are intentional scope cuts documented for future improvement:

| Simplification | Impact | Future Fix |
| -------------- | ------ | ---------- |
| 5 strict positions only — no `F`, no multi-eligibility (C/LW etc.) | None in practice (current data fits the 5-value set); a non-conforming player would surface as a loud validation error | Add `F` → C/LW/RW mapping, or `sim_player_eligibility` table for Yahoo-style multi-pos |
| IR unrestricted (no injury designation) | IR is effectively extra bench | Check NHL injury reports |
| Player current-team derivation from most-recent stats row only | Day-1 sim, mid-season trade gaps (1–5 days), and pre-debut rookie callups can have NULL or stale `team` → `plays_today` and `games_next_7_days` misleading or unavailable for affected players | Fallback chain (`season_rosters` → `players.team_id`) or a `player_team_history` materialization |
| No trades between agents | Removes a strategic dimension | Agent-to-agent trade negotiation |
| No transaction limits | Agents can churn FA pool daily | Add `max_adds_per_week` config |
| ~~No waiver priority~~ | ~~First-processed agent wins FA~~ | Done: two-pool system (instant FA + waiver claims with priority) |
| ~~No agent memory between days~~ | ~~Each day is a fresh LLM call~~ | Done: `update_notes` tool + `notes` column |
