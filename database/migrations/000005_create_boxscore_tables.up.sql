-- Migration: Create NHL boxscore tables
-- Replaces GORM games and player_stats tables with sqlc-managed tables

-- =============================================================================
-- nhl_games: Game metadata from NHL API boxscores
-- Primary key is the NHL API 10-digit game ID
-- =============================================================================
CREATE TABLE IF NOT EXISTS nhl_games (
    -- Primary key: NHL API game ID (10-digit: YYYYGTNNNN)
    id BIGINT PRIMARY KEY,

    -- Season and game classification
    season INT NOT NULL,                            -- 8-digit format: 20242025
    game_type SMALLINT NOT NULL,                    -- 1=preseason, 2=regular, 3=playoffs, 4=allstar
    game_date DATE NOT NULL,

    -- Venue information
    venue TEXT NOT NULL DEFAULT '',
    venue_location TEXT NOT NULL DEFAULT '',

    -- Time information
    start_time_utc TIMESTAMPTZ,
    eastern_utc_offset TEXT NOT NULL DEFAULT '',
    venue_utc_offset TEXT NOT NULL DEFAULT '',

    -- Game state
    game_state TEXT NOT NULL DEFAULT 'FUT',         -- FUT, PRE, LIVE, FINAL, OFF, PPD, SUSP, CRIT
    game_schedule_state TEXT NOT NULL DEFAULT 'OK', -- OK, DONT_PLAY, PPD, SUSP, TBD, COMPLETED, CNCL

    -- Period information
    period_number SMALLINT NOT NULL DEFAULT 0,
    period_type TEXT NOT NULL DEFAULT 'REG',        -- REG, OT, SO
    max_regulation_periods SMALLINT NOT NULL DEFAULT 3,

    -- Clock (for live/completed games)
    clock_time_remaining TEXT NOT NULL DEFAULT '',
    clock_seconds_remaining INT NOT NULL DEFAULT 0,
    clock_running BOOLEAN NOT NULL DEFAULT FALSE,
    clock_in_intermission BOOLEAN NOT NULL DEFAULT FALSE,

    -- Home team
    home_team_id BIGINT NOT NULL REFERENCES nhl_teams(id),
    home_team_score INT NOT NULL DEFAULT 0,
    home_team_sog INT NOT NULL DEFAULT 0,

    -- Away team
    away_team_id BIGINT NOT NULL REFERENCES nhl_teams(id),
    away_team_score INT NOT NULL DEFAULT 0,
    away_team_sog INT NOT NULL DEFAULT 0,

    -- Metadata
    limited_scoring BOOLEAN NOT NULL DEFAULT FALSE,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes for common query patterns
CREATE INDEX IF NOT EXISTS idx_nhl_games_game_date ON nhl_games(game_date);
CREATE INDEX IF NOT EXISTS idx_nhl_games_season ON nhl_games(season);
CREATE INDEX IF NOT EXISTS idx_nhl_games_game_type ON nhl_games(game_type);
CREATE INDEX IF NOT EXISTS idx_nhl_games_home_team ON nhl_games(home_team_id);
CREATE INDEX IF NOT EXISTS idx_nhl_games_away_team ON nhl_games(away_team_id);
CREATE INDEX IF NOT EXISTS idx_nhl_games_game_state ON nhl_games(game_state);
CREATE INDEX IF NOT EXISTS idx_nhl_games_season_game_type ON nhl_games(season, game_type);

-- =============================================================================
-- nhl_game_skater_stats: Per-game statistics for forwards and defensemen
-- Composite primary key: (game_id, player_id)
-- =============================================================================
CREATE TABLE IF NOT EXISTS nhl_game_skater_stats (
    -- Composite primary key
    game_id BIGINT NOT NULL REFERENCES nhl_games(id) ON DELETE CASCADE,
    player_id BIGINT NOT NULL REFERENCES players(id),

    -- Team context
    team_id BIGINT NOT NULL REFERENCES nhl_teams(id),
    is_home BOOLEAN NOT NULL,

    -- Player identification in game
    sweater_number SMALLINT NOT NULL,
    position TEXT NOT NULL,                         -- C, LW, RW, D

    -- Scoring stats
    goals SMALLINT NOT NULL DEFAULT 0,
    assists SMALLINT NOT NULL DEFAULT 0,
    points SMALLINT NOT NULL DEFAULT 0,
    plus_minus SMALLINT NOT NULL DEFAULT 0,

    -- Shot stats
    shots_on_goal SMALLINT NOT NULL DEFAULT 0,

    -- Time stats
    toi_seconds INT NOT NULL DEFAULT 0,             -- Time on ice in seconds
    shifts SMALLINT NOT NULL DEFAULT 0,

    -- Faceoffs
    faceoff_winning_pctg REAL,                      -- NULL if no faceoffs taken

    -- Physical play
    hits SMALLINT NOT NULL DEFAULT 0,
    blocked_shots SMALLINT NOT NULL DEFAULT 0,
    penalty_minutes SMALLINT NOT NULL DEFAULT 0,

    -- Puck control
    giveaways SMALLINT NOT NULL DEFAULT 0,
    takeaways SMALLINT NOT NULL DEFAULT 0,

    -- Special teams
    power_play_goals SMALLINT NOT NULL DEFAULT 0,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (game_id, player_id)
);

-- Indexes for common query patterns
CREATE INDEX IF NOT EXISTS idx_nhl_game_skater_stats_player ON nhl_game_skater_stats(player_id);
CREATE INDEX IF NOT EXISTS idx_nhl_game_skater_stats_team ON nhl_game_skater_stats(team_id);
CREATE INDEX IF NOT EXISTS idx_nhl_game_skater_stats_position ON nhl_game_skater_stats(position);

-- =============================================================================
-- nhl_game_goalie_stats: Per-game statistics for goalies
-- Composite primary key: (game_id, player_id)
-- =============================================================================
CREATE TABLE IF NOT EXISTS nhl_game_goalie_stats (
    -- Composite primary key
    game_id BIGINT NOT NULL REFERENCES nhl_games(id) ON DELETE CASCADE,
    player_id BIGINT NOT NULL REFERENCES players(id),

    -- Team context
    team_id BIGINT NOT NULL REFERENCES nhl_teams(id),
    is_home BOOLEAN NOT NULL,

    -- Player identification in game
    sweater_number SMALLINT NOT NULL,

    -- Game result
    decision TEXT,                                  -- W, L, T, OTL (NULL if no decision)
    starter BOOLEAN,                                -- TRUE if starting goalie

    -- Save stats
    shots_against INT NOT NULL DEFAULT 0,
    saves INT NOT NULL DEFAULT 0,
    save_pctg REAL,                                 -- NULL if no shots faced

    -- Goals against breakdown
    goals_against SMALLINT NOT NULL DEFAULT 0,
    even_strength_goals_against SMALLINT NOT NULL DEFAULT 0,
    power_play_goals_against SMALLINT NOT NULL DEFAULT 0,
    shorthanded_goals_against SMALLINT NOT NULL DEFAULT 0,

    -- Time stats
    toi_seconds INT NOT NULL DEFAULT 0,

    -- Penalties
    penalty_minutes SMALLINT,                       -- NULL if not recorded

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (game_id, player_id)
);

-- Indexes for common query patterns
CREATE INDEX IF NOT EXISTS idx_nhl_game_goalie_stats_player ON nhl_game_goalie_stats(player_id);
CREATE INDEX IF NOT EXISTS idx_nhl_game_goalie_stats_team ON nhl_game_goalie_stats(team_id);
CREATE INDEX IF NOT EXISTS idx_nhl_game_goalie_stats_decision ON nhl_game_goalie_stats(decision) WHERE decision IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_nhl_game_goalie_stats_starter ON nhl_game_goalie_stats(starter) WHERE starter = TRUE;
