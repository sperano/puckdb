-- Links a Maurice user to the Yahoo teams they manage: app_user_yahoo_teams
-- (one team; the fallback for a manager row with no resolvable guid) and,
-- in app_user_yahoo_managers.sql, app_user_yahoo_managers (one Yahoo
-- account). Case-insensitive email matching is done below by
-- ListManagersByEmail; the allowlist filter (empty = every league) by
-- ListAppUserYahooTeams; neither is left to the caller.

-- name: ListManagersByEmail :many
-- Every yahoo_team_managers row whose email matches email, compared
-- case-insensitively, with the guid yahoo_team_manager_guids resolves it to
-- (NULL when none). A blank manager email never matches: it is the unset
-- default, not an empty address.
SELECT m.league_id, m.team_id, g.guid
FROM yahoo_team_managers m
LEFT JOIN yahoo_team_manager_guids g
    ON g.league_id = m.league_id AND g.team_id = m.team_id AND g.manager_id = m.id
WHERE m.email <> '' AND lower(m.email) = lower(sqlc.arg(email)::text)
ORDER BY m.league_id, m.team_id, m.id;

-- name: SyncAppUserEmailTeamLinks :exec
-- Links user_id to the given (league_id, team_id) pairs, as source='email':
-- the caller passes the email's matches whose rows resolve to no guid (a
-- row resolving to one gets a guid link instead). A link already present under any source is left alone:
-- ON CONFLICT DO NOTHING never promotes a manual link or re-stamps
-- linked_at on one already synced. A team the user deselected
-- (app_user_yahoo_team_rejections) gets no link. Call with DeleteStaleAppUserEmailTeamLinks
-- to also drop email links the current match set no longer contains.
INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source)
SELECT sqlc.arg(user_id)::uuid, p.league_id, p.team_id, 'email'
FROM (SELECT unnest(sqlc.arg(league_ids)::integer[]) AS league_id, unnest(sqlc.arg(team_ids)::integer[]) AS team_id) p
WHERE NOT EXISTS (
    SELECT 1 FROM app_user_yahoo_team_rejections r
    WHERE r.user_id = sqlc.arg(user_id)::uuid AND r.league_id = p.league_id AND r.team_id = p.team_id
)
ON CONFLICT (user_id, league_id, team_id) DO NOTHING;

-- name: DeleteStaleAppUserEmailTeamLinks :exec
-- Removes user_id's email-sourced links whose (league_id, team_id) is not
-- among the given pairs, the email's unresolved matches: the user's email
-- changed, the manager row's did, or the row now resolves to a guid (its
-- team link gives way to the guid link). Manual links are never touched
-- here regardless of their pair, nor the user's own picks (self).
WITH matched(league_id, team_id) AS (
    SELECT unnest(sqlc.arg(league_ids)::integer[]), unnest(sqlc.arg(team_ids)::integer[])
)
DELETE FROM app_user_yahoo_teams
WHERE user_id = sqlc.arg(user_id)::uuid AND source = 'email'
  AND NOT EXISTS (
      SELECT 1 FROM matched m
      WHERE m.league_id = app_user_yahoo_teams.league_id AND m.team_id = app_user_yahoo_teams.team_id
  );

