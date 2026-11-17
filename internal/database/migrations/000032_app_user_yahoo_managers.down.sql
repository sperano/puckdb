-- Every team a guid link reaches becomes a team link again, so no user loses
-- a team on the way down (a guid link covering several seasons turns into
-- one team link per season).
INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source, linked_at)
SELECT DISTINCT ON (a.user_id, g.league_id, g.team_id) a.user_id, g.league_id, g.team_id, a.source, a.linked_at
FROM app_user_yahoo_managers a
JOIN yahoo_team_manager_guids g ON g.guid = a.guid
ORDER BY a.user_id, g.league_id, g.team_id, (a.source = 'manual') DESC, a.linked_at
ON CONFLICT (user_id, league_id, team_id) DO NOTHING;

DROP TABLE app_user_yahoo_managers;
DROP VIEW yahoo_team_manager_guids;
