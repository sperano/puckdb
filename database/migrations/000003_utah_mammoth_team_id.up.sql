-- From 2025-26 the NHL API identifies Utah as team 68 (Utah Mammoth), but
-- standings carry only the abbreviation and "UTA" resolved to 59 (Utah
-- Hockey Club), so season_teams held (20252026, 59) while every 2025-26
-- Utah game uses 68 — dropping those games from season_teams joins.
-- Move the season row and everything keyed to it from 59 to 68. On a
-- database without a (20252026, 59) row every statement is a no-op.

INSERT INTO season_teams (
    season, team_id, franchise_id, full_name, abbrev, logo_url,
    division_name, division_abbrev, conference_name, conference_abbrev, team_kind
)
SELECT season, 68, franchise_id, full_name, abbrev, logo_url,
       division_name, division_abbrev, conference_name, conference_abbrev, team_kind
FROM season_teams
WHERE season = 20252026 AND team_id = 59
ON CONFLICT (season, team_id) DO NOTHING;

UPDATE club_skater_stats               SET team_id = 68 WHERE season = 20252026 AND team_id = 59;
UPDATE club_goalie_stats               SET team_id = 68 WHERE season = 20252026 AND team_id = 59;
UPDATE edge_team_stats                 SET team_id = 68 WHERE season = 20252026 AND team_id = 59;
UPDATE edge_team_shot_differential     SET team_id = 68 WHERE season = 20252026 AND team_id = 59;
UPDATE edge_team_shot_locations        SET team_id = 68 WHERE season = 20252026 AND team_id = 59;
UPDATE edge_team_sog_summary           SET team_id = 68 WHERE season = 20252026 AND team_id = 59;
UPDATE edge_team_zone_time_by_strength SET team_id = 68 WHERE season = 20252026 AND team_id = 59;
UPDATE season_rosters                  SET team_id = 68 WHERE season = 20252026 AND team_id = 59;
UPDATE standings_snapshots             SET team_id = 68 WHERE season = 20252026 AND team_id = 59;
UPDATE player_season_totals            SET team_id = 68 WHERE season = 20252026 AND team_id = 59;

-- Player landings resolve season totals by team name, and "Utah Mammoth"
-- was unknown, so those NHL rows were stored with a NULL team_id.
UPDATE player_season_totals SET team_id = 68
WHERE season = 20252026 AND team_id IS NULL
  AND league_abbrev = 'NHL' AND team_name = 'Utah Mammoth';

DELETE FROM season_teams WHERE season = 20252026 AND team_id = 59;
