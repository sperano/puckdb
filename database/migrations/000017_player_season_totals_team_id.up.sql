-- Migration: Add team_id to player_season_totals + western league teams
-- ====================================================================
--
-- Adds team_id column with composite FK to season_teams.
-- Inserts 5 pre-NHL western league teams (PCHA/WCHL) for Stanley Cup data.
-- Converts player_season_totals.season from start-year to concatenated format.

-- ==========================================================================
-- Step 1: Insert pre-NHL western league teams
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
-- Step 2: Convert season from start-year to concatenated format
-- ==========================================================================
-- On a fresh DB this is a no-op (table is empty). Included for correctness
-- if migrating a populated DB.

ALTER TABLE player_season_totals DROP CONSTRAINT player_season_totals_pkey;

UPDATE player_season_totals
SET season = season * 10001 + 1
WHERE season < 100000;

ALTER TABLE player_season_totals
  ADD CONSTRAINT player_season_totals_pkey
  PRIMARY KEY (player_id, season, game_type, league_abbrev, sequence);

-- ==========================================================================
-- Step 3: Add team_id with composite FK
-- ==========================================================================

ALTER TABLE player_season_totals ADD COLUMN team_id BIGINT;

ALTER TABLE player_season_totals
  ADD CONSTRAINT player_season_totals_season_team_fkey
  FOREIGN KEY (season, team_id) REFERENCES season_teams (season, team_id)
  ON DELETE SET NULL (team_id);

CREATE INDEX idx_player_season_totals_team_id ON player_season_totals (team_id) WHERE team_id IS NOT NULL;
