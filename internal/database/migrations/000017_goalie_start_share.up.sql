-- nhl-baseline-v7 builds on v6 and blends a goalie's share of his club's
-- recent starts (regular season and playoffs) into his projected starts (see
-- internal/projection/goalie_share.go). Its four parameters join the typed
-- config columns; 0 (the default for earlier snapshots) means the share was
-- off. v7 fits and stores its aging curve like v4 to v6.
ALTER TABLE projection_snapshots
    ADD COLUMN goalie_share_window_games integer NOT NULL DEFAULT 0,
    ADD COLUMN goalie_share_half_life_games double precision NOT NULL DEFAULT 0,
    ADD COLUMN goalie_playoff_weight double precision NOT NULL DEFAULT 0,
    ADD COLUMN goalie_share_blend double precision NOT NULL DEFAULT 0;

ALTER TABLE projection_snapshots
    DROP CONSTRAINT projection_snapshots_aging_curve_check,
    ADD CONSTRAINT projection_snapshots_aging_curve_check
        CHECK (model_version NOT IN ('nhl-baseline-v4', 'nhl-baseline-v5', 'nhl-baseline-v6', 'nhl-baseline-v7')
            OR aging_curve IS NOT NULL);
