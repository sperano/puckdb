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

**Tie-breaking:** Tied teams split points (Yahoo rule). Two teams tied for 3rd with 5 teams get (3+2)/2 = 2.5 pts each. Total roto points can be fractional.

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
| W        | game_goalie_stats  | `decision = 'W'`                             |
| GA       | game_goalie_stats  | `goals_against`                              |
| GAA      | game_goalie_stats  | `goals_against / toi_seconds * 3600` (derived) |

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

**Position `F` (forward):** Players listed as `F` in the database can fill C, LW, or RW slots (or Util).

### Draft

Snake draft. 18 rounds x N teams. Order is randomized at pool creation.

**Post-draft roster:** 18 players drafted into 21 available slots. Agents start with 3 empty slots (typically BN) that they can fill via free agency during the season. `roster_full` is based on actual player count vs total slots (21), not draft round count.

### Transactions

Free agent add/drop via waiver claims. No trades (for now). No transaction limits per day/week (V1 simplification — consider adding `max_adds_per_week` in the future).

**add_player validation:** If the roster is full (21 players) and the agent omits `drop_player_id`, the action is rejected with an error logged. The agent does not get a retry.

### Free Agents vs Waivers

Two separate player pools, modeled after Yahoo:

**Free agents** — players who have never been rostered in this pool (or were dropped more than `waiver_days` ago). Picked up instantly via `add_player`. First-processed agent wins (randomized daily order).

**Waivers** — players recently dropped by a pool team. When a player is dropped, they go on waivers for `waiver_days` (configurable, default: 2, matching crapettes). During this window, any agent can file a **waiver claim** via `claim_player`. When the window closes, highest waiver priority wins.

### Waiver System

**Waiver priority:** Starts as reverse draft order (last draft pick = highest priority). When an agent successfully wins a waiver claim, they drop to the bottom of the priority list.

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

### Player Team Assignment

A player's current team is derived from their most recent `game_skater_stats.team_id` or `game_goalie_stats.team_id` as of the sim's current date — not from `players.team_id` which reflects today's real-world value. This correctly handles mid-season trades.

### Position Eligibility

**TODO:** Currently NHL positions only. Future improvement: support Yahoo-style multi-position eligibility (e.g., a player listed as C/LW).

---

## Configuration

```jsonc
{
  "season": "20252026",
  "num_teams": 5,
  "categories": ["G", "A", "+/-", "PIM", "PPP", "SOG", "W", "GA", "GAA"],
  "roster_positions": {
    "C": 2, "LW": 2, "RW": 2, "D": 3, "Util": 1, "G": 2, "BN": 6, "IR": 3
  },
  "waiver_days": 2,
  "draft_rounds": 18,
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
    season       TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'draft',  -- draft|running|paused|complete
    sim_date     DATE,                           -- current simulation date (avoids SQL reserved word "current_date")
    config       JSONB NOT NULL,
    workflow_id  TEXT,
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
    notes          TEXT NOT NULL DEFAULT '',      -- agent-managed persistent notes (strategic memory)
    agent_config   JSONB NOT NULL DEFAULT '{}',  -- temperature, api_base, max_tokens, timeout_seconds
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- sim_rosters: current roster state per agent
CREATE TABLE sim_rosters (
    pool_id      INT NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    agent_id     INT NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    player_id    INT NOT NULL,                   -- nhl player ID
    slot         TEXT NOT NULL,                   -- roster slot: C/LW/RW/D/G/Util/BN/IR
    acquired_at  DATE NOT NULL,
    acquired_via TEXT NOT NULL DEFAULT 'draft',   -- draft|free_agent
    PRIMARY KEY (pool_id, agent_id, player_id)
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
    player_id       INT NOT NULL,        -- player being claimed
    drop_player_id  INT,                 -- player to drop if claim succeeds (NULL if roster not full)
    filed_date      DATE NOT NULL,       -- sim date when claim was filed
    process_date    DATE NOT NULL,       -- sim date when claim will be processed (filed_date + waiver_days)
    status          TEXT NOT NULL DEFAULT 'pending',  -- pending|won|lost|cancelled
    resolved_at     DATE,                -- sim date when resolved
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_sim_waiver_claims_pending ON sim_waiver_claims(pool_id, process_date)
    WHERE status = 'pending';

-- sim_agent_daily_stats: write-once ledger of stats earned per agent per day
-- This is the source of truth for scoring. Each day's stats are computed from
-- the current roster at lock time, then upserted. Rerunning a day overwrites
-- only that day's rows. Totals are SUM() across all days.
-- For GAA: store raw components per day. Season GAA = SUM(goalie_ga) / SUM(goalie_toi_seconds) * 3600.
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

-- sim_transactions: full log of every agent decision with reasoning
CREATE TABLE sim_transactions (
    id         SERIAL PRIMARY KEY,
    pool_id    INT NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    agent_id   INT NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    date       DATE NOT NULL,
    type       TEXT NOT NULL,           -- draft_pick|add|drop|lineup_set|pass|error
    player_id  INT,
    details    JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_sim_transactions_lookup ON sim_transactions(pool_id, date, agent_id);
```

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

