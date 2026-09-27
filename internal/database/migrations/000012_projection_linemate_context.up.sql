ALTER TABLE projection_snapshots
    ADD COLUMN linemate_regression_strength double precision NOT NULL DEFAULT 0
        CHECK (
            linemate_regression_strength >= 0
            AND linemate_regression_strength <= 1
            AND linemate_regression_strength < 'Infinity'::double precision
        );

ALTER TABLE projection_players
    ADD COLUMN linemate_observed_points_per_60 double precision,
    ADD COLUMN linemate_average_points_per_60 double precision,
    ADD COLUMN linemate_shared_toi_seconds double precision,
    ADD COLUMN linemate_adjustment_factor double precision;

ALTER TABLE projection_players
    ADD CONSTRAINT projection_players_linemate_context_check CHECK (
        num_nulls(
            linemate_observed_points_per_60,
            linemate_average_points_per_60,
            linemate_shared_toi_seconds,
            linemate_adjustment_factor
        ) IN (0, 4)
        AND (
            linemate_observed_points_per_60 IS NULL
            OR (
                linemate_observed_points_per_60 >= 0
                AND linemate_average_points_per_60 >= 0
                AND linemate_shared_toi_seconds > 0
                AND linemate_adjustment_factor >= 0
                AND linemate_observed_points_per_60 < 'Infinity'::double precision
                AND linemate_average_points_per_60 < 'Infinity'::double precision
                AND linemate_shared_toi_seconds < 'Infinity'::double precision
                AND linemate_adjustment_factor < 'Infinity'::double precision
            )
        )
    );
