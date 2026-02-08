-- name: GetAllNHLFranchises :many
SELECT id, full_name, team_common_name, team_place_name
FROM nhl_franchises
ORDER BY id;

-- name: GetNHLFranchise :one
SELECT id, full_name, team_common_name, team_place_name
FROM nhl_franchises
WHERE id = $1;

-- name: UpsertNHLFranchise :exec
INSERT INTO nhl_franchises (id, full_name, team_common_name, team_place_name)
VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE SET
    full_name = EXCLUDED.full_name,
    team_common_name = EXCLUDED.team_common_name,
    team_place_name = EXCLUDED.team_place_name;

-- name: CountNHLFranchises :one
SELECT COUNT(*) FROM nhl_franchises;