```
SimPoolWorkflow(input: SimPoolWorkflowInput)
│
│  Input struct:
│    poolID       int
│    simDate      *Date     -- nil on first run, set on ContinueAsNew
│    autoAdvance  bool      -- recovered from signal state
│    dayCount     int       -- reset to 0 on each ContinueAsNew
│
├── Queries:
│   ├── "status"     → {status, simDate, standings}
│   ├── "rosters"    → all agent rosters
│   └── "log"        → recent transactions
│
├── Signals:
│   ├── "advance"    → process next game day
│   ├── "pause"      → sets paused flag (no-op if already paused)
│   └── "auto_advance" → keep advancing until paused or season ends
│
├── ContinueAsNew: every 30 game days to keep history bounded
│   (carries forward full SimPoolWorkflowInput with updated simDate, dayCount=0)
│
├── Phase 1: DRAFT (skipped if simDate is set, i.e., ContinueAsNew resume)
│   ├── Randomize draft order
│   ├── For each pick (18 rounds x N teams, snake):
│   │   └── DraftPickActivity(agentID, availablePlayers, existingRoster)
│   │       → agent LLM picks a player
│   │       → fallback: if LLM fails 2x, pick best available by G+A (skaters) or W (goalies)
│   │       → persist to sim_rosters + sim_transactions
│   └── Set status = "paused" (wait for first "advance" signal)
│
├── Phase 2: SEASON (loop over game days)
│   └── On each "advance" signal (or auto-advance loop):
│       ├── Get next game day with games (game_state = 'FINAL')
│       ├── ProcessWaiversActivity(date)
│       │   → resolve claims with process_date <= today
│       │   → highest priority wins contested players
│       │   → winner drops to bottom of priority list
│       │   → unclaimed players become free agents
│       ├── Randomize agent processing order for this day
│       ├── For each agent (in randomized order):
│       │   └── ManageRosterActivity(agentID, date, context)
│       │       → agent sees: yesterday's results, standings, roster, free agents, schedule, waiver results
│       │       → agent returns: lineup changes + add/drop/claim (or pass)
│       │       → persist changes
│       ├── CollectDayStatsActivity(date)
│       │   → query game_skater_stats + game_goalie_stats for this date
│       │   → compute stats earned by each agent's ACTIVE roster only
│       │   → UPSERT into sim_agent_daily_stats (idempotent: overwrites this day's rows)
│       │   → recompute sim_agent_totals from SUM(daily_stats)
│       ├── UpdateStandingsActivity(date)
│       │   → rank all agents per category with tie-splitting
│       │   → persist to sim_standings
│       ├── Set sim_date = date, dayCount++
│       └── If dayCount >= 30: ContinueAsNew(poolID, simDate, autoAdvance, dayCount=0)
│
└── Phase 3: COMPLETE
    └── Final standings, set status = "complete"
```

### Idempotency

All stat activities must be idempotent. If the worker crashes mid-day and Temporal retries, double-counting must not occur.

**Strategy:** `CollectDayStatsActivity` computes that day's stat contribution from the current roster (at lock time), then upserts into `sim_agent_daily_stats`. Rerunning the same day overwrites only that day's rows. Totals in `sim_agent_totals` are then recomputed as `SUM()` across all daily rows.

