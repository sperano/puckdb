-- name: GetPlayer :one
SELECT * FROM players WHERE id = $1;

-- name: GetPlayerByYahooID :one
SELECT * FROM players WHERE yahoo_id = $1;

-- name: GetAllPlayers :many
SELECT * FROM players ORDER BY last_name, first_name;

-- name: GetActivePlayers :many
SELECT * FROM players WHERE is_active = TRUE ORDER BY last_name, first_name;

-- name: GetPlayersByTeam :many
SELECT * FROM players WHERE team_id = $1 ORDER BY last_name, first_name;

-- name: GetPlayersByPosition :many
SELECT * FROM players WHERE position = $1 ORDER BY last_name, first_name;

-- name: GetPlayersByBirthplace :many
-- Players matching a birthplace filter, joined with their NHL regular-
-- season career totals (league_abbrev='NHL', game_type=2). Default
-- ordering is career_points DESC so the same query answers both
-- "list everyone from X" (with a large limit) and "top N from X"
-- (with a small limit). birth_city / birth_state_province use ILIKE
-- so callers can pass partial matches (e.g., 'Montr%' covers
-- 'Montréal' / 'Montreal-Nord' / etc.). Goalies appear at the
-- bottom (low career points); for goalie-specific ranking, sort the
-- result client-side or use a goalie-stat tool.
WITH filtered AS (
    SELECT DISTINCT ON (sr.player_id)
        sr.player_id,
        sr.birth_date,
        sr.birth_city,
        sr.birth_state_province,
        sr.birth_country
    FROM season_rosters sr
    WHERE sr.birth_country = sqlc.arg('birth_country')::text
      AND (sqlc.narg('birth_city')::text IS NULL
           OR sr.birth_city ILIKE sqlc.narg('birth_city')::text)
      AND (sqlc.narg('birth_state_province')::text IS NULL
           OR sr.birth_state_province ILIKE sqlc.narg('birth_state_province')::text)
    ORDER BY sr.player_id, sr.season DESC
),
career AS (
    SELECT
        player_id,
        SUM(games_played)::bigint AS career_games,
        SUM(goals)::bigint        AS career_goals,
        SUM(assists)::bigint      AS career_assists,
        SUM(points)::bigint       AS career_points,
        SUM(pim)::bigint          AS career_pim
    FROM player_season_totals
    WHERE league_abbrev = 'NHL' AND game_type = 'regular_season'
    GROUP BY player_id
)
SELECT
    p.id,
    p.first_name,
    p.last_name,
    p.position,
    f.birth_date,
    f.birth_city,
    f.birth_state_province,
    f.birth_country,
    COALESCE(c.career_games, 0)::bigint   AS career_games,
    COALESCE(c.career_goals, 0)::bigint   AS career_goals,
    COALESCE(c.career_assists, 0)::bigint AS career_assists,
    COALESCE(c.career_points, 0)::bigint  AS career_points,
    COALESCE(c.career_pim, 0)::bigint     AS career_pim
FROM filtered f
JOIN players p ON p.id = f.player_id
LEFT JOIN career c ON c.player_id = f.player_id
ORDER BY COALESCE(c.career_points, 0) DESC, p.last_name, p.first_name
LIMIT sqlc.arg('limit_n')::int;

-- name: SearchPlayersByName :many
-- Single-term player search with relevance ranking (exact > prefix > substring)
SELECT * FROM players
WHERE first_name_normalized LIKE '%' || lower($1) || '%'
   OR last_name_normalized LIKE '%' || lower($1) || '%'
   OR first_name ILIKE '%' || $1 || '%'
   OR last_name ILIKE '%' || $1 || '%'
ORDER BY
  CASE
    WHEN first_name_normalized = lower($1) OR last_name_normalized = lower($1) THEN 0
    WHEN first_name_normalized LIKE lower($1) || '%' OR last_name_normalized LIKE lower($1) || '%' THEN 1
    ELSE 2
  END,
  last_name, first_name
LIMIT 500;

-- name: SearchPlayersByFullName :many
-- Two-term player search matching first+last name in either order
SELECT * FROM players
WHERE (first_name_normalized LIKE '%' || lower($1) || '%' AND last_name_normalized LIKE '%' || lower($2) || '%')
   OR (first_name_normalized LIKE '%' || lower($2) || '%' AND last_name_normalized LIKE '%' || lower($1) || '%')
   OR (first_name ILIKE '%' || $1 || '%' AND last_name ILIKE '%' || $2 || '%')
   OR (first_name ILIKE '%' || $2 || '%' AND last_name ILIKE '%' || $1 || '%')
ORDER BY
  CASE
    WHEN (first_name_normalized = lower($1) AND last_name_normalized = lower($2))
      OR (first_name_normalized = lower($2) AND last_name_normalized = lower($1)) THEN 0
    WHEN (first_name_normalized LIKE lower($1) || '%' AND last_name_normalized LIKE lower($2) || '%')
      OR (first_name_normalized LIKE lower($2) || '%' AND last_name_normalized LIKE lower($1) || '%') THEN 1
    ELSE 2
  END,
  last_name, first_name
