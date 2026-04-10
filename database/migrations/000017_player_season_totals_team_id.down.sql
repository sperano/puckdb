-- Reverse of 000017: Remove team_id, revert season format, restore team_id=0 rows

-- Drop team_id column (also drops the composite FK and index)
ALTER TABLE player_season_totals DROP COLUMN team_id;

-- Revert season from concatenated to start-year format
ALTER TABLE player_season_totals DROP CONSTRAINT player_season_totals_pkey;

UPDATE player_season_totals
SET season = (season - 1) / 10001;

ALTER TABLE player_season_totals
  ADD CONSTRAINT player_season_totals_pkey
  PRIMARY KEY (player_id, season, game_type, league_abbrev, sequence);

-- Remove western league teams
DELETE FROM season_teams WHERE team_id IN (70, 71, 72, 73, 74);

-- Note: Restoring team_id=0 duplicate rows and reverting Cleveland Barons
-- back to team_id=0 is not practical. The data quality fix is kept.
