-- Reverse: restore the JSONB players column and drop the normalized table.

-- Step 1: Add back the JSONB column
ALTER TABLE yahoo_transactions ADD COLUMN players JSONB;

-- Step 2: Rebuild JSONB from normalized rows
-- Note: this reconstructs a minimal JSON structure (no XMLName/Name fields).
UPDATE yahoo_transactions t SET players = sub.players_json
FROM (
    SELECT
        tp.league_id,
        tp.transaction_key,
        jsonb_agg(jsonb_build_object(
            'ID', tp.player_id,
            'Key', tp.player_key,
            'TransactionData', jsonb_build_object(
                'Type', tp.type,
                'SourceType', tp.source_type,
                'SourceTeamKey', tp.source_team_key,
                'DestinationType', tp.destination_type,
                'DestinationTeamKey', tp.destination_team_key
            )
        )) AS players_json
    FROM yahoo_transaction_players tp
    GROUP BY tp.league_id, tp.transaction_key
) sub
WHERE t.league_id = sub.league_id
  AND t.transaction_key = sub.transaction_key;

-- Step 3: Drop the normalized table
DROP TABLE IF EXISTS yahoo_transaction_players;
