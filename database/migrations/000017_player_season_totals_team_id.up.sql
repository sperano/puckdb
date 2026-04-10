-- Migration: Add team_id to player_season_totals + fix season_teams team_id=0 data quality
-- ======================================================================================
--
-- The NHL API standings endpoint sometimes returns team_id=0 alongside the real team_id
-- for historical teams. This migration:
--   1. Fixes Cleveland Barons (only team with team_id=0 and NO real-id row)
--   2. Updates standings_snapshots that reference team_id=0 to the real ID
--   3. Removes duplicate team_id=0 rows from season_teams
--   4. Inserts 5 pre-NHL western league teams (PCHA/WCHL) that appear in NHL playoff data
--   5. Converts player_season_totals.season from start-year to concatenated format
--   6. Adds team_id with composite FK to season_teams

-- ==========================================================================
-- Step 1: Fix Cleveland Barons (team_id=0 → 49)
-- ==========================================================================
-- Cleveland Barons is the only team where team_id=0 is the ONLY entry (no real-id duplicate).
-- Update standings_snapshots first (FK dependency), then season_teams.

UPDATE standings_snapshots
SET team_id = 49
WHERE team_id = 0
  AND season IN (19761977, 19771978)
  AND team_abbrev = 'CBN';

UPDATE season_teams
SET team_id = 49
WHERE team_id = 0
  AND season IN (19761977, 19771978)
  AND abbrev = 'CBN';

-- ==========================================================================
-- Step 2: Migrate standings_snapshots from team_id=0 to real team IDs
-- ==========================================================================
-- For all remaining team_id=0 references, look up the real team_id from season_teams.

UPDATE standings_snapshots ss
SET team_id = real_st.team_id
FROM season_teams real_st
WHERE ss.team_id = 0
  AND real_st.season = ss.season
  AND real_st.abbrev = ss.team_abbrev
  AND real_st.team_id > 0;

-- ==========================================================================
-- Step 3: Delete duplicate team_id=0 rows from season_teams
-- ==========================================================================
-- Now that no FK references point to team_id=0, safe to remove the duplicates.

DELETE FROM season_teams st0
WHERE st0.team_id = 0
  AND EXISTS (
    SELECT 1 FROM season_teams st_real
    WHERE st_real.season = st0.season
      AND st_real.abbrev = st0.abbrev
      AND st_real.team_id > 0
  );

-- Verify no team_id=0 rows remain (Cleveland Barons was already fixed above)
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM season_teams WHERE team_id = 0) THEN
    RAISE EXCEPTION 'Unexpected team_id=0 rows remain in season_teams';
  END IF;
END $$;

-- ==========================================================================
-- Step 4: Insert pre-NHL western league teams
-- ==========================================================================
-- These teams competed for the Stanley Cup against NHL teams (1917-1925).
-- The NHL API records their appearances under leagueAbbrev="NHL" but provides
-- no team_id. We assign synthetic IDs 70-74 in a range unused by the NHL API.

-- Vancouver Millionaires (PCHA) — Cup finalist 1918, 1921, 1922
INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev, conference_name, conference_abbrev)
VALUES
  (19171918, 70, 'Vancouver Millionaires', 'VMI', 'PCHA', 'PCHA', NULL, NULL),
  (19201921, 70, 'Vancouver Millionaires', 'VMI', 'PCHA', 'PCHA', NULL, NULL),
  (19211922, 70, 'Vancouver Millionaires', 'VMI', 'PCHA', 'PCHA', NULL, NULL);

-- Seattle Metropolitans (PCHA) — Cup finalist 1919, 1920 (1919 series cancelled due to flu)
INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev, conference_name, conference_abbrev)
VALUES
  (19181919, 71, 'Seattle Metropolitans', 'SMT', 'PCHA', 'PCHA', NULL, NULL),
  (19191920, 71, 'Seattle Metropolitans', 'SMT', 'PCHA', 'PCHA', NULL, NULL);

-- Edmonton Eskimos (WCHL) — Cup finalist 1923
INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev, conference_name, conference_abbrev)
VALUES
  (19221923, 72, 'Edmonton Eskimos', 'EDK', 'WCHL', 'WCHL', NULL, NULL);

-- Vancouver Maroons (PCHA/WCHL) — Cup finalist 1923, 1924
INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev, conference_name, conference_abbrev)
VALUES
  (19221923, 73, 'Vancouver Maroons', 'VMR', 'PCHA', 'PCHA', NULL, NULL),
  (19231924, 73, 'Vancouver Maroons', 'VMR', 'WCHL', 'WCHL', NULL, NULL);

-- Victoria Cougars (WCHL) — won Cup 1925, finalist 1926
INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev, conference_name, conference_abbrev)
VALUES
  (19241925, 74, 'Victoria Cougars', 'VIC', 'WCHL', 'WCHL', NULL, NULL),
  (19251926, 74, 'Victoria Cougars', 'VIC', 'WHL', 'WHL', NULL, NULL);

-- ==========================================================================
-- Step 5: Convert season from start-year to concatenated format
-- ==========================================================================
-- Currently stores 2024, convert to 20242025 to match seasons.id and season_teams.season.
-- Formula: start_year * 10001 + 1 (e.g. 2024 → 20242025).

-- Drop PK before modifying column values (player_id FK is unaffected)
ALTER TABLE player_season_totals
  DROP CONSTRAINT player_season_totals_pkey;

UPDATE player_season_totals
SET season = season * 10001 + 1;

-- Restore PK
ALTER TABLE player_season_totals
  ADD CONSTRAINT player_season_totals_pkey
  PRIMARY KEY (player_id, season, game_type, league_abbrev, sequence);

-- ==========================================================================
-- Step 6: Add team_id with composite FK
-- ==========================================================================
-- Nullable because most rows are non-NHL leagues with no season_teams entry.

ALTER TABLE player_season_totals ADD COLUMN team_id BIGINT;

-- Populate team_id for NHL rows by matching team_name → season_teams.full_name.
-- Now that season is in concatenated format, we can join directly.
UPDATE player_season_totals pst
SET team_id = st.team_id
FROM season_teams st
WHERE pst.league_abbrev = 'NHL'
  AND st.full_name = pst.team_name
  AND st.season = pst.season;

-- Verify all NHL rows got a team_id
DO $$
DECLARE
  unmatched_count INT;
BEGIN
  SELECT COUNT(*) INTO unmatched_count
  FROM player_season_totals
  WHERE league_abbrev = 'NHL' AND team_id IS NULL;

  IF unmatched_count > 0 THEN
    RAISE EXCEPTION '% NHL player_season_totals rows have no team_id', unmatched_count;
  END IF;
END $$;

-- Composite FK: (season, team_id) → season_teams(season, team_id)
ALTER TABLE player_season_totals
  ADD CONSTRAINT player_season_totals_season_team_fkey
  FOREIGN KEY (season, team_id) REFERENCES season_teams (season, team_id)
  ON DELETE SET NULL (team_id);

-- Index for efficient lookups by team
CREATE INDEX idx_player_season_totals_team_id ON player_season_totals (team_id) WHERE team_id IS NOT NULL;
