ALTER TABLE projection_snapshots
    DROP CONSTRAINT projection_snapshots_aging_curve_check,
    ADD CONSTRAINT projection_snapshots_aging_curve_check
        CHECK (model_version NOT IN ('nhl-baseline-v4', 'nhl-baseline-v5') OR aging_curve IS NOT NULL);
