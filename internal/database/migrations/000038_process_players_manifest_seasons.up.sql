-- The seasons (start years) whose boxscore players the consolidated list a
-- ProcessPlayers manifest copied was extracted from. Phase 3 verifies only
-- the unmatched Yahoo IDs whose game player lists name one of them. NULL:
-- unknown (a manifest created before this column, or from a list saved
-- without its seasons), so every unmatched Yahoo ID is verified.
ALTER TABLE process_players_manifests ADD COLUMN seasons integer[];
