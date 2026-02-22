-- Reverse migration: remove game story data tables

DROP TABLE IF EXISTS shootout_attempts;
DROP TABLE IF EXISTS goal_highlights;
DROP TABLE IF EXISTS game_three_stars;
