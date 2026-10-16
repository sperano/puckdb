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
-- without a timestamp. Same-second transactions order by key, the longer
-- key first so tr.10 comes before tr.9. Team names come from yahoo_teams,
-- the player name from the league's Yahoo pool, else from the matched NHL
-- player.
WITH moves AS (
    SELECT t.transaction_key, t.type AS transaction_type, t.timestamp, t.status,
           tp.league_id, tp.player_id, tp.type AS move_type,
           tp.source_type, tp.source_team_key, tp.destination_type, tp.destination_team_key
    FROM yahoo_transactions t
    JOIN yahoo_transaction_players tp
      ON tp.league_id = t.league_id AND tp.transaction_key = t.transaction_key
    WHERE t.league_id = @league_id
      AND (sqlc.narg(since_ts)::bigint IS NULL OR t.timestamp >= sqlc.narg(since_ts)::bigint)
      AND (sqlc.narg(move_type)::text IS NULL
           OR t.type = sqlc.narg(move_type)::text
           OR tp.type = sqlc.narg(move_type)::text)
),
picked AS (
    SELECT transaction_key, timestamp
    FROM moves
    GROUP BY transaction_key, timestamp
    ORDER BY timestamp DESC NULLS LAST, length(transaction_key) DESC, transaction_key DESC
    LIMIT @row_limit::integer
)
SELECT
    m.timestamp,
    m.transaction_key,
    m.transaction_type,
    m.status,
    m.move_type,
    m.player_id AS yahoo_player_id,
    pl.id AS nhl_player_id,
    COALESCE(NULLIF(lp.full_name, ''), pl.first_name || ' ' || pl.last_name, '')::text AS player_name,
    m.source_type,
    src.id AS source_team_id,
    COALESCE(src.name, '')::text AS source_team_name,
    m.source_team_key,
    m.destination_type,
    dst.id AS destination_team_id,
    COALESCE(dst.name, '')::text AS destination_team_name,
    m.destination_team_key
FROM moves m
JOIN picked p ON p.transaction_key = m.transaction_key
JOIN yahoo_leagues l ON l.id = m.league_id
LEFT JOIN yahoo_league_players lp
  ON lp.league_key = l.league_key AND lp.player_id = m.player_id
LEFT JOIN players pl ON pl.yahoo_id = m.player_id
LEFT JOIN yahoo_teams src
  ON src.league_id = m.league_id AND src.team_key = m.source_team_key
LEFT JOIN yahoo_teams dst
  ON dst.league_id = m.league_id AND dst.team_key = m.destination_team_key
ORDER BY m.timestamp DESC NULLS LAST, length(m.transaction_key) DESC, m.transaction_key DESC,
         m.move_type, m.player_id;