-- name: ListAppUserYahooTeams :many
-- One user's teams, through a guid link (every team yahoo_team_manager_guids
-- gives that guid, whichever season) or a team link, with their league's
-- name and season (Yahoo's start year, 2026 for 2026-27), ordered by league
-- then team, limited to league_ids when it is non-empty (the instance's
-- Yahoo league allowlist), leaving out the teams the user deselected
-- (app_user_yahoo_team_rejections). A team reached by several links is
-- listed once: a manual link first, then the user's own pick, then a guid
-- link, then the earliest.
WITH links AS (
    SELECT g.league_id, g.team_id, a.source, a.linked_at, a.guid
    FROM app_user_yahoo_managers a
    JOIN yahoo_team_manager_guids g ON g.guid = a.guid
    WHERE a.user_id = sqlc.arg(user_id)::uuid
    UNION ALL
    SELECT l.league_id, l.team_id, l.source, l.linked_at, ''::text AS guid
    FROM app_user_yahoo_teams l
    WHERE l.user_id = sqlc.arg(user_id)::uuid
)
SELECT DISTINCT ON (k.league_id, k.team_id)
       k.league_id, k.team_id, k.source, k.linked_at, k.guid::text AS guid, t.name AS team_name,
       lg.name AS league_name, lg.season
FROM links k
JOIN yahoo_teams t ON t.league_id = k.league_id AND t.id = k.team_id
JOIN yahoo_leagues lg ON lg.id = k.league_id
WHERE (coalesce(cardinality(sqlc.arg(league_ids)::integer[]), 0) = 0 OR k.league_id = ANY(sqlc.arg(league_ids)::integer[]))
  AND NOT EXISTS (
      SELECT 1 FROM app_user_yahoo_team_rejections r
      WHERE r.user_id = sqlc.arg(user_id)::uuid AND r.league_id = k.league_id AND r.team_id = k.team_id
  )
ORDER BY k.league_id, k.team_id, (k.source = 'manual') DESC, (k.source = 'self') DESC, (k.guid <> '') DESC, k.linked_at, k.guid;

-- name: ListInstanceAppUserYahooTeams :many
-- Every user's teams on one instance, for the admin list command, without
-- the teams each user deselected; a team reached by several of a user's
-- links is listed once, picked as by ListAppUserYahooTeams.
WITH links AS (
    SELECT a.user_id, g.league_id, g.team_id, a.source, a.linked_at, a.guid
    FROM app_user_yahoo_managers a
    JOIN yahoo_team_manager_guids g ON g.guid = a.guid
    UNION ALL
    SELECT l.user_id, l.league_id, l.team_id, l.source, l.linked_at, ''::text AS guid
    FROM app_user_yahoo_teams l
), picked AS (
    SELECT DISTINCT ON (k.user_id, k.league_id, k.team_id)
           k.user_id, k.league_id, k.team_id, k.source, k.linked_at, k.guid
    FROM links k
    WHERE NOT EXISTS (
        SELECT 1 FROM app_user_yahoo_team_rejections r
        WHERE r.user_id = k.user_id AND r.league_id = k.league_id AND r.team_id = k.team_id
    )
    ORDER BY k.user_id, k.league_id, k.team_id, (k.source = 'manual') DESC, (k.source = 'self') DESC, (k.guid <> '') DESC, k.linked_at, k.guid
)
SELECT u.username, p.league_id, p.team_id, p.source, p.linked_at, p.guid::text AS guid, t.name AS team_name
FROM picked p
JOIN app_users u ON u.id = p.user_id
JOIN yahoo_teams t ON t.league_id = p.league_id AND t.id = p.team_id
WHERE u.instance = sqlc.arg(instance)::text
ORDER BY u.username, u.id, p.league_id, p.team_id;

-- name: HasAppUserYahooTeamLink :one
SELECT EXISTS (
    SELECT 1 FROM app_user_yahoo_teams
    WHERE user_id = sqlc.arg(user_id)::uuid AND league_id = sqlc.arg(league_id)::integer AND team_id = sqlc.arg(team_id)::integer
);

-- name: LinkAppUserYahooTeam :one
-- Manual admin link; promotes an existing link for the same pair (email or
-- the user's own pick) to manual, so a resync never removes it, and
-- refreshes linked_at.
INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source)
VALUES (sqlc.arg(user_id)::uuid, sqlc.arg(league_id)::integer, sqlc.arg(team_id)::integer, 'manual')
ON CONFLICT (user_id, league_id, team_id) DO UPDATE SET source = 'manual', linked_at = now()
RETURNING user_id, league_id, team_id, source, linked_at;

-- name: UnlinkAppUserYahooTeam :execrows
DELETE FROM app_user_yahoo_teams
WHERE user_id = sqlc.arg(user_id)::uuid AND league_id = sqlc.arg(league_id)::integer AND team_id = sqlc.arg(team_id)::integer;

-- name: FindAppUserByInstanceUsername :many
-- Username is a mutable snapshot, not unique by constraint; the caller must
-- check for more than one match.
SELECT id, username, email FROM app_users WHERE instance = sqlc.arg(instance)::text AND username = sqlc.arg(username)::text;
