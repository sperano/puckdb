-- Manager overrides of news adjustments. Rows are never deleted: a reset or
-- expiry ends an override while keeping it, so a replay at an earlier as-of
-- time still sees what was in force then.
CREATE TABLE news_adjustment_overrides (
    id text PRIMARY KEY,
    player_key text NOT NULL,
    league_key text DEFAULT ''::text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('missed_games', 'input', 'exclude_event')),
    event_id text DEFAULT ''::text NOT NULL,
    scenario text DEFAULT ''::text NOT NULL
        CHECK (scenario IN ('', 'conservative', 'base', 'optimistic')),
    input text DEFAULT ''::text NOT NULL
        CHECK (input IN ('', 'games_played', 'games_started', 'toi_per_game', 'power_play_factor')),
    value double precision DEFAULT 0 NOT NULL CHECK (value >= 0),
    reason text NOT NULL CHECK (btrim(reason) <> ''),
    created_by text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone,
    reset_at timestamp with time zone,
    reset_reason text DEFAULT ''::text NOT NULL,
    CHECK (expires_at IS NULL OR expires_at > created_at),
    CHECK (reset_at IS NULL OR reset_at >= created_at)
);

CREATE INDEX news_adjustment_overrides_player_idx ON news_adjustment_overrides (player_key, created_at);

-- One adjustment of a baseline projection snapshot at an as-of time. The
-- stored policy, season, overrides (as they were at as_of) and event
-- versions are everything newsadjust.Apply reads, so the run can be
-- replayed and its explanations reconstructed.
CREATE TABLE news_adjustment_runs (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    adjustment_id text NOT NULL UNIQUE,
    method_version text NOT NULL,
    policy_hash text NOT NULL,
    policy jsonb NOT NULL,
    baseline_snapshot_id uuid NOT NULL REFERENCES projection_snapshots(id),
    baseline_source_hash text NOT NULL,
    league_key text DEFAULT ''::text NOT NULL,
    as_of timestamp with time zone NOT NULL,
    season jsonb NOT NULL,
    overrides jsonb NOT NULL,
    shadowed_overrides jsonb NOT NULL,
    coverage_warnings jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

-- Every event version a run saw and what it decided about it.
CREATE TABLE news_adjustment_events (
    run_id uuid NOT NULL REFERENCES news_adjustment_runs(id) ON DELETE CASCADE,
    event_id text NOT NULL,
    version integer NOT NULL CHECK (version > 0),
    player_key text NOT NULL,
    incident_id bigint,
    outcome text NOT NULL CHECK (outcome IN ('applied', 'merged', 'alert', 'skipped')),
    reason text NOT NULL,
    scenarios text[] DEFAULT '{}'::text[] NOT NULL,
    event jsonb NOT NULL,
    PRIMARY KEY (run_id, event_id)
);

-- The adjusted projection snapshot of each scenario.
CREATE TABLE news_adjustment_scenarios (
    run_id uuid NOT NULL REFERENCES news_adjustment_runs(id) ON DELETE CASCADE,
    scenario text NOT NULL CHECK (scenario IN ('conservative', 'base', 'optimistic')),
    snapshot_id uuid NOT NULL REFERENCES projection_snapshots(id),
    PRIMARY KEY (run_id, scenario)
);

-- Per-player explanation: reasons, effects, changed stats, overrides with
-- the values they replaced, assumptions and alerts.
CREATE TABLE news_adjustment_players (
    run_id uuid NOT NULL REFERENCES news_adjustment_runs(id) ON DELETE CASCADE,
    player_key text NOT NULL,
    adjustment jsonb NOT NULL,
    PRIMARY KEY (run_id, player_key)
);
