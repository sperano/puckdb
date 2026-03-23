-- Remove duplicate and subset indexes to reduce write amplification during upserts.
--
-- Exact duplicates:
--   idx_players_yahoo_id       = players_yahoo_id_key (both on players.yahoo_id)
--   idx_yahoo_leagues_league_key = yahoo_leagues_league_key_key (both on yahoo_leagues.league_key)
--   idx_yahoo_teams_team_key   = yahoo_teams_team_key_key (both on yahoo_teams.team_key)
--
-- Subset indexes (left-prefix covered by a wider index):
--   idx_shifts_game (game_id)                 covered by idx_shifts_game_player (game_id, player_id)
--   idx_yahoo_team_rosters_league (league_id) covered by yahoo_team_rosters_pkey (league_id, team_id, date, player_id)
--   idx_yahoo_team_summaries_league (league_id) covered by yahoo_team_summaries_pkey (league_id, team_id, date)

-- Exact duplicates
DROP INDEX IF EXISTS idx_players_yahoo_id;
DROP INDEX IF EXISTS idx_yahoo_leagues_league_key;
DROP INDEX IF EXISTS idx_yahoo_teams_team_key;

-- Subset indexes
DROP INDEX IF EXISTS idx_shifts_game;
DROP INDEX IF EXISTS idx_yahoo_team_rosters_league;
DROP INDEX IF EXISTS idx_yahoo_team_summaries_league;
