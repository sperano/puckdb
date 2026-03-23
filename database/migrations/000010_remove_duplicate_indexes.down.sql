-- Recreate the duplicate and subset indexes that were removed.

-- Exact duplicates
CREATE INDEX IF NOT EXISTS idx_players_yahoo_id ON players (yahoo_id);
CREATE INDEX IF NOT EXISTS idx_yahoo_leagues_league_key ON yahoo_leagues (league_key);
CREATE INDEX IF NOT EXISTS idx_yahoo_teams_team_key ON yahoo_teams (team_key);

-- Subset indexes
CREATE INDEX IF NOT EXISTS idx_shifts_game ON shifts (game_id);
CREATE INDEX IF NOT EXISTS idx_yahoo_team_rosters_league ON yahoo_team_rosters (league_id);
CREATE INDEX IF NOT EXISTS idx_yahoo_team_summaries_league ON yahoo_team_summaries (league_id);
