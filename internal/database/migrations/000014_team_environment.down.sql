ALTER TABLE projection_snapshots
    DROP CONSTRAINT IF EXISTS projection_snapshots_aging_curve_check,
    ADD CONSTRAINT projection_snapshots_v4_aging_curve_check
        CHECK (model_version <> 'nhl-baseline-v4' OR aging_curve IS NOT NULL);

DROP INDEX IF EXISTS idx_play_events_minor_penalties;

ALTER TABLE projection_players
    DROP COLUMN IF EXISTS team_environment;

ALTER TABLE projection_snapshots
    DROP COLUMN IF EXISTS team_environment_max_change,
    DROP COLUMN IF EXISTS team_environment_prior_games;
