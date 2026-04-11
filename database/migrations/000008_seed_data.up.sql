-- Seed data: pre-NHL western league teams for Stanley Cup records.
-- These teams competed for the Stanley Cup against NHL teams (1917-1925).
-- The NHL API records their appearances under leagueAbbrev="NHL" but provides
-- no team_id. We assign synthetic IDs 70-74 in a range unused by the NHL API.
--
-- seasons rows are inserted first to satisfy season_teams FK.
-- These will be overwritten with real dates when the NHL API manifest is fetched.

INSERT INTO seasons (id, standings_start, standings_end) VALUES
  (19171918, '1917-12-19', '1918-03-20'),
  (19181919, '1918-12-21', '1919-03-10'),
  (19191920, '1919-12-23', '1920-03-10'),
  (19201921, '1920-12-22', '1921-03-14'),
  (19211922, '1921-12-21', '1922-03-11'),
  (19221923, '1922-12-16', '1923-03-05'),
  (19231924, '1923-12-15', '1924-03-07'),
  (19241925, '1924-11-29', '1925-03-09'),
  (19251926, '1925-11-26', '1926-03-17')
ON CONFLICT (id) DO NOTHING;

-- Vancouver Millionaires (PCHA) — Cup finalist 1918, 1921, 1922
INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev, conference_name, conference_abbrev)
VALUES
  (19171918, 70, 'Vancouver Millionaires', 'VMI', 'PCHA', 'PCHA', NULL, NULL),
  (19201921, 70, 'Vancouver Millionaires', 'VMI', 'PCHA', 'PCHA', NULL, NULL),
  (19211922, 70, 'Vancouver Millionaires', 'VMI', 'PCHA', 'PCHA', NULL, NULL);

-- Seattle Metropolitans (PCHA) — Cup finalist 1919, 1920
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
