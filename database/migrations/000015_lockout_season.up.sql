-- 2004-05 NHL season was cancelled by lockout (no NHL games), but the
-- World Cup of Hockey 2004 happened that year. Player landings include
-- WCH 20042005 rows; the international team upsert needs the season
-- to exist to satisfy season_teams_season_fkey. Dates use the NHL's
-- originally-scheduled window for the cancelled season.
INSERT INTO seasons (id, standings_start, standings_end)
VALUES (20042005, '2004-10-13', '2005-04-17')
ON CONFLICT (id) DO NOTHING;
