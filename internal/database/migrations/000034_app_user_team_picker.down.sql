-- A user's own picks become manual links, so the user keeps those teams and
-- a resync still never removes them; deselections are dropped, so an email
-- match the user deselected comes back on the next login.
ALTER TABLE app_users DROP COLUMN team_offer_seen_at;
DROP TABLE app_user_yahoo_team_rejections;

UPDATE app_user_yahoo_managers SET source = 'manual' WHERE source = 'self';
ALTER TABLE app_user_yahoo_managers DROP CONSTRAINT app_user_yahoo_managers_source_check;
ALTER TABLE app_user_yahoo_managers ADD CONSTRAINT app_user_yahoo_managers_source_check
    CHECK (source IN ('email', 'manual'));

UPDATE app_user_yahoo_teams SET source = 'manual' WHERE source = 'self';
ALTER TABLE app_user_yahoo_teams DROP CONSTRAINT app_user_yahoo_teams_source_check;
ALTER TABLE app_user_yahoo_teams ADD CONSTRAINT app_user_yahoo_teams_source_check
    CHECK (source IN ('email', 'manual'));
