-- Lets a Maurice user pick their own Yahoo teams (GraphQL mauriceSetTeams),
-- for a login email that differs from the Yahoo manager email.
--
-- A pick is a link of source 'self': self-declared and unverified, it only
-- changes that user's own prompt, and like 'manual' it survives the email
-- resync (which only touches 'email' links).
ALTER TABLE app_user_yahoo_teams DROP CONSTRAINT app_user_yahoo_teams_source_check;
ALTER TABLE app_user_yahoo_teams ADD CONSTRAINT app_user_yahoo_teams_source_check
    CHECK (source IN ('email', 'manual', 'self'));
ALTER TABLE app_user_yahoo_managers DROP CONSTRAINT app_user_yahoo_managers_source_check;
ALTER TABLE app_user_yahoo_managers ADD CONSTRAINT app_user_yahoo_managers_source_check
    CHECK (source IN ('email', 'manual', 'self'));

-- A team the user deselected. It hides the team from the user's teams
-- whatever link reaches it (a guid link also reaches the account's other
-- teams, which stay), and the email resync makes no team link to it, so a
-- deselected team the login email still matches stays deselected. Selecting
-- the team again, or an operator's link-team/unlink-team, removes the row.
CREATE TABLE app_user_yahoo_team_rejections (
    user_id uuid NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    league_id integer NOT NULL,
    team_id integer NOT NULL,
    rejected_at timestamp with time zone DEFAULT now() NOT NULL,
    PRIMARY KEY (user_id, league_id, team_id),
    FOREIGN KEY (league_id, team_id) REFERENCES yahoo_teams(league_id, id) ON DELETE CASCADE
);

-- When the user first saved or dismissed the team picker offer; NULL until
-- then. Shared by every client, so the offer is made once per user.
ALTER TABLE app_users ADD COLUMN team_offer_seen_at timestamp with time zone;
