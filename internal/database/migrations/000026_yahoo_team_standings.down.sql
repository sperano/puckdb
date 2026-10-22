DROP TABLE yahoo_team_summary_stats;

DROP INDEX idx_yahoo_team_summaries_standings_date;

ALTER TABLE yahoo_team_summaries
    DROP CONSTRAINT yahoo_team_summaries_standings_snapshot_check,
    DROP CONSTRAINT yahoo_team_summaries_standings_rank_check,
    DROP CONSTRAINT yahoo_team_summaries_standings_playoff_seed_check,
    DROP CONSTRAINT yahoo_team_summaries_h2h_record_check,
    DROP CONSTRAINT yahoo_team_summaries_h2h_division_record_check,
    DROP CONSTRAINT yahoo_team_summaries_h2h_percentage_check,
    DROP COLUMN standings_captured_at,
    DROP COLUMN standings_rank,
    DROP COLUMN standings_playoff_seed,
    DROP COLUMN h2h_wins,
    DROP COLUMN h2h_losses,
    DROP COLUMN h2h_ties,
    DROP COLUMN h2h_percentage,
    DROP COLUMN h2h_division_wins,
    DROP COLUMN h2h_division_losses,
    DROP COLUMN h2h_division_ties,
    DROP COLUMN h2h_division_percentage;
