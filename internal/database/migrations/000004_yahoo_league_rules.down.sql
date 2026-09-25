DROP TABLE IF EXISTS yahoo_league_players;
DROP TABLE IF EXISTS yahoo_league_rule_snapshots;

ALTER TABLE yahoo_league_stat_categories
    DROP COLUMN IF EXISTS is_only_display_stat,
    DROP COLUMN IF EXISTS sort_order,
    DROP COLUMN IF EXISTS position_type,
    DROP COLUMN IF EXISTS display_name;
