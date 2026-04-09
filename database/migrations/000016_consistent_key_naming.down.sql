-- Reverse foreign keys.
ALTER TABLE standings_snapshots DROP CONSTRAINT IF EXISTS standings_snapshots_season_team_fkey;
ALTER TABLE standings_snapshots DROP CONSTRAINT IF EXISTS standings_snapshots_season_fkey;

-- Reverse PK and team_id.
ALTER TABLE standings_snapshots DROP CONSTRAINT standings_snapshots_pkey;
DROP INDEX IF EXISTS idx_standings_snapshots_team_id;
ALTER TABLE standings_snapshots DROP COLUMN team_id;

-- Convert season back to start-year format: 20242025 → 2024.
UPDATE standings_snapshots SET season = (season - 1) / 10001;

-- Restore original PK.
ALTER TABLE standings_snapshots ADD PRIMARY KEY (season, date, team_abbrev);

-- Reverse rename.
ALTER TABLE season_teams RENAME COLUMN season TO season_id;