LIMIT 500;

-- name: ListPlayers :many
SELECT * FROM players
WHERE
    (sqlc.narg('name')::text IS NULL OR
     first_name ILIKE '%' || sqlc.narg('name') || '%' OR
     last_name ILIKE '%' || sqlc.narg('name') || '%')
    AND (sqlc.narg('sweater_number')::int IS NULL OR sweater_number = sqlc.narg('sweater_number'))
    AND (sqlc.narg('has_yahoo_id')::boolean IS NULL OR
         (CASE WHEN sqlc.narg('has_yahoo_id') THEN yahoo_id IS NOT NULL ELSE yahoo_id IS NULL END))
    AND (sqlc.narg('team_id')::bigint IS NULL OR team_id = sqlc.narg('team_id'))
    AND (sqlc.narg('position')::text IS NULL OR position = sqlc.narg('position'))
    AND (sqlc.narg('is_active')::boolean IS NULL OR is_active = sqlc.narg('is_active'))
ORDER BY last_name, first_name;

-- name: UpsertPlayer :exec
INSERT INTO players (
    id, yahoo_id, first_name, last_name, first_name_normalized, last_name_normalized, team_id,
    position, shoots_catches, height_inches, weight_pounds,
    birth_date, birth_city, birth_state_province, birth_country,
    sweater_number, is_active, headshot_url, hero_image_url,
    yahoo_image,
    yahoo_home_url, player_slug,
    draft_year, draft_team_abbrev, draft_round, draft_pick_in_round, draft_overall_pick
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11,
    $12, $13, $14, $15,
    $16, $17, $18, $19,
    $20,
    $21, $22,
    $23, $24, $25, $26, $27
)
ON CONFLICT (id) DO UPDATE SET
    yahoo_id = COALESCE(EXCLUDED.yahoo_id, players.yahoo_id),
    first_name = EXCLUDED.first_name,
    last_name = EXCLUDED.last_name,
    first_name_normalized = EXCLUDED.first_name_normalized,
    last_name_normalized = EXCLUDED.last_name_normalized,
    team_id = EXCLUDED.team_id,
    position = EXCLUDED.position,
    shoots_catches = EXCLUDED.shoots_catches,
    height_inches = COALESCE(EXCLUDED.height_inches, players.height_inches),
    weight_pounds = COALESCE(EXCLUDED.weight_pounds, players.weight_pounds),
    birth_date = COALESCE(EXCLUDED.birth_date, players.birth_date),
    birth_city = COALESCE(EXCLUDED.birth_city, players.birth_city),
    birth_state_province = COALESCE(EXCLUDED.birth_state_province, players.birth_state_province),
    birth_country = COALESCE(EXCLUDED.birth_country, players.birth_country),
    sweater_number = COALESCE(EXCLUDED.sweater_number, players.sweater_number),
    is_active = EXCLUDED.is_active,
    headshot_url = CASE WHEN EXCLUDED.headshot_url != '' THEN EXCLUDED.headshot_url ELSE players.headshot_url END,
    hero_image_url = COALESCE(EXCLUDED.hero_image_url, players.hero_image_url),
    yahoo_image = CASE WHEN EXCLUDED.yahoo_image != '' THEN EXCLUDED.yahoo_image ELSE players.yahoo_image END,
    yahoo_home_url = CASE WHEN EXCLUDED.yahoo_home_url != '' THEN EXCLUDED.yahoo_home_url ELSE players.yahoo_home_url END,
    player_slug = COALESCE(EXCLUDED.player_slug, players.player_slug),
    draft_year = COALESCE(EXCLUDED.draft_year, players.draft_year),
    draft_team_abbrev = COALESCE(EXCLUDED.draft_team_abbrev, players.draft_team_abbrev),
    draft_round = COALESCE(EXCLUDED.draft_round, players.draft_round),
    draft_pick_in_round = COALESCE(EXCLUDED.draft_pick_in_round, players.draft_pick_in_round),
    draft_overall_pick = COALESCE(EXCLUDED.draft_overall_pick, players.draft_overall_pick);

