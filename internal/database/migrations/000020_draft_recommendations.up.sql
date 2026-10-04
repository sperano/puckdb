-- Recommendation runs retain their complete deterministic input and output.
-- Keeping the input makes historical replay independent of ranking-snapshot
-- retention, while the indexed version columns make audit lookups cheap.
CREATE TABLE draft_recommendation_runs (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    league_key text NOT NULL,
    session_version bigint NOT NULL CHECK (session_version >= 0),
    ranking_snapshot_id uuid NOT NULL,
    ranking_identity text NOT NULL,
    ranking_version text NOT NULL,
    projection_snapshot_id uuid NOT NULL,
    projection_version text NOT NULL,
    rules_hash text NOT NULL,
    scenario text NOT NULL,
    strategy jsonb NOT NULL,
    input jsonb NOT NULL,
    result jsonb NOT NULL,
    generated_at timestamp with time zone NOT NULL,
    latency_milliseconds bigint NOT NULL CHECK (latency_milliseconds >= 0)
);

CREATE INDEX draft_recommendation_runs_session_idx
    ON draft_recommendation_runs (league_key, session_version, generated_at DESC, id);

CREATE INDEX draft_recommendation_runs_snapshot_idx
    ON draft_recommendation_runs (ranking_snapshot_id, generated_at DESC, id);
