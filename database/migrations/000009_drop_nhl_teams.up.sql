-- Remove foreign key constraints from tables that reference nhl_teams
-- SQLC-style naming (table_column_fkey)
ALTER TABLE nhl_games DROP CONSTRAINT IF EXISTS nhl_games_home_team_id_fkey;
ALTER TABLE nhl_games DROP CONSTRAINT IF EXISTS nhl_games_away_team_id_fkey;
ALTER TABLE nhl_game_skater_stats DROP CONSTRAINT IF EXISTS nhl_game_skater_stats_team_id_fkey;
ALTER TABLE nhl_game_goalie_stats DROP CONSTRAINT IF EXISTS nhl_game_goalie_stats_team_id_fkey;
ALTER TABLE players DROP CONSTRAINT IF EXISTS players_nhl_team_id_fkey;
ALTER TABLE player_stats DROP CONSTRAINT IF EXISTS player_stats_nhl_team_id_fkey;
ALTER TABLE games DROP CONSTRAINT IF EXISTS games_home_team_id_fkey;
ALTER TABLE games DROP CONSTRAINT IF EXISTS games_away_team_id_fkey;

-- GORM-style naming (fk_table_field)
ALTER TABLE player_stats DROP CONSTRAINT IF EXISTS fk_player_stats_nhl_team;
ALTER TABLE games DROP CONSTRAINT IF EXISTS fk_games_home_team;
ALTER TABLE games DROP CONSTRAINT IF EXISTS fk_games_away_team;

-- Drop the nhl_teams table
DROP TABLE IF EXISTS nhl_teams;
