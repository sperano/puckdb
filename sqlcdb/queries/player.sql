-- name: GetPlayer :one
SELECT p.*, t.city as team_city, t.name as team_name, t.abbreviation as team_abbrev
FROM players p
LEFT JOIN nhl_teams t ON p.nhl_team_id = t.id
WHERE p.id = $1;

-- name: GetPlayerByYahooID :one
SELECT p.*, t.city as team_city, t.name as team_name, t.abbreviation as team_abbrev
FROM players p
LEFT JOIN nhl_teams t ON p.nhl_team_id = t.id
WHERE p.yahoo_id = $1;

-- name: GetAllPlayers :many
SELECT p.*, t.city as team_city, t.name as team_name, t.abbreviation as team_abbrev
FROM players p
LEFT JOIN nhl_teams t ON p.nhl_team_id = t.id
ORDER BY p.last_name, p.first_name;

-- name: GetActivePlayers :many
SELECT p.*, t.city as team_city, t.name as team_name, t.abbreviation as team_abbrev
FROM players p
LEFT JOIN nhl_teams t ON p.nhl_team_id = t.id
WHERE p.is_active = TRUE
ORDER BY p.last_name, p.first_name;

-- name: GetPlayersByTeam :many
SELECT p.*, t.city as team_city, t.name as team_name, t.abbreviation as team_abbrev
FROM players p
LEFT JOIN nhl_teams t ON p.nhl_team_id = t.id
WHERE p.nhl_team_id = $1
ORDER BY p.last_name, p.first_name;

-- name: GetPlayersByPosition :many
SELECT p.*, t.city as team_city, t.name as team_name, t.abbreviation as team_abbrev
FROM players p
LEFT JOIN nhl_teams t ON p.nhl_team_id = t.id
WHERE p.position = $1
ORDER BY p.last_name, p.first_name;

-- name: SearchPlayersByName :many
SELECT p.*, t.city as team_city, t.name as team_name, t.abbreviation as team_abbrev
FROM players p
LEFT JOIN nhl_teams t ON p.nhl_team_id = t.id
WHERE p.last_name ILIKE $1 OR p.first_name ILIKE $1
ORDER BY p.last_name, p.first_name
LIMIT 50;

-- name: UpsertPlayer :exec
INSERT INTO players (
    id, yahoo_id, first_name, last_name, nhl_team_id,
    position, shoots_catches, height_inches, weight_pounds,
    birth_date, birth_city, birth_state_province, birth_country,
    sweater_number, is_active, headshot_url, hero_image_url,
    yahoo_image_small, yahoo_image_medium, yahoo_image_large,
    yahoo_home_url, player_slug,
    draft_year, draft_team_abbrev, draft_round, draft_pick_in_round, draft_overall_pick
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12, $13,
    $14, $15, $16, $17,
    $18, $19, $20,
    $21, $22,
    $23, $24, $25, $26, $27
)
ON CONFLICT (id) DO UPDATE SET
    yahoo_id = COALESCE(EXCLUDED.yahoo_id, players.yahoo_id),
    first_name = EXCLUDED.first_name,
    last_name = EXCLUDED.last_name,
    nhl_team_id = EXCLUDED.nhl_team_id,
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
    yahoo_image_small = CASE WHEN EXCLUDED.yahoo_image_small != '' THEN EXCLUDED.yahoo_image_small ELSE players.yahoo_image_small END,
    yahoo_image_medium = CASE WHEN EXCLUDED.yahoo_image_medium != '' THEN EXCLUDED.yahoo_image_medium ELSE players.yahoo_image_medium END,
    yahoo_image_large = CASE WHEN EXCLUDED.yahoo_image_large != '' THEN EXCLUDED.yahoo_image_large ELSE players.yahoo_image_large END,
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
    id, first_name, last_name, nhl_team_id,
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
    nhl_team_id = EXCLUDED.nhl_team_id,
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
    draft_overall_pick = EXCLUDED.draft_overall_pick;

-- name: UpdatePlayerYahooInfo :exec
-- Use this when importing from Yahoo API (updates Yahoo-specific fields)
UPDATE players SET
    yahoo_id = $2,
    yahoo_image_small = $3,
    yahoo_image_medium = $4,
    yahoo_image_large = $5,
    yahoo_home_url = $6
WHERE id = $1;

-- name: LinkYahooToNHLPlayer :exec
-- Link a Yahoo player ID to an existing NHL player
UPDATE players SET yahoo_id = $2 WHERE id = $1;

-- name: CountPlayers :one
SELECT COUNT(*) FROM players;

-- name: CountActivePlayers :one
SELECT COUNT(*) FROM players WHERE is_active = TRUE;

-- name: DeletePlayer :exec
DELETE FROM players WHERE id = $1;
