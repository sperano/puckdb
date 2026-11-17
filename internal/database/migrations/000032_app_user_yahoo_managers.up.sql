-- Links a Maurice user to a Yahoo account (the manager guid) rather than to
-- one (league, team) pair: Yahoo issues new league IDs every season, so a
-- team link must be redone every season, while a guid link picks up each new
-- season's teams as soon as they are imported.
--
-- Yahoo hides the guid ('--hidden--') in every endpoint of the current
-- season, and some old rows carry '--' or '' (deleted accounts), so a
-- manager row's account is resolved by yahoo_team_manager_guids: its own
-- guid when real, otherwise the one real guid other rows with the same email
-- (compared case-insensitively) carry. A row resolving to no guid (a new
-- manager with a hidden guid, a league whose emails Yahoo withholds) keeps
-- being linked through app_user_yahoo_teams, which stays as the fallback.

CREATE VIEW yahoo_team_manager_guids AS
WITH real_guids AS (
    SELECT league_id, team_id, id, guid, email
    FROM yahoo_team_managers
    WHERE guid <> '' AND guid NOT LIKE '--%'
), email_guids AS (
    SELECT lower(email) AS email, min(guid) AS guid
    FROM real_guids
    WHERE email <> ''
    GROUP BY lower(email)
    HAVING count(DISTINCT guid) = 1
)
SELECT league_id, team_id, id AS manager_id, guid
FROM real_guids
UNION ALL
SELECT m.league_id, m.team_id, m.id AS manager_id, eg.guid
FROM yahoo_team_managers m
JOIN email_guids eg ON eg.email = lower(m.email)
WHERE (m.guid = '' OR m.guid LIKE '--%') AND m.email <> '';

CREATE TABLE app_user_yahoo_managers (
    user_id uuid NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    guid text NOT NULL CHECK (guid <> '' AND guid NOT LIKE '--%'),
    source text NOT NULL CHECK (source IN ('email', 'manual')),
    linked_at timestamp with time zone DEFAULT now() NOT NULL,
    PRIMARY KEY (user_id, guid)
);

-- Backfill: a team link whose team resolves to exactly one guid, with every
-- manager resolved (a co-managed team could be either manager's), becomes a
-- link to that guid; manual wins over email when a user's team links
-- disagree, and the earliest linked_at is kept.
CREATE TEMPORARY TABLE single_guid_teams AS
SELECT m.league_id, m.team_id, min(g.guid) AS guid
FROM yahoo_team_managers m
LEFT JOIN yahoo_team_manager_guids g
    ON g.league_id = m.league_id AND g.team_id = m.team_id AND g.manager_id = m.id
GROUP BY m.league_id, m.team_id
HAVING count(*) = count(g.guid) AND count(DISTINCT g.guid) = 1;

INSERT INTO app_user_yahoo_managers (user_id, guid, source, linked_at)
SELECT l.user_id, s.guid,
       CASE WHEN bool_or(l.source = 'manual') THEN 'manual' ELSE 'email' END,
       min(l.linked_at)
FROM app_user_yahoo_teams l
JOIN single_guid_teams s ON s.league_id = l.league_id AND s.team_id = l.team_id
GROUP BY l.user_id, s.guid;

DELETE FROM app_user_yahoo_teams l
USING single_guid_teams s
WHERE s.league_id = l.league_id AND s.team_id = l.team_id;

DROP TABLE single_guid_teams;
