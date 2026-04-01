-- =============================================================================
-- Game Broadcasts Queries
-- =============================================================================

-- name: UpsertGameBroadcastBatch :batchexec
INSERT INTO game_broadcasts (
    game_id, broadcast_id, market, country_code, network, sequence_number
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (game_id, broadcast_id) DO UPDATE SET
    market = EXCLUDED.market,
    country_code = EXCLUDED.country_code,
    network = EXCLUDED.network,
    sequence_number = EXCLUDED.sequence_number
WHERE (game_broadcasts.market, game_broadcasts.country_code,
       game_broadcasts.network, game_broadcasts.sequence_number)
      IS DISTINCT FROM
      (EXCLUDED.market, EXCLUDED.country_code,
       EXCLUDED.network, EXCLUDED.sequence_number);

-- name: GetGameBroadcasts :many
SELECT * FROM game_broadcasts
WHERE game_id = $1
ORDER BY sequence_number;

-- name: CountGameBroadcasts :one
SELECT COUNT(*) FROM game_broadcasts;
