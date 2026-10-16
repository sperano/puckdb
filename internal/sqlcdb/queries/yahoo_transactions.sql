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

-- name: ListYahooTransactionMoves :many
-- One row per player moved, for the newest row_limit transactions of a
-- league that match the filters; the limit counts transactions, so a trade
-- or an add/drop is never cut in half. move_type narrows to transactions of
-- that type or players moved that way ("add" also finds the add of an
-- add/drop); since_ts (Unix seconds) drops older transactions and those
-- without a timestamp. Team names come from yahoo_teams, the player name
-- from the league's Yahoo pool, else from the matched NHL player.
WITH picked AS (
    SELECT t.league_id, t.transaction_key, t.type, t.timestamp, t.status
    FROM yahoo_transactions t
    WHERE t.league_id = @league_id
      AND (sqlc.narg(since_ts)::bigint IS NULL OR t.timestamp >= sqlc.narg(since_ts)::bigint)
      AND EXISTS (
          SELECT 1 FROM yahoo_transaction_players mp
          WHERE mp.league_id = t.league_id
            AND mp.transaction_key = t.transaction_key
            AND (sqlc.narg(move_type)::text IS NULL
                 OR t.type = sqlc.narg(move_type)::text
                 OR mp.type = sqlc.narg(move_type)::text))
    ORDER BY t.timestamp DESC NULLS LAST, t.transaction_key DESC
    LIMIT @row_limit::integer
)
SELECT
    p.timestamp,
    p.transaction_key,
    p.type AS transaction_type,
    p.status,
    tp.type AS move_type,
    tp.player_id AS yahoo_player_id,
    pl.id AS nhl_player_id,
    COALESCE(NULLIF(lp.full_name, ''), pl.first_name || ' ' || pl.last_name, '')::text AS player_name,
    tp.source_type,
    src.id AS source_team_id,
    COALESCE(src.name, '')::text AS source_team_name,
    tp.source_team_key,
    tp.destination_type,
    dst.id AS destination_team_id,
    COALESCE(dst.name, '')::text AS destination_team_name,
    tp.destination_team_key
FROM picked p
JOIN yahoo_transaction_players tp
  ON tp.league_id = p.league_id AND tp.transaction_key = p.transaction_key
JOIN yahoo_leagues l ON l.id = p.league_id
LEFT JOIN yahoo_league_players lp
  ON lp.league_key = l.league_key AND lp.player_id = tp.player_id
LEFT JOIN players pl ON pl.yahoo_id = tp.player_id
LEFT JOIN yahoo_teams src
  ON src.league_id = tp.league_id AND src.team_key = tp.source_team_key
LEFT JOIN yahoo_teams dst
  ON dst.league_id = tp.league_id AND dst.team_key = tp.destination_team_key
WHERE sqlc.narg(move_type)::text IS NULL
   OR p.type = sqlc.narg(move_type)::text
   OR tp.type = sqlc.narg(move_type)::text
ORDER BY p.timestamp DESC NULLS LAST, p.transaction_key DESC, tp.type, tp.player_id;