This is correct even after roster changes: if an agent drops Player A on day 50, Player A's stats from days 1-49 remain in `sim_agent_daily_stats` because those rows were written when Player A was on the active roster. Day 50 onward, Player A is no longer active, so new daily rows won't include them. The totals correctly reflect the historical ownership.

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
│    - Only active-slot players who appear in the  │
│      boxscore earn stats (team off / healthy     │
│      scratch = nothing)                          │
│    - Filter to game_state = 'FINAL' only         │
│    - Upsert into sim_agent_daily_stats           │
│    - Recompute sim_agent_totals from SUM(daily)  │
│                                                  │
│ 6. UPDATE: Recalculate roto standings            │
│    - Rank per category with tie-splitting        │
│    - GAA computed from SUM(components)            │
└─────────────────────────────────────────────────┘
```

Agent processing order is randomized each day to prevent first-mover advantage in free agency. The first agent processed for the day gets first pick of available free agents.

### Agent Turn — Single LLM Call

Each agent gets one LLM call per day with all tools available. The agent can return multiple tool calls or none.

**Tool call execution order:** adds/drops are processed first, then lineup changes. This lets an agent add a free agent and slot them into the active lineup in the same turn.

**`set_lineup` moves are applied in array order, not atomically.** A swap of two active players works by displacement: move A to slot_of_B (B displaced to BN), then move B to slot_of_A.

**If the agent does nothing:** No tool calls in the response = pass. The agent's reasoning text is still logged. This is a legitimate strategy — not every day requires a move.

**Lineup conflict auto-resolution:** If the agent moves a player to a slot that is already occupied, the displaced player is automatically moved to BN. The agent's intent is clear; the conflict is bookkeeping. Log the auto-resolution in transaction details.

### Error Handling & Fuzzy Recovery

**If the agent messes up:**
- Invalid tool call (bad player ID, position violation) → action skipped, error logged
- Agent returns no tool calls (just text) → treated as pass
- Agent hallucinates a tool name → attempt fuzzy match (edit distance ≤ 2), otherwise ignore
- Malformed JSON in tool arguments → attempt lenient parse (strip trailing commas, fix unquoted keys)
- Text contains implicit action (e.g., "I'd pick player 8476453") → attempt regex extraction as fallback
- LLM API error (rate limit, timeout) → retry up to 2x, then treat as pass

**Metrics tracked per agent (in sim_transactions):**
- `tool_use_failures` — LLM produced output but it couldn't be parsed into valid actions
- `strategic_passes` — LLM explicitly chose to do nothing
- `llm_errors` — API call failed entirely

This distinction lets us evaluate whether a model lost because of bad strategy vs bad tool use.

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
  "top_free_agents": [
    {"player": "Jake DeBrusk", "id": 8479337, "nhl_position": "LW", "team": "VAN",
     "plays_today": true, "games_next_7_days": 4,
     "last_7": {"G": 2, "A": 1, "+/-": -1, "PIM": 4, "PPP": 1, "SOG": 18},
     "season": {"G": 10, "A": 15, "+/-": 3, "PIM": 20, "PPP": 8, "SOG": 85}}
  ],
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

- `games_next_7_days`: Count games from the `games` table where `game_date` between sim_date and sim_date+7, for the player's current team. Works because we're replaying a past season (all games already exist with final states).
- `home_goals_per_game` / `away_goals_per_game`: Aggregated from `games` table (home/away team scores) for all `FINAL` games up to sim_date for the respective teams.
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

Per day: 1 LLM call per agent. With 5 agents and ~180 game days:
- ~900 total LLM calls for a full season
- Context per call: ~2-3K tokens in, ~200 tokens out
- Roughly ~2.5M input tokens, ~180K output tokens total
- At Sonnet pricing (~$3/M in, $15/M out): ~$10 per agent's full season
- Full 5-agent sim: ~$30-50 depending on model mix (Haiku/Llama much cheaper)

---

## Draft Phase — Detailed Design

### Available Player Presentation

Do NOT send all ~800 NHL players per pick. Instead, compute server-side:

1. Determine the agent's **positional needs** based on current roster (unfilled active slots)
2. Send **top 10 available players per unfilled position**, ranked by prior-season or current-season stats
3. Send **top 5 best-available overall** regardless of position (for BPA strategy)
4. Total: ~50-80 players per pick

### Draft Ranking for Available Players

Players are ranked by a simple composite for presentation and fallback:
- **Skaters:** `goals + assists` from the prior season (or current season if available)
- **Goalies:** `wins` from the prior season

This is deliberately simple — the LLM agent applies its own strategy on top. The ranking is just for sorting the presentation and for the deterministic fallback.

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
- Players can fill slots matching their NHL position, or Util (any skater), or BN/IR
- Players listed as F (forward) can fill C, LW, or RW slots
- Goalies can only fill G, BN, or IR slots

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
(if any) are shown in the context under "your_notes".
```

