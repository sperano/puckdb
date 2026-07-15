-- Per-pool simulation window. NULL means "use the season's
-- standings_start / standings_end" (the pre-000016 behavior, and the
-- default). Setting these makes "simulate March 2025" a pool config
-- instead of an UPDATE-the-seasons-row hack — the day loop starts at
-- COALESCE(start_date, standings_start) and ends at
-- COALESCE(end_date, standings_end). MaxSeasonDays still caps the
-- window when set.
ALTER TABLE sim_pools
    ADD COLUMN start_date DATE,
    ADD COLUMN end_date DATE,
    ADD CONSTRAINT ck_sim_pools_window
        CHECK (start_date IS NULL OR end_date IS NULL OR start_date <= end_date);
