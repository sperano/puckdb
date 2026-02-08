-- Recreate nhl_conferences table
CREATE TABLE IF NOT EXISTS nhl_conferences (
    id BIGINT PRIMARY KEY,
    name TEXT NOT NULL
);

-- Recreate nhl_divisions table
CREATE TABLE IF NOT EXISTS nhl_divisions (
    id BIGINT PRIMARY KEY,
    name TEXT NOT NULL,
    nhl_conference_id BIGINT NOT NULL REFERENCES nhl_conferences(id)
);

-- Add back the division column to nhl_teams
ALTER TABLE nhl_teams ADD COLUMN IF NOT EXISTS nhl_division_id BIGINT REFERENCES nhl_divisions(id);
