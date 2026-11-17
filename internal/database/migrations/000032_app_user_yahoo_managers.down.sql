-- Every team a guid link reaches becomes a team link again, so no user loses
-- a team on the way down (a guid link covering several seasons turns into
-- one team link per season). A team link already present keeps its row,
-- promoted to manual when a manual guid link reaches it, so a later resync
-- never drops a team an operator linked by hand.
INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source, linked_at)
SELECT DISTINCT ON (a.user_id, g.league_id, g.team_id) a.user_id, g.league_id, g.team_id, a.source, a.linked_at
FROM app_user_yahoo_managers a
JOIN yahoo_team_manager_guids g ON g.guid = a.guid
ORDER BY a.user_id, g.league_id, g.team_id, (a.source = 'manual') DESC, a.linked_at
ON CONFLICT (user_id, league_id, team_id) DO UPDATE SET source = 'manual'
    WHERE EXCLUDED.source = 'manual' AND app_user_yahoo_teams.source <> 'manual';

DROP TABLE app_user_yahoo_managers;
DROP VIEW yahoo_team_manager_guids;
