-- Yahoo Team Data Schema
-- Stores team summaries (stats) and rosters for specific dates

-- =============================================================================
-- Team Summaries (team stats for a coverage date)
-- =============================================================================

CREATE TABLE IF NOT EXISTS yahoo_team_summaries (
    -- Composite primary key: team stats for a specific date
    league_id INT NOT NULL,
    team_id INT NOT NULL,
    date DATE NOT NULL,

    -- Coverage metadata
    coverage_type TEXT NOT NULL DEFAULT 'date',

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (league_id, team_id, date),
    FOREIGN KEY (league_id, team_id) REFERENCES yahoo_teams(league_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_yahoo_team_summaries_league
    ON yahoo_team_summaries(league_id);

CREATE INDEX IF NOT EXISTS idx_yahoo_team_summaries_date
    ON yahoo_team_summaries(date);

-- =============================================================================
-- Team Summary Stats (individual stat values per team/date)
-- =============================================================================

CREATE TABLE IF NOT EXISTS yahoo_team_summary_stats (
    -- Composite primary key: stat value for a team on a specific date
    league_id INT NOT NULL,
    team_id INT NOT NULL,
    date DATE NOT NULL,
    stat_id INT NOT NULL,

    -- Stat value (stored as text since Yahoo returns mixed types)
    value TEXT NOT NULL DEFAULT '',

    PRIMARY KEY (league_id, team_id, date, stat_id),
    FOREIGN KEY (league_id, team_id, date) REFERENCES yahoo_team_summaries(league_id, team_id, date) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_yahoo_team_summary_stats_stat
    ON yahoo_team_summary_stats(stat_id);

-- =============================================================================
-- Team Rosters (roster entries for a coverage date)
-- =============================================================================

CREATE TABLE IF NOT EXISTS yahoo_team_rosters (
    -- Composite primary key: player on a team's roster for a specific date
    league_id INT NOT NULL,
    team_id INT NOT NULL,
    date DATE NOT NULL,
    player_id INT NOT NULL,

    -- Coverage metadata
    coverage_type TEXT NOT NULL DEFAULT 'date',
    is_editable BOOLEAN NOT NULL DEFAULT FALSE,

    -- Player key (format: "game_key.p.player_id")
    player_key TEXT NOT NULL DEFAULT '',

    -- Selected position for this roster date
    selected_position TEXT NOT NULL DEFAULT '',
    is_flex BOOLEAN NOT NULL DEFAULT FALSE,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (league_id, team_id, date, player_id),
    FOREIGN KEY (league_id, team_id) REFERENCES yahoo_teams(league_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_yahoo_team_rosters_league
    ON yahoo_team_rosters(league_id);

CREATE INDEX IF NOT EXISTS idx_yahoo_team_rosters_date
    ON yahoo_team_rosters(date);

CREATE INDEX IF NOT EXISTS idx_yahoo_team_rosters_player
    ON yahoo_team_rosters(player_id);
