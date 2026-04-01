-- Rollback: remove new data source tables and column additions

-- Drop column additions (reverse order)
ALTER TABLE yahoo_team_rosters
    DROP COLUMN IF EXISTS editorial_team_abbr,
    DROP COLUMN IF EXISTS uniform_number,
    DROP COLUMN IF EXISTS eligible_positions,
    DROP COLUMN IF EXISTS primary_position,
    DROP COLUMN IF EXISTS display_position,
    DROP COLUMN IF EXISTS position_type,
    DROP COLUMN IF EXISTS on_disabled_list,
    DROP COLUMN IF EXISTS injury_note,
    DROP COLUMN IF EXISTS player_status_full,
    DROP COLUMN IF EXISTS player_status;

ALTER TABLE game_goalie_stats
    DROP COLUMN IF EXISTS shorthanded_shots_against,
    DROP COLUMN IF EXISTS power_play_shots_against,
    DROP COLUMN IF EXISTS even_strength_shots_against;

-- Drop new tables (reverse creation order)
DROP TABLE IF EXISTS yahoo_matchups;
DROP TABLE IF EXISTS yahoo_draft_results;
DROP TABLE IF EXISTS yahoo_transactions;
DROP TABLE IF EXISTS game_broadcasts;
DROP TABLE IF EXISTS player_season_totals;
DROP TABLE IF EXISTS player_awards;
DROP TABLE IF EXISTS club_goalie_stats;
DROP TABLE IF EXISTS club_skater_stats;
DROP TABLE IF EXISTS season_rosters;
DROP TABLE IF EXISTS standings_snapshots;
