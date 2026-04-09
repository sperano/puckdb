-- Rename season_teams.season_id → season for naming consistency.
-- All other tables already use "season" as the column name.
ALTER TABLE season_teams RENAME COLUMN season_id TO season;

-- Convert standings_snapshots.season from start-year format (e.g. 2024)
-- to concatenated format (e.g. 20242025) matching seasons.id.
ALTER TABLE standings_snapshots DROP CONSTRAINT standings_snapshots_pkey;
UPDATE standings_snapshots SET season = season * 10001 + 1;

-- Add team_id column, replacing team_abbrev in the primary key.
-- team_abbrev as text is fragile across franchise relocations/rebrands.
ALTER TABLE standings_snapshots ADD COLUMN team_id BIGINT;

UPDATE standings_snapshots ss
SET team_id = st.team_id
FROM season_teams st
WHERE st.season = ss.season AND st.abbrev = ss.team_abbrev;

ALTER TABLE standings_snapshots ALTER COLUMN team_id SET NOT NULL;
ALTER TABLE standings_snapshots ADD PRIMARY KEY (season, date, team_id);

-- Foreign keys for standings_snapshots.
ALTER TABLE standings_snapshots
    ADD CONSTRAINT standings_snapshots_season_fkey
    FOREIGN KEY (season) REFERENCES seasons(id);

ALTER TABLE standings_snapshots
    ADD CONSTRAINT standings_snapshots_season_team_fkey
    FOREIGN KEY (season, team_id) REFERENCES season_teams(season, team_id);

-- Index for team_id lookups.
CREATE INDEX idx_standings_snapshots_team_id ON standings_snapshots(team_id);
