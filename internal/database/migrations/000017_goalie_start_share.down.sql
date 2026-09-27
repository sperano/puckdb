ALTER TABLE projection_snapshots
    DROP CONSTRAINT projection_snapshots_aging_curve_check,
    ADD CONSTRAINT projection_snapshots_aging_curve_check
        CHECK (model_version NOT IN ('nhl-baseline-v4', 'nhl-baseline-v5', 'nhl-baseline-v6')
            OR aging_curve IS NOT NULL);

ALTER TABLE projection_snapshots
    DROP COLUMN IF EXISTS goalie_share_blend,
    DROP COLUMN IF EXISTS goalie_playoff_weight,
    DROP COLUMN IF EXISTS goalie_share_half_life_games,
    DROP COLUMN IF EXISTS goalie_share_window_games;
