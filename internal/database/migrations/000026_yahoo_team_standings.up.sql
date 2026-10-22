-- Head-to-head standings on the daily Yahoo team summaries, and every Yahoo
-- team-stat category of a summary as one normalized row.
--
-- A summary row of date D now carries two captures of that day:
--   * the category columns (goals ... blocks) and yahoo_team_summary_stats
--     hold the team's stats of D (resource.TeamSummary, stats;type=date);
--   * the standings columns hold the league standings snapshot captured on D
--     (resource.LeagueStandings: the standings at D's first capture, usually
--     through D-1's games). Yahoo serves current standings only, so a date
--     without a capture has no snapshot: standings_captured_at and every
--     other standings column are NULL, never filled from another day.
--
-- The standings columns are prefixed standings_/h2h_ so the head-to-head
-- record (h2h_wins) cannot be mistaken for the goalie wins category (wins).
ALTER TABLE yahoo_team_summaries
    -- When the standings file was captured (its modification time); NULL
    -- when the row has no standings snapshot.
    ADD COLUMN standings_captured_at timestamptz,
    -- 1-based rank; NULL when Yahoo serves none (before the first game).
    ADD COLUMN standings_rank integer,
    -- NULL when Yahoo serves no playoff seed.
    ADD COLUMN standings_playoff_seed integer,
    -- Head-to-head record (outcome_totals); NULL in leagues without one
    -- (rotisserie).
    ADD COLUMN h2h_wins integer,
    ADD COLUMN h2h_losses integer,
    ADD COLUMN h2h_ties integer,
    -- Yahoo's winning percentage as a fraction (.571 = 0.571); NULL when
    -- Yahoo serves none (before the first matchup is scored).
    ADD COLUMN h2h_percentage double precision,
    -- Record against division opponents (divisional_outcome_totals); NULL in
    -- leagues without divisions.
    ADD COLUMN h2h_division_wins integer,
    ADD COLUMN h2h_division_losses integer,
    ADD COLUMN h2h_division_ties integer,
    ADD COLUMN h2h_division_percentage double precision,
    ADD CONSTRAINT yahoo_team_summaries_standings_snapshot_check CHECK (
        standings_captured_at IS NOT NULL
        OR num_nonnulls(standings_rank, standings_playoff_seed,
                        h2h_wins, h2h_losses, h2h_ties, h2h_percentage,
                        h2h_division_wins, h2h_division_losses, h2h_division_ties,
                        h2h_division_percentage) = 0),
    ADD CONSTRAINT yahoo_team_summaries_standings_rank_check CHECK (standings_rank >= 1),
    ADD CONSTRAINT yahoo_team_summaries_standings_playoff_seed_check CHECK (standings_playoff_seed >= 1),
    ADD CONSTRAINT yahoo_team_summaries_h2h_record_check CHECK (
        num_nulls(h2h_wins, h2h_losses, h2h_ties) IN (0, 3)
        AND h2h_wins >= 0 AND h2h_losses >= 0 AND h2h_ties >= 0
        AND (h2h_percentage IS NULL OR h2h_wins IS NOT NULL)),
    ADD CONSTRAINT yahoo_team_summaries_h2h_division_record_check CHECK (
        num_nulls(h2h_division_wins, h2h_division_losses, h2h_division_ties) IN (0, 3)
        AND h2h_division_wins >= 0 AND h2h_division_losses >= 0 AND h2h_division_ties >= 0
        AND (h2h_division_percentage IS NULL OR h2h_division_wins IS NOT NULL)),
    ADD CONSTRAINT yahoo_team_summaries_h2h_percentage_check CHECK (
        h2h_percentage BETWEEN 0 AND 1 AND h2h_division_percentage BETWEEN 0 AND 1);

-- Latest standings snapshot of a league.
CREATE INDEX idx_yahoo_team_summaries_standings_date
    ON yahoo_team_summaries (league_id, date)
    WHERE standings_captured_at IS NOT NULL;

-- Every category value of a team summary, whatever its Yahoo stat ID, next
-- to the fixed category columns kept for existing consumers. The import
-- replaces a summary's whole set from its file, so a category the file no
-- longer lists leaves no row behind.
CREATE TABLE yahoo_team_summary_stats (
    league_id integer NOT NULL,
    team_id integer NOT NULL,
    date date NOT NULL,
    stat_id integer NOT NULL,
    -- The value as a number; NULL when Yahoo serves none ("-") or a text
    -- that is not a finite number.
    value double precision,
    -- The value exactly as Yahoo served it.
    raw_value text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (league_id, team_id, date, stat_id),
    FOREIGN KEY (league_id, team_id, date)
        REFERENCES yahoo_team_summaries (league_id, team_id, date) ON DELETE CASCADE,
    CHECK (stat_id >= 0),
    CHECK (value IS NULL OR value NOT IN ('NaN', 'Infinity', '-Infinity'))
);

-- A league's category values of one date, across its teams.
CREATE INDEX idx_yahoo_team_summary_stats_league_date
    ON yahoo_team_summary_stats (league_id, date);
