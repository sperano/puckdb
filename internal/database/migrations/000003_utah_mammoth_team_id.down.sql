-- Data-only correction: 000003 rewrites team IDs to match what the NHL API
-- reports, and restoring the wrong ID would re-break the season_teams joins.
-- Nothing to undo.
SELECT 1;
