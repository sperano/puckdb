-- Add normalized name columns for accent-insensitive searching
-- These columns store lowercase ASCII versions of names (accents stripped)

ALTER TABLE players
ADD COLUMN first_name_normalized TEXT NOT NULL DEFAULT '',
ADD COLUMN last_name_normalized TEXT NOT NULL DEFAULT '';

-- Populate the normalized columns using PostgreSQL's unaccent extension
-- Note: unaccent must be enabled in the database, or we fall back to lowercase only
DO $$
BEGIN
    -- Try to use unaccent if available
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'unaccent') THEN
        UPDATE players SET
            first_name_normalized = LOWER(unaccent(first_name)),
            last_name_normalized = LOWER(unaccent(last_name));
    ELSE
        -- Fallback: just lowercase (better than nothing)
        UPDATE players SET
            first_name_normalized = LOWER(first_name),
            last_name_normalized = LOWER(last_name);
    END IF;
END $$;

-- Index for fast lookups on normalized names
CREATE INDEX IF NOT EXISTS idx_players_name_normalized ON players(last_name_normalized, first_name_normalized);
