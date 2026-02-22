-- Add columns from player game logs that aren't in boxscores
-- These stats are valuable for fantasy hockey but not present in boxscore data:
-- - power_play_points: PPG + PPA (boxscores only have PPG)
-- - game_winning_goals: GWG is a common fantasy category
-- - ot_goals: overtime goal tracking

ALTER TABLE game_skater_stats
    ADD COLUMN IF NOT EXISTS power_play_points SMALLINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS game_winning_goals SMALLINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS ot_goals SMALLINT NOT NULL DEFAULT 0;

-- Index for fantasy queries (GWG is a common stat category)
CREATE INDEX IF NOT EXISTS idx_game_skater_stats_gwg
    ON game_skater_stats(game_winning_goals) WHERE game_winning_goals > 0;
