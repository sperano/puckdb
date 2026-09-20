-- name: GetAllFranchises :many
SELECT id, full_name, team_common_name, team_place_name
FROM franchises
ORDER BY id;

-- name: GetFranchise :one
SELECT id, full_name, team_common_name, team_place_name
FROM franchises
WHERE id = $1;

-- name: UpsertFranchise :exec
INSERT INTO franchises (id, full_name, team_common_name, team_place_name)
VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE SET
    full_name = EXCLUDED.full_name,
    team_common_name = EXCLUDED.team_common_name,
    team_place_name = EXCLUDED.team_place_name
WHERE (franchises.full_name, franchises.team_common_name, franchises.team_place_name)
      IS DISTINCT FROM
      (EXCLUDED.full_name, EXCLUDED.team_common_name, EXCLUDED.team_place_name);

-- name: CountFranchises :one
SELECT COUNT(*) FROM franchises;
