ALTER TABLE projection_snapshots
    DROP CONSTRAINT projection_snapshots_v4_aging_curve_check,
    DROP COLUMN aging_curve;
