-- name: InsertProcessPlayersManifest :one
-- Returns no row when a manifest with this id already exists (a retried or
-- overlapping creation); the caller then reads the committed one.
INSERT INTO process_players_manifests (id, workflow_id, player_count, players_hash)
VALUES (sqlc.arg(id), sqlc.arg(workflow_id), sqlc.arg(player_count), sqlc.arg(players_hash))
ON CONFLICT (id) DO NOTHING
RETURNING id;

-- name: GetProcessPlayersManifest :one
SELECT id, player_count, players_hash
FROM process_players_manifests
WHERE id = sqlc.arg(id);

-- name: CopyProcessPlayersManifestPlayers :copyfrom
INSERT INTO process_players_manifest_players (manifest_id, ordinal, player_id, first_name, last_name, position)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListProcessPlayersManifestPlayers :many
-- The players at ordinals [from_ordinal, to_ordinal), in ordinal order.
SELECT ordinal, player_id, first_name, last_name, position
FROM process_players_manifest_players
WHERE manifest_id = sqlc.arg(manifest_id)
  AND ordinal >= sqlc.arg(from_ordinal)
  AND ordinal < sqlc.arg(to_ordinal)
ORDER BY ordinal;

-- name: DeleteOldProcessPlayersManifests :execrows
-- Keeps the newest keep_count manifests; their players and errors go with
-- the deleted ones through ON DELETE CASCADE.
DELETE FROM process_players_manifests
WHERE id NOT IN (
    SELECT m.id FROM process_players_manifests m
    ORDER BY m.created_at DESC, m.id DESC
    LIMIT sqlc.arg(keep_count)
);

-- name: UpsertProcessPlayersManifestErrors :exec
-- One row per (player, stage); the arrays are parallel and must not repeat a
-- (player, stage) pair.
INSERT INTO process_players_manifest_errors (manifest_id, player_id, stage, message)
SELECT sqlc.arg(manifest_id),
       unnest(sqlc.arg(player_ids)::bigint[]),
       unnest(sqlc.arg(stages)::text[]),
       unnest(sqlc.arg(messages)::text[])
ON CONFLICT (manifest_id, player_id, stage) DO UPDATE SET message = EXCLUDED.message;

-- name: FinishProcessPlayersManifest :one
-- Marks the manifest finished and returns how many errors it recorded; no
-- row when the manifest does not exist.
UPDATE process_players_manifests m
SET finished_at = now()
WHERE m.id = sqlc.arg(id)
RETURNING (SELECT count(*) FROM process_players_manifest_errors e WHERE e.manifest_id = m.id)::integer AS error_count;

-- name: ListMatchedYahooIDs :many
SELECT yahoo_id::bigint AS yahoo_id
FROM players
WHERE yahoo_id IS NOT NULL;
