-- NHL Franchises table (organizational entities spanning all time)
CREATE TABLE IF NOT EXISTS nhl_franchises (
    id BIGINT PRIMARY KEY,
    full_name TEXT NOT NULL,
    team_common_name TEXT NOT NULL,
    team_place_name TEXT NOT NULL
);

-- Add franchise_id FK to nhl_teams
ALTER TABLE nhl_teams ADD COLUMN franchise_id BIGINT REFERENCES nhl_franchises(id);

CREATE INDEX IF NOT EXISTS idx_nhl_teams_franchise ON nhl_teams(franchise_id);
