-- Add missing foreign key constraints to enforce referential integrity.
--
-- All FK columns were validated against production data (0 orphans).
-- Temporal workflows guarantee parent rows exist before children:
--   NHL: season_teams populated before club_stats/rosters
--   Yahoo: yahoo_leagues/yahoo_teams populated before matchups/transactions/drafts

-- Step 1: NHL club stats → season_teams
ALTER TABLE club_skater_stats
    ADD CONSTRAINT club_skater_stats_season_team_fkey
    FOREIGN KEY (season, team_id) REFERENCES season_teams(season_id, team_id);

ALTER TABLE club_goalie_stats
    ADD CONSTRAINT club_goalie_stats_season_team_fkey
    FOREIGN KEY (season, team_id) REFERENCES season_teams(season_id, team_id);

-- Step 2: season_rosters → season_teams
ALTER TABLE season_rosters
    ADD CONSTRAINT season_rosters_season_team_fkey
    FOREIGN KEY (season, team_id) REFERENCES season_teams(season_id, team_id);

-- Step 3: Yahoo matchups → yahoo_leagues + yahoo_teams
ALTER TABLE yahoo_matchups
    ADD CONSTRAINT yahoo_matchups_league_id_fkey
    FOREIGN KEY (league_id) REFERENCES yahoo_leagues(id);

ALTER TABLE yahoo_matchups
    ADD CONSTRAINT yahoo_matchups_team1_fkey
    FOREIGN KEY (league_id, team1_id) REFERENCES yahoo_teams(league_id, id);

ALTER TABLE yahoo_matchups
    ADD CONSTRAINT yahoo_matchups_team2_fkey
    FOREIGN KEY (league_id, team2_id) REFERENCES yahoo_teams(league_id, id);

-- Step 4: Yahoo transactions → yahoo_leagues
ALTER TABLE yahoo_transactions
    ADD CONSTRAINT yahoo_transactions_league_id_fkey
    FOREIGN KEY (league_id) REFERENCES yahoo_leagues(id);

-- Step 5: Yahoo draft results → yahoo_leagues + yahoo_teams
ALTER TABLE yahoo_draft_results
    ADD CONSTRAINT yahoo_draft_results_league_id_fkey
    FOREIGN KEY (league_id) REFERENCES yahoo_leagues(id);

ALTER TABLE yahoo_draft_results
    ADD CONSTRAINT yahoo_draft_results_league_team_fkey
    FOREIGN KEY (league_id, team_id) REFERENCES yahoo_teams(league_id, id);