-- name: UpsertPlayerFromNHL :exec
-- Use this when importing from NHL API (has NHL ID as primary)
INSERT INTO players (
    id, first_name, last_name, team_id,
    position, shoots_catches, height_inches, weight_pounds,
    birth_date, birth_city, birth_state_province, birth_country,
    sweater_number, is_active, headshot_url, hero_image_url, player_slug,
    draft_year, draft_team_abbrev, draft_round, draft_pick_in_round, draft_overall_pick
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7, $8,
    $9, $10, $11, $12,
    $13, $14, $15, $16, $17,
    $18, $19, $20, $21, $22
)
ON CONFLICT (id) DO UPDATE SET
    first_name = EXCLUDED.first_name,
    last_name = EXCLUDED.last_name,
    team_id = EXCLUDED.team_id,
    position = EXCLUDED.position,
    shoots_catches = EXCLUDED.shoots_catches,
    height_inches = EXCLUDED.height_inches,
    weight_pounds = EXCLUDED.weight_pounds,
    birth_date = EXCLUDED.birth_date,
    birth_city = EXCLUDED.birth_city,
    birth_state_province = EXCLUDED.birth_state_province,
    birth_country = EXCLUDED.birth_country,
    sweater_number = EXCLUDED.sweater_number,
    is_active = EXCLUDED.is_active,
    headshot_url = EXCLUDED.headshot_url,
    hero_image_url = EXCLUDED.hero_image_url,
    player_slug = EXCLUDED.player_slug,
    draft_year = EXCLUDED.draft_year,
    draft_team_abbrev = EXCLUDED.draft_team_abbrev,
    draft_round = EXCLUDED.draft_round,
    draft_pick_in_round = EXCLUDED.draft_pick_in_round,
    draft_overall_pick = EXCLUDED.draft_overall_pick
WHERE (players.first_name, players.last_name, players.team_id,
       players.position, players.shoots_catches,
       players.height_inches, players.weight_pounds,
       players.birth_date, players.birth_city,
       players.birth_state_province, players.birth_country,
       players.sweater_number, players.is_active,
       players.headshot_url, players.hero_image_url, players.player_slug,
       players.draft_year, players.draft_team_abbrev,
       players.draft_round, players.draft_pick_in_round,
       players.draft_overall_pick)
      IS DISTINCT FROM
      (EXCLUDED.first_name, EXCLUDED.last_name, EXCLUDED.team_id,
       EXCLUDED.position, EXCLUDED.shoots_catches,
       EXCLUDED.height_inches, EXCLUDED.weight_pounds,
       EXCLUDED.birth_date, EXCLUDED.birth_city,
       EXCLUDED.birth_state_province, EXCLUDED.birth_country,
       EXCLUDED.sweater_number, EXCLUDED.is_active,
       EXCLUDED.headshot_url, EXCLUDED.hero_image_url, EXCLUDED.player_slug,
       EXCLUDED.draft_year, EXCLUDED.draft_team_abbrev,
       EXCLUDED.draft_round, EXCLUDED.draft_pick_in_round,
       EXCLUDED.draft_overall_pick);

-- name: UpdatePlayerYahooInfo :exec
-- Use this when importing from Yahoo API (updates Yahoo-specific fields)
UPDATE players SET
    yahoo_id = $2,
    yahoo_image = $3,
    yahoo_home_url = $4
WHERE id = $1;

-- name: LinkYahooToNHLPlayer :exec
-- Link a Yahoo player ID to an existing NHL player
UPDATE players SET yahoo_id = $2 WHERE id = $1;

-- name: ClearConflictingYahooID :exec
-- Clear a yahoo_id from any player except the one we're about to assign it to.
-- This handles cases where a yahoo_id was previously assigned to the wrong player.
UPDATE players SET yahoo_id = NULL WHERE yahoo_id = $1 AND id != $2;

-- name: EnsurePlayerExistsBatch :batchexec
-- Create stub player records for players not yet in the database.
-- Uses DO NOTHING to avoid overwriting existing player data.
-- Called before season roster import to satisfy the FK constraint.
INSERT INTO players (
    id, first_name, last_name, first_name_normalized, last_name_normalized,
    position, shoots_catches, headshot_url,
    height_inches, weight_pounds,
    birth_date, birth_city, birth_state_province, birth_country,
    sweater_number
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (id) DO NOTHING;

-- name: CountPlayers :one
SELECT COUNT(*) FROM players;

-- name: CountActivePlayers :one
SELECT COUNT(*) FROM players WHERE is_active = TRUE;

-- name: DeletePlayer :exec
DELETE FROM players WHERE id = $1;

-- name: ListPlayerHeadshots :many
SELECT id, headshot_url FROM players WHERE headshot_url <> '' ORDER BY id;

-- name: CountPlayerHeadshots :one
SELECT COUNT(*) FROM players WHERE headshot_url <> '';

-- name: ListPlayerHeroImages :many
SELECT id, hero_image_url FROM players WHERE hero_image_url IS NOT NULL AND hero_image_url <> '' ORDER BY id;

-- name: CountPlayerHeroImages :one
SELECT COUNT(*) FROM players WHERE hero_image_url IS NOT NULL AND hero_image_url <> '';

-- name: ListPlayerYahooImages :many
SELECT id, yahoo_image FROM players WHERE yahoo_image <> '' ORDER BY id;

-- name: CountPlayerYahooImages :one
SELECT COUNT(*) FROM players WHERE yahoo_image <> '';
