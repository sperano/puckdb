-- Links a Maurice user to the Yahoo teams they manage (app_user_yahoo_teams,
-- see its migration). Case-insensitive email matching is done below by
-- ListManagerLeagueTeamsByEmail; the allowlist filter (empty = every
-- league) by ListAppUserYahooTeams; neither is left to the caller.

-- name: ListManagerLeagueTeamsByEmail :many
-- Distinct (league, team) pairs a yahoo_team_managers row claims for email,
-- compared case-insensitively. A blank manager email never matches: it is
-- the unset default, not an empty address.
SELECT DISTINCT league_id, team_id
FROM yahoo_team_managers
WHERE email <> '' AND lower(email) = lower(sqlc.arg(email)::text);

-- name: SyncAppUserEmailTeamLinks :exec
-- Links user_id to every (league_id, team_id) pair found by email, as
-- source='email'. A link already present under any source is left alone:
-- ON CONFLICT DO NOTHING never promotes a manual link or re-stamps
-- linked_at on one already synced. Call with DeleteStaleAppUserEmailTeamLinks
-- to also drop email links the current match set no longer contains.
INSERT INTO app_user_yahoo_teams (user_id, league_id, team_id, source)
SELECT sqlc.arg(user_id)::uuid, unnest(sqlc.arg(league_ids)::integer[]), unnest(sqlc.arg(team_ids)::integer[]), 'email'
ON CONFLICT (user_id, league_id, team_id) DO NOTHING;

-- name: DeleteStaleAppUserEmailTeamLinks :exec
-- Removes user_id's email-sourced links whose (league_id, team_id) is not in
-- the current matched set (the user's email changed, or the manager row's
-- did); manual links are never touched here regardless of their pair.
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
-- One user's linked teams, ordered by league then team, limited to
-- league_ids when it is non-empty (the instance's Yahoo league allowlist).
SELECT l.league_id, l.team_id, l.source, l.linked_at, t.name AS team_name
FROM app_user_yahoo_teams l
JOIN yahoo_teams t ON t.league_id = l.league_id AND t.id = l.team_id
WHERE l.user_id = sqlc.arg(user_id)::uuid
  AND (coalesce(cardinality(sqlc.arg(league_ids)::integer[]), 0) = 0 OR l.league_id = ANY(sqlc.arg(league_ids)::integer[]))
ORDER BY l.league_id, l.team_id;

-- name: ListInstanceAppUserYahooTeams :many
-- Every user's linked teams on one instance, for the admin list command.
SELECT u.username, l.league_id, l.team_id, l.source, l.linked_at, t.name AS team_name
FROM app_user_yahoo_teams l
JOIN app_users u ON u.id = l.user_id
JOIN yahoo_teams t ON t.league_id = l.league_id AND t.id = l.team_id
WHERE u.instance = sqlc.arg(instance)::text
ORDER BY u.username, l.league_id, l.team_id;

-- name: LinkAppUserYahooTeam :one
-- Manual admin link; promotes an existing email-sourced link for the same
-- pair to manual (so a resync never removes it) and refreshes linked_at.
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
