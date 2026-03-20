-- Reverse migration: remove fantasy analysis views

DROP VIEW IF EXISTS goalie_recent_stats;
DROP VIEW IF EXISTS skater_recent_stats;
DROP VIEW IF EXISTS goalie_season_stats;
DROP VIEW IF EXISTS skater_season_stats;
DROP VIEW IF EXISTS yahoo_roto_standings;
DROP VIEW IF EXISTS yahoo_season_team_totals;
DROP VIEW IF EXISTS yahoo_roster_players;
