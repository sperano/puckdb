-- One successful draft ranking refresh of a league: the baseline ranking
-- and, when news adjustments ran, one ranking per news scenario. Snapshots
-- are immutable; the latest one (by as_of) is what readers serve, so a
-- failed or canceled refresh never replaces the last good snapshot.
CREATE TABLE draft_ranking_snapshots (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    season integer NOT NULL,
    league_id integer NOT NULL,
    league_key text NOT NULL,
    -- identity hashes the ranking versions of every scenario and the
    -- adjustment they were built from.
    identity text NOT NULL,
    rules_hash text NOT NULL,
    projection_snapshot_id uuid NOT NULL REFERENCES projection_snapshots(id),
    adjustment_run_id uuid REFERENCES news_adjustment_runs(id),
    as_of timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    -- meta holds the league, scoring, options, assumptions, freshness, news
    -- coverage and unavailable parts of the snapshot (draftrank.Meta).
    meta jsonb NOT NULL
);

CREATE INDEX draft_ranking_snapshots_league_idx ON draft_ranking_snapshots (season, league_id, as_of DESC, id);

-- Every pool player of a snapshot with their placement in each scenario
-- (draftrank.Player).
CREATE TABLE draft_ranking_players (
    snapshot_id uuid NOT NULL REFERENCES draft_ranking_snapshots(id) ON DELETE CASCADE,
    player_key text NOT NULL,
    baseline_rank integer NOT NULL,
    player jsonb NOT NULL,
    PRIMARY KEY (snapshot_id, player_key)
);

-- Each league's refresh attempts, kept so a failure is reported next to the
-- last good snapshot. run_id is the Temporal run, which makes a retried
-- activity update its own row instead of adding one.
CREATE TABLE draft_ranking_refreshes (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    run_id text NOT NULL,
    season integer NOT NULL,
    league_id integer NOT NULL,
    league_key text DEFAULT ''::text NOT NULL,
    status text NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'canceled')),
    state text DEFAULT ''::text NOT NULL,
    error text DEFAULT ''::text NOT NULL,
    snapshot_id uuid REFERENCES draft_ranking_snapshots(id) ON DELETE SET NULL,
    started_at timestamp with time zone NOT NULL,
    finished_at timestamp with time zone,
    UNIQUE (run_id, season, league_id)
);

CREATE INDEX draft_ranking_refreshes_league_idx ON draft_ranking_refreshes (season, league_id, started_at DESC);
