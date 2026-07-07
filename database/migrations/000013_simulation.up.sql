-- Hockey Pool Simulator
-- AI-agent fantasy hockey roto pool replaying real NHL seasons day-by-day.
-- Per-pool state, per-agent rosters, daily/total stats, transactions, waivers,
-- and full per-turn LLM telemetry.
--
-- Consolidated migration: this is the net schema of the simulator's
-- incremental development (originally migrations 000013, 000016-000024).
-- The add-then-drop churn from that history (pause-gate booleans, the
-- pause_at enum, the operator-supplied agent name) is omitted; only the
-- final state is created here.

-- =============================================================================
-- Pool & Agents
-- =============================================================================

CREATE TABLE sim_pools (
    id                 SERIAL PRIMARY KEY,
    name               TEXT NOT NULL,
    season             INT NOT NULL REFERENCES seasons(id),
    status             TEXT NOT NULL DEFAULT 'draft',  -- draft|running|paused|complete|cancelled
    sim_date           DATE,                           -- current simulation date

    -- Pool config — fully typed columns (no JSONB). The slot-capacity
    -- columns enumerate the V1 5-position table (PLAN.md > "Position
    -- Eligibility") plus Util/BN/IR. Adding a new slot kind would
    -- require a migration, which is the right tradeoff: the slot
    -- vocabulary is part of the simulator's shape.
    num_teams                INT     NOT NULL,
    waiver_days              INT     NOT NULL,
    draft_rounds             INT     NOT NULL,
    max_llm_cost_usd_per_pool NUMERIC NOT NULL,
    -- Roto categories scored in this pool (G/A/+/-/PIM/PPP/SOG/W/GA/GAA).
    -- TEXT[] rather than a child table because the categories are
    -- iterated en-masse for prompt + scoring; no per-category indexed
    -- query justifies the extra JOIN.
    categories               TEXT[]  NOT NULL,
    -- Per-slot roster capacity. Eight columns matching the V1 vocabulary;
    -- a NULL/absent slot means "not used by this pool" (effectively zero).
    roster_c                 INT     NOT NULL DEFAULT 0,
    roster_lw                INT     NOT NULL DEFAULT 0,
    roster_rw                INT     NOT NULL DEFAULT 0,
    roster_d                 INT     NOT NULL DEFAULT 0,
    roster_g                 INT     NOT NULL DEFAULT 0,
    roster_util              INT     NOT NULL DEFAULT 0,
    roster_bn                INT     NOT NULL DEFAULT 0,
    roster_ir                INT     NOT NULL DEFAULT 0,

    total_llm_cost_usd NUMERIC NOT NULL DEFAULT 0,     -- running cumulative LLM spend

    -- Derived from id: 'sim-pool-{id}'. Generated column so it's set atomically
    -- with INSERT (no race window between row creation and Temporal start).
    workflow_id        TEXT GENERATED ALWAYS AS ('sim-pool-' || id::text) STORED,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Columns added after the original 000013 in the incremental history;
    -- kept here at table end to reproduce that ordinal column order exactly
    -- (so the consolidation is byte-identical to the old chain, not just
    -- semantically equal).

    -- Per-pool flag controlling whether sim_agent_turn_messages is populated.
    -- Defaults to true (active research mode); set false on production pools
    -- where storage cost outweighs the transcript value.
    record_full_messages BOOLEAN NOT NULL DEFAULT true,

    -- Config-time run scoping (no runtime navigation). The workflow runs to
    -- the configured stop point and exits cleanly (status=complete).
    --   never     — run to season end (default)
    --   team_name — exit after Phase 0 team-name picks
    --   draft     — exit after the full snake draft
    --   season    — explicit synonym for never (kept for symmetry)
    stop_after TEXT NOT NULL DEFAULT 'never'
        CHECK (stop_after IN ('never', 'team_name', 'draft', 'season')),
    -- 0 means no cap (run to the season's actual end date). Any positive
    -- value caps the season loop at N days for fast small-sample debugging.
    max_season_days INT NOT NULL DEFAULT 0
        CHECK (max_season_days >= 0)
);

CREATE TABLE sim_agents (
    id             SERIAL PRIMARY KEY,
    pool_id        INT NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    -- draft_position records the SHUFFLED draft order (set by the workflow's
    -- RecordDraftOrder activity after the SideEffect shuffle), not the input
    -- order. Nullable to express "shuffle hasn't run yet."
    draft_position INT,
    provider       TEXT NOT NULL,
    model          TEXT NOT NULL,
    strategy       TEXT NOT NULL,
    notes          TEXT NOT NULL DEFAULT ''
                   CHECK (octet_length(notes) <= 50000),  -- 50KB cap on persistent strategic notes
    -- Per-agent runtime tunables. Typed columns (no JSONB) — there are
    -- only four fields and they're all read once at workflow start, so
    -- a flat shape is simpler than a child table or a JSON blob.
    -- temperature is NULL when the operator wants the provider default.
    timeout_seconds INT     NOT NULL DEFAULT 0,
    temperature     NUMERIC,
    api_base        TEXT    NOT NULL DEFAULT '',
    max_tokens      INT     NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Columns added after the original 000013 in the incremental history;
    -- kept at table end to reproduce that ordinal column order exactly.
    -- team_name (set by PickTeamName in Phase 0) is the agent's sole
    -- identifier. Displays fall back to "agent #<id>" until PickTeamName runs.
    team_name       TEXT NOT NULL DEFAULT '',
    -- Terse <= 50-char label the agent writes alongside its team_name during
    -- PickTeamName, for compact dashboard/tail display.
    strategy_summary TEXT NOT NULL DEFAULT ''
);

-- =============================================================================
-- Rosters & Waivers
-- =============================================================================

CREATE TABLE sim_rosters (
    pool_id      INT    NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    agent_id     INT    NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    player_id    BIGINT NOT NULL REFERENCES players(id),
    slot         TEXT   NOT NULL,                  -- C/LW/RW/D/G/Util/BN/IR
    acquired_at  DATE   NOT NULL,
    acquired_via TEXT   NOT NULL DEFAULT 'draft',  -- draft|free_agent
    PRIMARY KEY (pool_id, agent_id, player_id),
    -- Defense-in-depth: a player can only be on one agent's roster per pool.
    -- Without this, a race during waiver processing could place the same
    -- player on multiple rosters and silently corrupt the simulation.
    UNIQUE (pool_id, player_id)
);

CREATE INDEX idx_sim_rosters_active ON sim_rosters(pool_id, slot)
    WHERE slot NOT IN ('BN', 'IR');

-- Waiver priority — initialized as reverse draft order; winner of a claim
-- drops to bottom (highest priority number).
CREATE TABLE sim_waiver_priority (
    pool_id   INT NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    agent_id  INT NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    priority  INT NOT NULL,            -- lower number = higher priority (1 = first pick)
    PRIMARY KEY (pool_id, agent_id)
);

CREATE TABLE sim_waiver_claims (
    id              SERIAL PRIMARY KEY,
    pool_id         INT    NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    agent_id        INT    NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    player_id       BIGINT NOT NULL REFERENCES players(id),  -- player being claimed
    drop_player_id  BIGINT REFERENCES players(id),           -- player to drop if claim succeeds
    filed_date      DATE   NOT NULL,
    process_date    DATE   NOT NULL,                          -- filed_date + waiver_days
    status          TEXT   NOT NULL DEFAULT 'pending',        -- pending|won|lost|cancelled
    resolved_at     DATE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sim_waiver_claims_pending ON sim_waiver_claims(pool_id, process_date)
    WHERE status = 'pending';

-- Defense-in-depth: an agent may have at most one pending claim per player.
-- Self-cleaning via the partial predicate: once a claim resolves to
-- won/lost/cancelled it leaves the index, freeing the triple for a future claim.
CREATE UNIQUE INDEX ux_sim_waiver_claims_pending_one_per_agent_player
    ON sim_waiver_claims (pool_id, agent_id, player_id)
    WHERE status = 'pending';

-- =============================================================================
-- Daily Stats — per-player attribution + per-agent rollup + season totals
-- =============================================================================

-- Per-player attribution layer (source of truth). Each row is one player's
-- contribution in one category on one day, while on the agent's active roster.
CREATE TABLE sim_agent_daily_player_stats (
    pool_id            INT    NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    agent_id           INT    NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    date               DATE   NOT NULL,
    player_id          BIGINT NOT NULL REFERENCES players(id),
    category           TEXT   NOT NULL,
    value              NUMERIC NOT NULL DEFAULT 0,
    goalie_ga          INT,                       -- raw goals against for this day (GAA rows only)
    goalie_toi_seconds INT,                       -- raw TOI seconds for this day (GAA rows only)
    PRIMARY KEY (pool_id, agent_id, date, player_id, category)
);

CREATE INDEX idx_sim_agent_daily_player_stats_player
    ON sim_agent_daily_player_stats (pool_id, player_id, date);

-- Per-agent rollup derived from sim_agent_daily_player_stats.
-- For GAA: stores raw components per day. Season GAA = SUM(goalie_ga) / SUM(goalie_toi_seconds) * 3600.
CREATE TABLE sim_agent_daily_stats (
    pool_id            INT  NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    agent_id           INT  NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    date               DATE NOT NULL,
    category           TEXT NOT NULL,
    value              NUMERIC NOT NULL DEFAULT 0,
    goalie_ga          INT,
    goalie_toi_seconds INT,
    PRIMARY KEY (pool_id, agent_id, date, category)
);

-- Materialized totals per agent (recomputed from daily_stats each day).
-- Counting stats: SUM(daily value). GAA: SUM(goalie_ga) / SUM(goalie_toi_seconds) * 3600.
CREATE TABLE sim_agent_totals (
    pool_id            INT  NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    agent_id           INT  NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    category           TEXT NOT NULL,
    value              NUMERIC NOT NULL DEFAULT 0,
    goalie_ga          INT,
    goalie_toi_seconds INT,
    PRIMARY KEY (pool_id, agent_id, category)
);

-- =============================================================================
-- Standings (daily snapshot)
-- =============================================================================

CREATE TABLE sim_standings (
    pool_id     INT  NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    date        DATE NOT NULL,
    agent_id    INT  NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    category    TEXT NOT NULL,
    value       NUMERIC NOT NULL,
    roto_points NUMERIC NOT NULL,             -- fractional: supports tie-splitting (e.g., 3.5)
    PRIMARY KEY (pool_id, date, agent_id, category)
);

-- =============================================================================
-- Transactions (full audit log) + lineup move children
-- =============================================================================

-- Fully typed columns, no JSONB. Type-specific fields are nullable;
-- application-layer construction populates the appropriate subset per type.
CREATE TABLE sim_transactions (
    id             SERIAL PRIMARY KEY,
    pool_id        INT    NOT NULL REFERENCES sim_pools(id) ON DELETE CASCADE,
    agent_id       INT    NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    date           DATE   NOT NULL,
    type           TEXT   NOT NULL,                       -- draft_pick|add|claim|drop|lineup_set|pass|error|cost_cap_reached
    player_id      BIGINT REFERENCES players(id),         -- primary player for this tx; NULL for lineup_set/pass/error/cost_cap_reached
    reasoning      TEXT   NOT NULL DEFAULT '',            -- agent's natural-language explanation
    -- draft_pick fields:
    round          INT,
    pick           INT,
    -- add / claim fields (the player getting dropped to make room, if any):
    drop_player_id BIGINT REFERENCES players(id),
    -- error fields:
    error_kind     TEXT,                                  -- tool_use_failure|llm_error|validation
    error_detail   TEXT,
    -- cost_cap_reached fields:
    cost_usd       NUMERIC,                               -- cumulative spend at the moment the cap fired
    cap_usd        NUMERIC,                               -- the configured cap value
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sim_transactions_lookup ON sim_transactions(pool_id, date, agent_id);

-- Child rows for lineup_set transactions. One row per individual slot move
-- within a single set_lineup tool call. `sequence` preserves the agent-supplied
-- ordering (set_lineup applies moves in array order, not atomically).
-- displaced_player_id is non-null when the destination slot was already occupied
-- and the simulator auto-resolved by moving the displaced player to BN.
CREATE TABLE sim_lineup_moves (
    transaction_id      INT    NOT NULL REFERENCES sim_transactions(id) ON DELETE CASCADE,
    sequence            INT    NOT NULL,                          -- 0-based index within the set_lineup call
    player_id           BIGINT NOT NULL REFERENCES players(id),
    from_slot           TEXT   NOT NULL,
    to_slot             TEXT   NOT NULL,
    displaced_player_id BIGINT REFERENCES players(id),            -- NULL if no displacement
    PRIMARY KEY (transaction_id, sequence)
);

-- =============================================================================
-- Turn telemetry — full agent transcript per LLM-driven turn.
--
-- sim_transactions records the fantasy-world outcome of each agent action
-- (the "what happened"). These four tables record the agent introspection
-- behind it (the "how it happened"): every LLM round, every tool call
-- (accepted or rejected), per-round token usage, latency, and (gated by
-- sim_pools.record_full_messages) the literal message-list transcript.
-- =============================================================================

-- sim_agent_turns — one row per LLM-driven turn (team_name, draft pick,
-- daily roster management).
CREATE TABLE sim_agent_turns (
    id              SERIAL PRIMARY KEY,
    pool_id         INT  NOT NULL REFERENCES sim_pools(id)  ON DELETE CASCADE,
    agent_id        INT  NOT NULL REFERENCES sim_agents(id) ON DELETE CASCADE,
    sim_date        DATE,                                 -- NULL for team_name; required for draft/daily
    phase           TEXT NOT NULL,                        -- team_name | draft | daily
    -- draft only: round * num_teams + position. NOT NULL DEFAULT 0 so each
    -- draft pick is its own idempotency unit (non-draft turns share 0).
    pick_number     INT NOT NULL DEFAULT 0,
    status          TEXT NOT NULL,                        -- ok | errored | skipped
    skip_reason     TEXT,                                 -- already_managed | already_drafted | cost_cap_reached
    error_kind      TEXT,                                 -- mirrors sim_transactions.error_kind
    error_detail    TEXT,

    -- Provider/model snapshot at call time. sim_agents.model can be edited
    -- between turns; the snapshot pins what THIS turn actually used.
    provider        TEXT    NOT NULL,
    model           TEXT    NOT NULL,
    temperature     NUMERIC,
    max_tokens      INT,

    -- Aggregate accounting from agentloop.Result.Usage. The per-round
    -- breakdown lives in sim_agent_turn_rounds; these are SUMs for fast
    -- pool-level dashboards without a join.
    rounds                INT     NOT NULL DEFAULT 0,
    prompt_tokens         INT     NOT NULL DEFAULT 0,
    completion_tokens     INT     NOT NULL DEFAULT 0,
    cache_creation_tokens INT     NOT NULL DEFAULT 0,
    cache_read_tokens     INT     NOT NULL DEFAULT 0,
    cost_usd              NUMERIC NOT NULL DEFAULT 0,
    latency_ms            INT     NOT NULL DEFAULT 0,     -- wall clock around agentloop.Run

    -- Final assistant text — the model's prose summary at termination
    -- (matches sim_transactions.reasoning for the action rows of this turn).
    final_text      TEXT NOT NULL DEFAULT '',

    started_at      TIMESTAMPTZ NOT NULL,
    completed_at    TIMESTAMPTZ NOT NULL,

    CONSTRAINT ck_sim_agent_turns_phase
        CHECK (phase IN ('team_name', 'draft', 'daily')),
    CONSTRAINT ck_sim_agent_turns_status
        CHECK (status IN ('ok', 'errored', 'skipped')),
    CONSTRAINT ck_sim_agent_turns_date_by_phase
        CHECK (
            (phase = 'team_name' AND sim_date IS NULL) OR
            (phase IN ('draft', 'daily') AND sim_date IS NOT NULL)
        )
);

-- Phases without a sim_date (team_name): at most one per (pool, agent, phase).
CREATE UNIQUE INDEX ux_sim_agent_turns_no_date
    ON sim_agent_turns (pool_id, agent_id, phase)
    WHERE sim_date IS NULL;

-- Phases with a sim_date (draft, daily): at most one per
-- (pool, agent, date, phase, pick_number). pick_number is in the key so
-- every draft pick is its own idempotency unit (all draft picks for an agent
-- share phase='draft' + the season-start sim_date; without pick_number the
-- idempotent DELETE+INSERT for pick N would CASCADE-wipe pick N-1).
CREATE UNIQUE INDEX ux_sim_agent_turns_with_date
    ON sim_agent_turns (pool_id, agent_id, sim_date, phase, pick_number)
    WHERE sim_date IS NOT NULL;

-- Time-ordered lookup for "show me the timeline of turns in this pool".
CREATE INDEX idx_sim_agent_turns_pool_time
    ON sim_agent_turns (pool_id, started_at);

-- sim_agent_turn_rounds — one row per round (each client.Complete call).
-- Round 0 is the first call; agentloop.Audit.Round uses the same indexing.
CREATE TABLE sim_agent_turn_rounds (
    turn_id               INT  NOT NULL REFERENCES sim_agent_turns(id) ON DELETE CASCADE,
    round_index           INT  NOT NULL,
    assistant_text        TEXT NOT NULL DEFAULT '',      -- resp.Content for this round
    prompt_tokens         INT  NOT NULL DEFAULT 0,
    completion_tokens     INT  NOT NULL DEFAULT 0,
    cache_creation_tokens INT  NOT NULL DEFAULT 0,
    cache_read_tokens     INT  NOT NULL DEFAULT 0,
    latency_ms            INT  NOT NULL DEFAULT 0,        -- per-Complete duration
    PRIMARY KEY (turn_id, round_index)
);

-- sim_agent_tool_calls — one row per tool invocation. Captures rejected calls
-- (the LLM tried something invalid) as well as accepted ones, with the same
-- shape so a single query slices by outcome.
CREATE TABLE sim_agent_tool_calls (
    turn_id          INT     NOT NULL REFERENCES sim_agent_turns(id) ON DELETE CASCADE,
    round_index      INT     NOT NULL,
    sequence         INT     NOT NULL,                    -- order within the round
    tool_name        TEXT    NOT NULL,                    -- raw name as the LLM emitted it
    recovered_name   TEXT,                                -- post-RecoverAction (set only when fuzzy match differed)
    arguments_raw    TEXT    NOT NULL,                    -- ToolCall.Function.Arguments verbatim
    arguments        JSONB,                               -- parsed args; NULL when arguments_raw is not valid JSON
    result           TEXT    NOT NULL,                    -- executor's resultJSON string ("ok: ..." | "error: ...")
    outcome          TEXT    NOT NULL,                    -- accepted | parse_error | validation_rejected | unknown_tool | unhandled
    failure_reason   TEXT,                                -- one of metrics.SimFailure*; populated on outcome != accepted
    applied_transaction_id INT REFERENCES sim_transactions(id) ON DELETE SET NULL,
    latency_ms       INT     NOT NULL DEFAULT 0,          -- executor() duration
    PRIMARY KEY (turn_id, round_index, sequence),
    CONSTRAINT ck_sim_agent_tool_calls_outcome
        CHECK (outcome IN ('accepted', 'parse_error', 'validation_rejected', 'unknown_tool', 'unhandled'))
);

-- Drill from sim_transactions back to the call that produced it.
CREATE INDEX idx_sim_agent_tool_calls_tx
    ON sim_agent_tool_calls (applied_transaction_id)
    WHERE applied_transaction_id IS NOT NULL;

-- "Show me every rejected call for turn 17."
CREATE INDEX idx_sim_agent_tool_calls_outcome
    ON sim_agent_tool_calls (turn_id, outcome);

-- sim_agent_turn_messages — full conversation transcript. Gated on
-- sim_pools.record_full_messages; populated in the same tx as the turn.
CREATE TABLE sim_agent_turn_messages (
    turn_id      INT  NOT NULL REFERENCES sim_agent_turns(id) ON DELETE CASCADE,
    ordinal      INT  NOT NULL,                            -- index in agentloop.Result.Messages
    role         TEXT NOT NULL,                            -- system | user | assistant | tool
    content      TEXT NOT NULL,
    tool_call_id TEXT,                                     -- role='tool' only
    tool_calls   JSONB,                                    -- role='assistant' with tool calls
    PRIMARY KEY (turn_id, ordinal),
    CONSTRAINT ck_sim_agent_turn_messages_role
        CHECK (role IN ('system', 'user', 'assistant', 'tool'))
);
