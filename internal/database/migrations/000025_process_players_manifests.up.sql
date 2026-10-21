-- Durable player manifests for ProcessPlayersWorkflow. Run 1 of a chain
-- copies the consolidated boxscore player list (a Redis key with a short TTL
-- whose order changes when it is rebuilt) into
-- process_players_manifest_players, ordered by player_id; every later
-- execution carries only the manifest id, count, hash and an ordinal cursor
-- instead of the whole list. Rows are written once and never updated; the
-- newest manifests are kept (older ones, with their players and errors, are
-- deleted when a new one is created) so the last run's errors stay
-- queryable.
CREATE TABLE process_players_manifests (
    id text PRIMARY KEY,            -- FirstRunID of the workflow chain
    workflow_id text NOT NULL,
    player_count integer NOT NULL,
    players_hash bytea NOT NULL,    -- sha256 over the ordered player rows
    created_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,        -- set by the chain's last execution
    CHECK (player_count > 0)
);

CREATE TABLE process_players_manifest_players (
    manifest_id text NOT NULL REFERENCES process_players_manifests (id) ON DELETE CASCADE,
    ordinal integer NOT NULL,       -- 0-based position, ordered by player_id
    player_id bigint NOT NULL,
    first_name text NOT NULL,
    last_name text NOT NULL,
    position text NOT NULL,
    PRIMARY KEY (manifest_id, ordinal),
    UNIQUE (manifest_id, player_id),
    CHECK (ordinal >= 0)
);

-- Per-player errors of a chain, recorded from completed batch results only,
-- so their number matches the error count the workflow carries. Upserted:
-- a retried or overlapping recording converges on the same rows.
CREATE TABLE process_players_manifest_errors (
    manifest_id text NOT NULL REFERENCES process_players_manifests (id) ON DELETE CASCADE,
    player_id bigint NOT NULL,
    stage text NOT NULL,
    message text NOT NULL,
    PRIMARY KEY (manifest_id, player_id, stage),
    CHECK (stage IN ('missing_import', 'read', 'upsert', 'career'))
);
