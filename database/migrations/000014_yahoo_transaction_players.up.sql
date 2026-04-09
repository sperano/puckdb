-- Normalize yahoo_transactions.players JSONB into a proper relational table.
--
-- Each transaction can involve 1-N players, each with a role (add, drop, trade)
-- and source/destination info. Previously stored as a JSON dump of the Go struct.

-- Step 1: Create the normalized table
CREATE TABLE IF NOT EXISTS yahoo_transaction_players (
    league_id INT NOT NULL,
    transaction_key TEXT NOT NULL,
    player_id INT NOT NULL,
    player_key TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL,
    source_type TEXT NOT NULL DEFAULT '',
    source_team_key TEXT NOT NULL DEFAULT '',
    destination_type TEXT NOT NULL DEFAULT '',
    destination_team_key TEXT NOT NULL DEFAULT '',

    PRIMARY KEY (league_id, transaction_key, player_id),
    FOREIGN KEY (league_id, transaction_key)
        REFERENCES yahoo_transactions(league_id, transaction_key) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_yahoo_transaction_players_player
    ON yahoo_transaction_players(player_id);

-- Step 2: Backfill from JSONB
INSERT INTO yahoo_transaction_players (
    league_id, transaction_key, player_id, player_key,
    type, source_type, source_team_key,
    destination_type, destination_team_key
)
SELECT
    t.league_id,
    t.transaction_key,
    (p->>'ID')::int AS player_id,
    COALESCE(p->>'Key', '') AS player_key,
    COALESCE(p->'TransactionData'->>'Type', '') AS type,
    COALESCE(p->'TransactionData'->>'SourceType', '') AS source_type,
    COALESCE(p->'TransactionData'->>'SourceTeamKey', '') AS source_team_key,
    COALESCE(p->'TransactionData'->>'DestinationType', '') AS destination_type,
    COALESCE(p->'TransactionData'->>'DestinationTeamKey', '') AS destination_team_key
FROM yahoo_transactions t
CROSS JOIN LATERAL jsonb_array_elements(t.players) AS p
WHERE t.players IS NOT NULL;

-- Step 3: Drop the JSONB column
ALTER TABLE yahoo_transactions DROP COLUMN players;
