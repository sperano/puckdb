-- =============================================================================
-- Yahoo Transactions Queries
-- =============================================================================

-- name: UpsertYahooTransactionBatch :batchexec
INSERT INTO yahoo_transactions (
    league_id, transaction_key, type, timestamp, status
)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (league_id, transaction_key) DO UPDATE SET
    type = EXCLUDED.type,
    timestamp = EXCLUDED.timestamp,
    status = EXCLUDED.status
WHERE (yahoo_transactions.type, yahoo_transactions.timestamp,
       yahoo_transactions.status)
      IS DISTINCT FROM
      (EXCLUDED.type, EXCLUDED.timestamp,
       EXCLUDED.status);

-- name: GetYahooTransactionsByLeague :many
SELECT * FROM yahoo_transactions
WHERE league_id = $1
ORDER BY timestamp DESC;

-- name: GetYahooTransactionsByType :many
SELECT * FROM yahoo_transactions
WHERE league_id = $1 AND type = $2
ORDER BY timestamp DESC;

-- name: CountYahooTransactions :one
SELECT COUNT(*) FROM yahoo_transactions;

-- name: CountYahooTransactionsByLeague :one
SELECT COUNT(*) FROM yahoo_transactions WHERE league_id = $1;

-- =============================================================================
-- Yahoo Transaction Players Queries
-- =============================================================================

-- name: UpsertYahooTransactionPlayerBatch :batchexec
INSERT INTO yahoo_transaction_players (
    league_id, transaction_key, player_id, player_key,
    type, source_type, source_team_key,
    destination_type, destination_team_key
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (league_id, transaction_key, player_id) DO UPDATE SET
    player_key = EXCLUDED.player_key,
    type = EXCLUDED.type,
    source_type = EXCLUDED.source_type,
    source_team_key = EXCLUDED.source_team_key,
    destination_type = EXCLUDED.destination_type,
    destination_team_key = EXCLUDED.destination_team_key
WHERE (yahoo_transaction_players.player_key,
       yahoo_transaction_players.type,
       yahoo_transaction_players.source_type,
       yahoo_transaction_players.source_team_key,
       yahoo_transaction_players.destination_type,
       yahoo_transaction_players.destination_team_key)
      IS DISTINCT FROM
      (EXCLUDED.player_key,
       EXCLUDED.type,
       EXCLUDED.source_type,
       EXCLUDED.source_team_key,
       EXCLUDED.destination_type,
       EXCLUDED.destination_team_key);

-- name: GetYahooTransactionPlayersByTransaction :many
SELECT * FROM yahoo_transaction_players
WHERE league_id = $1 AND transaction_key = $2
ORDER BY player_id;

-- name: GetYahooTransactionPlayersByPlayer :many
SELECT * FROM yahoo_transaction_players
WHERE player_id = $1
ORDER BY league_id, transaction_key;

-- name: CountYahooTransactionPlayers :one
SELECT COUNT(*) FROM yahoo_transaction_players;
