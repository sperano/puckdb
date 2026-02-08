-- Remove foreign key constraint from nhl_teams to nhl_divisions
ALTER TABLE nhl_teams DROP CONSTRAINT IF EXISTS nhl_teams_nhl_division_id_fkey;
ALTER TABLE nhl_teams DROP COLUMN IF EXISTS nhl_division_id;

-- Remove foreign key constraint from nhl_divisions to nhl_conferences
ALTER TABLE nhl_divisions DROP CONSTRAINT IF EXISTS nhl_divisions_nhl_conference_id_fkey;

-- Drop the tables
DROP TABLE IF EXISTS nhl_divisions;
DROP TABLE IF EXISTS nhl_conferences;
