ALTER TABLE draft_sessions
    ADD COLUMN sync_version bigint DEFAULT 0 NOT NULL CHECK (sync_version >= 0);

CREATE TABLE draft_shortlist (
    league_key text NOT NULL REFERENCES draft_sessions(league_key) ON DELETE CASCADE,
    player_key text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    PRIMARY KEY (league_key, player_key)
);
