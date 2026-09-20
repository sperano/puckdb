ALTER TABLE sim_pools
    DROP CONSTRAINT ck_sim_pools_window,
    DROP COLUMN start_date,
    DROP COLUMN end_date;
