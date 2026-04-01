-- =============================================================================
-- Player Awards Queries
-- =============================================================================

-- name: UpsertPlayerAwardBatch :batchexec
INSERT INTO player_awards (player_id, trophy_name, season)
VALUES ($1, $2, $3)
ON CONFLICT (player_id, trophy_name, season) DO NOTHING;

-- name: GetPlayerAwards :many
SELECT pa.*, p.first_name, p.last_name
FROM player_awards pa
JOIN players p ON pa.player_id = p.id
WHERE pa.player_id = $1
ORDER BY pa.season DESC, pa.trophy_name;

-- name: GetAwardsBySeason :many
SELECT pa.*, p.first_name, p.last_name
FROM player_awards pa
JOIN players p ON pa.player_id = p.id
WHERE pa.season = $1
ORDER BY pa.trophy_name, p.last_name;

-- name: GetAwardsByTrophy :many
SELECT pa.*, p.first_name, p.last_name
FROM player_awards pa
JOIN players p ON pa.player_id = p.id
WHERE pa.trophy_name = $1
ORDER BY pa.season DESC;

-- name: CountPlayerAwards :one
SELECT COUNT(*) FROM player_awards;
