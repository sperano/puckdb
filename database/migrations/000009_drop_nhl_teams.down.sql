-- Recreate nhl_teams table
CREATE TABLE IF NOT EXISTS nhl_teams (
    id BIGINT PRIMARY KEY,
    yahoo_id BIGINT,
    city TEXT NOT NULL,
    name TEXT NOT NULL,
    abbreviation TEXT NOT NULL,
    nhl_home_link TEXT NOT NULL DEFAULT '',
    yahoo_home_link TEXT NOT NULL DEFAULT '',
    small_logo_url TEXT NOT NULL DEFAULT '',
    large_logo_url TEXT NOT NULL DEFAULT '',
    all_stars BOOLEAN NOT NULL DEFAULT false
);

-- Recreate foreign key constraints
ALTER TABLE nhl_games ADD CONSTRAINT nhl_games_home_team_id_fkey
    FOREIGN KEY (home_team_id) REFERENCES nhl_teams(id);
ALTER TABLE nhl_games ADD CONSTRAINT nhl_games_away_team_id_fkey
    FOREIGN KEY (away_team_id) REFERENCES nhl_teams(id);
ALTER TABLE nhl_game_skater_stats ADD CONSTRAINT nhl_game_skater_stats_team_id_fkey
    FOREIGN KEY (team_id) REFERENCES nhl_teams(id);
ALTER TABLE nhl_game_goalie_stats ADD CONSTRAINT nhl_game_goalie_stats_team_id_fkey
    FOREIGN KEY (team_id) REFERENCES nhl_teams(id);
ALTER TABLE players ADD CONSTRAINT players_nhl_team_id_fkey
    FOREIGN KEY (nhl_team_id) REFERENCES nhl_teams(id);
