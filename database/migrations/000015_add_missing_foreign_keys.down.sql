-- Reverse: drop all foreign key constraints added in the up migration.

-- Step 5: Yahoo draft results
ALTER TABLE yahoo_draft_results DROP CONSTRAINT IF EXISTS yahoo_draft_results_league_team_fkey;
ALTER TABLE yahoo_draft_results DROP CONSTRAINT IF EXISTS yahoo_draft_results_league_id_fkey;

-- Step 4: Yahoo transactions
ALTER TABLE yahoo_transactions DROP CONSTRAINT IF EXISTS yahoo_transactions_league_id_fkey;

-- Step 3: Yahoo matchups
ALTER TABLE yahoo_matchups DROP CONSTRAINT IF EXISTS yahoo_matchups_team2_fkey;
ALTER TABLE yahoo_matchups DROP CONSTRAINT IF EXISTS yahoo_matchups_team1_fkey;
ALTER TABLE yahoo_matchups DROP CONSTRAINT IF EXISTS yahoo_matchups_league_id_fkey;

-- Step 2: season_rosters
ALTER TABLE season_rosters DROP CONSTRAINT IF EXISTS season_rosters_season_team_fkey;

-- Step 1: NHL club stats
ALTER TABLE club_goalie_stats DROP CONSTRAINT IF EXISTS club_goalie_stats_season_team_fkey;
ALTER TABLE club_skater_stats DROP CONSTRAINT IF EXISTS club_skater_stats_season_team_fkey;