### Tools

**Draft phase:** `draft_player(player_id: int)` — draft a player from the available pool

**Daily management:**
- `set_lineup(moves: [{player_id: int, slot: string}])` — move players between slots. Slot is one of: C, LW, RW, D, G, Util, BN, IR
- `add_player(player_id: int, drop_player_id: int?)` — instantly pick up a free agent. Must provide `drop_player_id` if roster is full. Only works for free agents (never owned or cleared waivers).
- `claim_player(player_id: int, drop_player_id: int?)` — file a waiver claim on a recently-dropped player. Claim is queued and resolved after the waiver period. Must provide `drop_player_id` if roster is full (drop happens only if claim succeeds).
- `drop_player(player_id: int)` — release a player. They go on waivers for `waiver_days`, then become a free agent if unclaimed.
- `update_notes(notes: string)` — replace your persistent notes. Use this to record strategy, observations, or plans that should inform future decisions. Notes persist across days.

### Multi-Provider Support

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
  type: String!
  playerName: String
  details: String!
}
```

### Queries

```graphql
simPool(id: Int!): SimPool
simPools: [SimPool!]!
simTransactions(poolId: Int!, agentId: Int, date: String, limit: Int): [SimTransaction!]!
simStandingsHistory(poolId: Int!): [[SimStandingEntry!]!]!
```

### Mutations

```graphql
createSimPool(input: CreateSimPoolInput!): SimPool!
advanceSimDay(poolId: Int!): SimPool!
autoAdvanceSim(poolId: Int!): SimPool!
pauseSimPool(poolId: Int!): SimPool!
```

---

## Package Layout

```
worker/simulation/
├── workflow.go          # SimPoolWorkflow + signal/query handlers + ContinueAsNew
├── activities.go        # DraftPick, CollectDayStats, ManageRoster, UpdateStandings
├── agent.go             # LLM agent interface (build prompt, parse tool calls, fuzzy recovery)
├── scoring.go           # Roto scoring engine (rank per category, tie-splitting, rate stats)
├── draft.go             # Draft logic (available player selection, fallback picker)
├── context.go           # Build agent context payload (standings, roster, free agents, schedule)
├── types.go             # Shared types (AgentConfig, RosterSlot, etc.)
├── scoring_test.go      # Test roto ranking: ties, lower-is-better, zero TOI edge case
└── agent_test.go        # Test fuzzy tool recovery, lineup conflict resolution
```

---

## Implementation Order

### Phase 1: Foundation
1. Database migration — create all `sim_*` tables with indexes
2. Go types — structs matching the DB schema
3. Scoring engine — roto ranking with tie-splitting, rate stat handling, tests

### Phase 2: Agent
4. Agent interface — prompt construction, tool definitions, response parsing
5. Fuzzy recovery — lenient JSON parsing, tool name matching, text extraction fallback
6. Tool execution — validate and apply agent actions with auto-conflict resolution
7. Draft logic — available player selection, positional need calculation, fallback picker

### Phase 3: Workflow
8. Activities — DraftPick, CollectDayStats (idempotent via daily ledger), ManageRoster, UpdateStandings
9. Workflow — SimPoolWorkflow with signal/query handlers, ContinueAsNew every 30 days
10. Auto-advance — signal that keeps processing days until paused or season ends

### Phase 4: API
11. GraphQL schema — types + queries + mutations
12. Resolvers — wire up to DB queries and Temporal signals
13. CLI commands — `puckdb sim create`, `puckdb sim advance`, `puckdb sim run`, `puckdb sim status`

---

## Known Simplifications (V1)

These are intentional scope cuts documented for future improvement:

| Simplification | Impact | Future Fix |
| -------------- | ------ | ---------- |
| NHL positions only (no multi-eligibility) | Limits lineup flexibility | Add `sim_player_eligibility` table |
| IR unrestricted (no injury designation) | IR is effectively extra bench | Check NHL injury reports |
| No trades between agents | Removes a strategic dimension | Agent-to-agent trade negotiation |
| No transaction limits | Agents can churn FA pool daily | Add `max_adds_per_week` config |
| ~~No waiver priority~~ | ~~First-processed agent wins FA~~ | Done: two-pool system (instant FA + waiver claims with priority) |
| ~~No agent memory between days~~ | ~~Each day is a fresh LLM call~~ | Done: `update_notes` tool + `notes` column |
