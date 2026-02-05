-- Remove normalized name columns

DROP INDEX IF EXISTS idx_players_name_normalized;

ALTER TABLE players
DROP COLUMN IF EXISTS first_name_normalized,
DROP COLUMN IF EXISTS last_name_normalized;
