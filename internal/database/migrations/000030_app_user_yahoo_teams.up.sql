-- Links a Maurice user to the Yahoo fantasy teams they manage, several per
-- user (the owner manages a team in three pools). source distinguishes an
-- automatic match (the login email equals a yahoo_team_managers.email,
-- case-insensitively) from one an operator added by hand for a user whose
-- Yahoo email differs from their login email. league_id/team_id are the
-- legacy numeric Yahoo identifiers already used by yahoo_teams and
-- yahoo_team_managers, not the full league_key: see "Yahoo league identity"
-- in CLAUDE.md for the one-season-per-ID caveat this inherits.

CREATE TABLE app_user_yahoo_teams (
    user_id uuid NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    league_id integer NOT NULL,
    team_id integer NOT NULL,
    source text NOT NULL CHECK (source IN ('email', 'manual')),
    linked_at timestamp with time zone DEFAULT now() NOT NULL,
    PRIMARY KEY (user_id, league_id, team_id),
    FOREIGN KEY (league_id, team_id) REFERENCES yahoo_teams(league_id, id) ON DELETE CASCADE
);
