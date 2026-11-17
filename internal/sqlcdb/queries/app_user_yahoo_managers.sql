-- Links a Maurice user to a Yahoo account, the manager guid
-- (app_user_yahoo_managers, see its migration): the user's teams are every
-- team yahoo_team_manager_guids gives that guid, so a new season's teams
-- need no new link.

-- name: ListYahooTeamManagerGUIDs :many
-- One row per manager of the team, with the guid it resolves to (NULL when
-- none); a team with no manager gives one row with both NULL, and an
-- unknown team no row at all.
SELECT m.id AS manager_id, g.guid
FROM yahoo_teams t
LEFT JOIN yahoo_team_managers m ON m.league_id = t.league_id AND m.team_id = t.id
LEFT JOIN yahoo_team_manager_guids g
    ON g.league_id = m.league_id AND g.team_id = m.team_id AND g.manager_id = m.id
WHERE t.league_id = sqlc.arg(league_id)::integer AND t.id = sqlc.arg(team_id)::integer
ORDER BY m.id;

-- name: SyncAppUserEmailManagerLinks :exec
-- Links user_id to every guid found by email, as source='email'. A link
-- already present under any source is left alone (see
-- SyncAppUserEmailTeamLinks); call with DeleteStaleAppUserEmailManagerLinks.
INSERT INTO app_user_yahoo_managers (user_id, guid, source)
SELECT sqlc.arg(user_id)::uuid, unnest(sqlc.arg(guids)::text[]), 'email'
ON CONFLICT (user_id, guid) DO NOTHING;

-- name: DeleteStaleAppUserEmailManagerLinks :exec
-- Removes user_id's email-sourced guid links not in the current matched
-- set; manual and self links are never touched here.
DELETE FROM app_user_yahoo_managers
WHERE user_id = sqlc.arg(user_id)::uuid AND source = 'email'
  AND NOT (guid = ANY(sqlc.arg(guids)::text[]));

-- name: LinkAppUserYahooManager :one
-- Manual admin link; promotes an existing link for the same guid (email or
-- the user's own pick) to manual, so a resync never removes it, and
-- refreshes linked_at.
INSERT INTO app_user_yahoo_managers (user_id, guid, source)
VALUES (sqlc.arg(user_id)::uuid, sqlc.arg(guid)::text, 'manual')
ON CONFLICT (user_id, guid) DO UPDATE SET source = 'manual', linked_at = now()
RETURNING user_id, guid, source, linked_at;

-- name: ListAppUserYahooManagerLinksAmong :many
-- The guids among guids user_id holds a link to.
SELECT guid FROM app_user_yahoo_managers
WHERE user_id = sqlc.arg(user_id)::uuid AND guid = ANY(sqlc.arg(guids)::text[])
ORDER BY guid;

-- name: UnlinkAppUserYahooManagers :many
-- Removes user_id's links to guids, returning the guids this call deleted.
DELETE FROM app_user_yahoo_managers
WHERE user_id = sqlc.arg(user_id)::uuid AND guid = ANY(sqlc.arg(guids)::text[])
RETURNING guid;
