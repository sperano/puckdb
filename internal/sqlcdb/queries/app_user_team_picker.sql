-- The self-service Yahoo team picker (yahoolink.Picker): the teams a user
-- may pick, their own picks ('self' links), the teams they deselected
-- (app_user_yahoo_team_rejections) and the first-login offer flag.

-- name: ListYahooLeagueSeasons :many
-- The seasons (Yahoo's start year) of the leagues in league_ids, every
-- league when it is empty (the instance's Yahoo league allowlist).
SELECT DISTINCT season FROM yahoo_leagues
WHERE coalesce(cardinality(sqlc.arg(league_ids)::integer[]), 0) = 0 OR id = ANY(sqlc.arg(league_ids)::integer[])
ORDER BY season;

-- name: ListYahooTeamChoices :many
-- Every team of season's leagues in league_ids (every league when empty),
-- with its managers' nicknames ordered by manager ID, blank ones left out.
-- Never a manager's email or guid: any user of the instance sees this list.
SELECT t.league_id, lg.name AS league_name, t.id AS team_id, t.name AS team_name,
       coalesce(array_agg(m.nickname ORDER BY m.id) FILTER (WHERE m.nickname <> ''), '{}')::text[] AS manager_nicknames
FROM yahoo_teams t
JOIN yahoo_leagues lg ON lg.id = t.league_id
LEFT JOIN yahoo_team_managers m ON m.league_id = t.league_id AND m.team_id = t.id
WHERE lg.season = sqlc.arg(season)::integer
  AND (coalesce(cardinality(sqlc.arg(league_ids)::integer[]), 0) = 0 OR t.league_id = ANY(sqlc.arg(league_ids)::integer[]))
GROUP BY t.league_id, lg.name, t.id, t.name
ORDER BY t.league_id, t.id;

-- name: MarkAppUserTeamOfferSeen :execrows
-- Records the first time the user saw the team offer; a later call keeps
-- that time. Taking the user row lock, it also serializes a selection with
-- the user's concurrent logins and selections (UpsertAppUser takes it too).
UPDATE app_users SET team_offer_seen_at = coalesce(team_offer_seen_at, now())
WHERE id = sqlc.arg(user_id)::uuid;

-- name: LockAppUserForLinks :one
-- Takes the user row lock MarkAppUserTeamOfferSeen and UpsertAppUser take,
-- so an operator's link-team/unlink-team serializes with the user's
-- selections and logins, and returns the login email as of the lock (NULL:
-- none), which no login can change before the transaction ends. No row: no
-- such user.
SELECT email FROM app_users WHERE id = sqlc.arg(user_id)::uuid FOR NO KEY UPDATE;

-- name: GetAppUserTeamOfferSeen :one
SELECT (team_offer_seen_at IS NOT NULL)::boolean AS seen FROM app_users WHERE id = sqlc.arg(user_id)::uuid;

-- name: LinkAppUserYahooManagerSelf :exec
-- The user's own pick of a team every manager of which resolves to guid; an
-- email link already present for the guid becomes the pick, so it survives
-- the email no longer matching; a manual or self link stays as it is.
INSERT INTO app_user_yahoo_managers AS l (user_id, guid, source)
VALUES (sqlc.arg(user_id)::uuid, sqlc.arg(guid)::text, 'self')
ON CONFLICT (user_id, guid) DO UPDATE SET source = 'self', linked_at = now()
WHERE l.source = 'email';

-- name: LinkAppUserYahooTeamSelf :exec
-- The user's own pick of one team; an email link already present for the
-- pair becomes the pick, as for LinkAppUserYahooManagerSelf; a manual or
-- self link stays as it is.
INSERT INTO app_user_yahoo_teams AS l (user_id, league_id, team_id, source)
VALUES (sqlc.arg(user_id)::uuid, sqlc.arg(league_id)::integer, sqlc.arg(team_id)::integer, 'self')
ON CONFLICT (user_id, league_id, team_id) DO UPDATE SET source = 'self', linked_at = now()
WHERE l.source = 'email';

-- name: RejectAppUserYahooTeams :exec
-- Records that the user deselected the (league_ids[i], team_ids[i]) teams
-- and removes the user's own and email-sourced team links to them (an
-- operator's manual link stays, hidden by the rejection). Guid links stay:
-- they also reach the account's other teams.
WITH rejected AS (
    INSERT INTO app_user_yahoo_team_rejections (user_id, league_id, team_id)
    SELECT sqlc.arg(user_id)::uuid, unnest(sqlc.arg(league_ids)::integer[]), unnest(sqlc.arg(team_ids)::integer[])
    ON CONFLICT (user_id, league_id, team_id) DO NOTHING
)
DELETE FROM app_user_yahoo_teams l
USING (SELECT unnest(sqlc.arg(league_ids)::integer[]) AS league_id, unnest(sqlc.arg(team_ids)::integer[]) AS team_id) p
WHERE l.user_id = sqlc.arg(user_id)::uuid AND l.source IN ('self', 'email')
  AND l.league_id = p.league_id AND l.team_id = p.team_id;

-- name: ClearAppUserYahooTeamRejections :execrows
-- Removes the user's rejections of the (league_ids[i], team_ids[i]) teams.
DELETE FROM app_user_yahoo_team_rejections r
USING (SELECT unnest(sqlc.arg(league_ids)::integer[]) AS league_id, unnest(sqlc.arg(team_ids)::integer[]) AS team_id) p
WHERE r.user_id = sqlc.arg(user_id)::uuid AND r.league_id = p.league_id AND r.team_id = p.team_id;
