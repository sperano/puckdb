-- =============================================================================
-- Yahoo Transactions Queries
-- =============================================================================

-- name: UpsertYahooTransactionBatch :batchexec
INSERT INTO yahoo_transactions (
    league_id, transaction_key, type, timestamp, status, players
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (league_id, transaction_key) DO UPDATE SET
    type = EXCLUDED.type,
    timestamp = EXCLUDED.timestamp,
    status = EXCLUDED.status,
    players = EXCLUDED.players
WHERE (yahoo_transactions.type, yahoo_transactions.timestamp,
       yahoo_transactions.status, yahoo_transactions.players)
      IS DISTINCT FROM
      (EXCLUDED.type, EXCLUDED.timestamp,
       EXCLUDED.status, EXCLUDED.players);

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
