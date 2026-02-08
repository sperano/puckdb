DROP INDEX IF EXISTS idx_nhl_teams_franchise;
ALTER TABLE nhl_teams DROP COLUMN IF EXISTS franchise_id;
DROP TABLE IF EXISTS nhl_franchises;
