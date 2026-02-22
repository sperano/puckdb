-- Reverse migration: remove player game log specific columns

DROP INDEX IF EXISTS idx_game_skater_stats_gwg;

ALTER TABLE game_skater_stats
    DROP COLUMN IF EXISTS power_play_points,
    DROP COLUMN IF EXISTS game_winning_goals,
    DROP COLUMN IF EXISTS ot_goals;
