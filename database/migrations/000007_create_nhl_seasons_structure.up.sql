-- NHL Seasons table (from SeasonStandingManifest)
CREATE TABLE IF NOT EXISTS nhl_seasons (
    id INT PRIMARY KEY,                    -- e.g., 20232024
    standings_start DATE NOT NULL,
    standings_end DATE NOT NULL
);

-- Team identity per season (denormalized league structure)
CREATE TABLE IF NOT EXISTS nhl_season_teams (
    season_id INT NOT NULL REFERENCES nhl_seasons(id),
    team_id BIGINT NOT NULL,               -- NHL API team ID
    franchise_id BIGINT REFERENCES nhl_franchises(id),

    -- Team identity
    full_name TEXT NOT NULL,               -- "Montréal Canadiens"
    abbrev TEXT NOT NULL,                  -- "MTL"
    logo_url TEXT,

    -- League structure (denormalized from standings)
    division_name TEXT NOT NULL,           -- "Atlantic"
    division_abbrev TEXT NOT NULL,         -- "A"
    conference_name TEXT,                  -- "Eastern" (NULL pre-1974)
    conference_abbrev TEXT,                -- "E" (NULL pre-1974)

    PRIMARY KEY (season_id, team_id)
);

CREATE INDEX IF NOT EXISTS idx_nhl_season_teams_franchise ON nhl_season_teams(franchise_id);
CREATE INDEX IF NOT EXISTS idx_nhl_season_teams_abbrev ON nhl_season_teams(abbrev);
CREATE INDEX IF NOT EXISTS idx_nhl_season_teams_division ON nhl_season_teams(division_name);
