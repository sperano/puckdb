-- Reverse of 000017: Remove team_id, revert season format, remove western teams

ALTER TABLE player_season_totals DROP COLUMN team_id;

ALTER TABLE player_season_totals DROP CONSTRAINT player_season_totals_pkey;

UPDATE player_season_totals
SET season = (season - 1) / 10001
WHERE season > 100000;

ALTER TABLE player_season_totals
  ADD CONSTRAINT player_season_totals_pkey
  PRIMARY KEY (player_id, season, game_type, league_abbrev, sequence);

DELETE FROM season_teams WHERE team_id IN (70, 71, 72, 73, 74);
