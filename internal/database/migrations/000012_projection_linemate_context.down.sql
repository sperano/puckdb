ALTER TABLE projection_players
    DROP COLUMN linemate_adjustment_factor,
    DROP COLUMN linemate_shared_toi_seconds,
    DROP COLUMN linemate_average_points_per_60,
    DROP COLUMN linemate_observed_points_per_60;

ALTER TABLE projection_snapshots
    DROP COLUMN linemate_regression_strength;
