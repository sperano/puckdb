-- =============================================================================
-- Season Rosters Queries
-- =============================================================================

-- name: UpsertSeasonRosterBatch :batchexec
INSERT INTO season_rosters (
    season, team_id, player_id,
    position, shoots_catches, sweater_number,
    height_inches, weight_pounds,
    birth_date, birth_city, birth_state_province, birth_country
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (season, team_id, player_id) DO UPDATE SET
    position = EXCLUDED.position,
    shoots_catches = EXCLUDED.shoots_catches,
    sweater_number = EXCLUDED.sweater_number,
    height_inches = EXCLUDED.height_inches,
    weight_pounds = EXCLUDED.weight_pounds,
    birth_date = EXCLUDED.birth_date,
    birth_city = EXCLUDED.birth_city,
    birth_state_province = EXCLUDED.birth_state_province,
    birth_country = EXCLUDED.birth_country,
    updated_at = NOW()
WHERE (season_rosters.position, season_rosters.shoots_catches,
       season_rosters.sweater_number, season_rosters.height_inches,
       season_rosters.weight_pounds, season_rosters.birth_date,
       season_rosters.birth_city, season_rosters.birth_state_province,
       season_rosters.birth_country)
      IS DISTINCT FROM
      (EXCLUDED.position, EXCLUDED.shoots_catches,
       EXCLUDED.sweater_number, EXCLUDED.height_inches,
       EXCLUDED.weight_pounds, EXCLUDED.birth_date,
       EXCLUDED.birth_city, EXCLUDED.birth_state_province,
       EXCLUDED.birth_country);

-- name: GetSeasonRosterByTeam :many
SELECT r.*, p.first_name, p.last_name
FROM season_rosters r
JOIN players p ON r.player_id = p.id
WHERE r.season = $1 AND r.team_id = $2
ORDER BY r.position, p.last_name;

-- name: GetSeasonRosterByPlayer :many
SELECT r.*, st.full_name as team_name, st.abbrev as team_abbrev
FROM season_rosters r
JOIN season_teams st ON st.season_id = r.season AND st.team_id = r.team_id
WHERE r.player_id = $1
ORDER BY r.season DESC;

-- name: CountSeasonRosters :one
SELECT COUNT(*) FROM season_rosters;

-- name: CountSeasonRostersBySeason :one
SELECT COUNT(*) FROM season_rosters WHERE season = $1;

-- name: GetSeasonTeamAbbrevs :many
SELECT team_id, abbrev FROM season_teams
WHERE season_id = $1
ORDER BY abbrev;
