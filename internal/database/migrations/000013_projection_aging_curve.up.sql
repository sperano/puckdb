ALTER TABLE projection_snapshots
    ADD COLUMN aging_curve jsonb,
    ADD CONSTRAINT projection_snapshots_v4_aging_curve_check
        CHECK (model_version <> 'nhl-baseline-v4' OR aging_curve IS NOT NULL);
