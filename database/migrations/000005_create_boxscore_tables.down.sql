-- Rollback: Drop NHL boxscore tables
-- Order matters: drop tables with foreign keys first

DROP TABLE IF EXISTS nhl_game_goalie_stats;
DROP TABLE IF EXISTS nhl_game_skater_stats;
DROP TABLE IF EXISTS nhl_games;
