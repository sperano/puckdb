-- Live draft sessions are keyed by Yahoo's full league key. Numeric league
-- IDs are reused between seasons and are therefore not safe session IDs.
CREATE TABLE draft_sessions (
    league_key text PRIMARY KEY,
    season integer NOT NULL CHECK (season > 0),
    league_id integer NOT NULL CHECK (league_id > 0),
    game_key integer NOT NULL CHECK (game_key > 0),
    state_version bigint DEFAULT 0 NOT NULL CHECK (state_version >= 0),
    draft_status text DEFAULT ''::text NOT NULL,
    board jsonb DEFAULT '{"upstream":[],"manual":[],"upstreamComplete":false,"version":0}'::jsonb NOT NULL,
    board_hash text DEFAULT ''::text NOT NULL,
    recommendations_safe boolean DEFAULT false NOT NULL,
    complete boolean DEFAULT false NOT NULL,
    upstream_pick_count integer DEFAULT 0 NOT NULL CHECK (upstream_pick_count >= 0),
    skipped_pick_count integer DEFAULT 0 NOT NULL CHECK (skipped_pick_count >= 0),
    last_poll_at timestamp with time zone,
    last_success_at timestamp with time zone,
    last_authoritative_at timestamp with time zone,
    last_error text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

-- Every attempt is retained so a capability report can distinguish Yahoo
-- latency, authentication failures, throttling and incomplete responses.
CREATE TABLE draft_session_observations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    league_key text NOT NULL REFERENCES draft_sessions(league_key) ON DELETE CASCADE,
    polled_at timestamp with time zone NOT NULL,
    duration_ms bigint NOT NULL CHECK (duration_ms >= 0),
    success boolean NOT NULL,
    authoritative boolean DEFAULT false NOT NULL,
    changed boolean DEFAULT false NOT NULL,
    draft_status text DEFAULT ''::text NOT NULL,
    declared_count integer DEFAULT 0 NOT NULL CHECK (declared_count >= 0),
    parsed_count integer DEFAULT 0 NOT NULL CHECK (parsed_count >= 0),
    skipped_count integer DEFAULT 0 NOT NULL CHECK (skipped_count >= 0),
    snapshot_hash text DEFAULT ''::text NOT NULL,
    error_class text DEFAULT ''::text NOT NULL,
    error text DEFAULT ''::text NOT NULL
);

CREATE INDEX draft_session_observations_league_idx
    ON draft_session_observations (league_key, polled_at DESC, id DESC);

-- State-changing events are versioned separately from observations. Identical
-- Yahoo snapshots add an observation but no event and do not advance version.
CREATE TABLE draft_session_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    league_key text NOT NULL REFERENCES draft_sessions(league_key) ON DELETE CASCADE,
    state_version bigint NOT NULL CHECK (state_version > 0),
    kind text NOT NULL CHECK (kind IN (
        'upstream_reconcile', 'manual_add', 'manual_correct', 'manual_undo',
        'resolve_keep_manual', 'resolve_accept_upstream'
    )),
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone NOT NULL,
    UNIQUE (league_key, state_version)
);
