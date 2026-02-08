-- PuckDB Initial Schema
-- NHL reference data + player/game statistics

-- =============================================================================
-- Reference Data
-- =============================================================================

-- Franchises (organizational entities spanning all time)
CREATE TABLE IF NOT EXISTS franchises (
    id BIGINT PRIMARY KEY,
    full_name TEXT NOT NULL,
    team_common_name TEXT NOT NULL,
    team_place_name TEXT NOT NULL
);

-- Seasons (from SeasonStandingManifest)
CREATE TABLE IF NOT EXISTS seasons (
    id INT PRIMARY KEY,                    -- e.g., 20232024
    standings_start DATE NOT NULL,
    standings_end DATE NOT NULL
);

-- Team identity per season (denormalized league structure)
CREATE TABLE IF NOT EXISTS season_teams (
    season_id INT NOT NULL REFERENCES seasons(id),
    team_id BIGINT NOT NULL,               -- NHL API team ID
    franchise_id BIGINT REFERENCES franchises(id),

    -- Team identity
    full_name TEXT NOT NULL,               -- "Montréal Canadiens"
    abbrev TEXT NOT NULL,                  -- "MTL"
    logo_url TEXT,

    -- League structure (denormalized from standings)
    division_name TEXT NOT NULL,           -- "Atlantic"
    division_abbrev TEXT NOT NULL,         -- "A"
    conference_name TEXT,                  -- "Eastern" (NULL pre-1974)
    conference_abbrev TEXT,                -- "E" (NULL pre-1974)

    PRIMARY KEY (season_id, team_id)
);

CREATE INDEX IF NOT EXISTS idx_season_teams_franchise ON season_teams(franchise_id);
CREATE INDEX IF NOT EXISTS idx_season_teams_abbrev ON season_teams(abbrev);
CREATE INDEX IF NOT EXISTS idx_season_teams_division ON season_teams(division_name);

-- =============================================================================
-- Players
-- =============================================================================

CREATE TABLE IF NOT EXISTS players (
    -- Primary key: NHL API player ID
    id BIGINT PRIMARY KEY,

    -- Yahoo Fantasy ID (nullable - not all NHL players are in Yahoo Fantasy)
    yahoo_id BIGINT UNIQUE,

    -- Basic info
    first_name TEXT NOT NULL,
    last_name TEXT NOT NULL,

    -- Normalized names for accent-insensitive searching (lowercase, unaccented)
    first_name_normalized TEXT NOT NULL DEFAULT '',
    last_name_normalized TEXT NOT NULL DEFAULT '',

    -- Team reference (NHL API team ID, no FK since teams are per-season)
    team_id BIGINT,

    -- Position and handedness
    position TEXT NOT NULL DEFAULT '',      -- C, LW, RW, D, G
    shoots_catches TEXT NOT NULL DEFAULT '', -- L, R

    -- Physical attributes
    height_inches INT,
    weight_pounds INT,

    -- Birth info
    birth_date DATE,
    birth_city TEXT,
    birth_state_province TEXT,
    birth_country TEXT,

    -- Jersey number
    sweater_number INT,

    -- Active status
    is_active BOOLEAN NOT NULL DEFAULT TRUE,

    -- Images from NHL API
    headshot_url TEXT NOT NULL DEFAULT '',
    hero_image_url TEXT,

    -- Images from Yahoo
    yahoo_image_small TEXT NOT NULL DEFAULT '',
    yahoo_image_medium TEXT NOT NULL DEFAULT '',
    yahoo_image_large TEXT NOT NULL DEFAULT '',

    -- URLs
    yahoo_home_url TEXT NOT NULL DEFAULT '',
    player_slug TEXT,                       -- NHL API slug for URLs

    -- Draft details (denormalized)
    draft_year INT,
    draft_team_abbrev TEXT,
    draft_round INT,
    draft_pick_in_round INT,
    draft_overall_pick INT
);

CREATE INDEX IF NOT EXISTS idx_players_team ON players(team_id);
CREATE INDEX IF NOT EXISTS idx_players_yahoo_id ON players(yahoo_id);
CREATE INDEX IF NOT EXISTS idx_players_position ON players(position);
CREATE INDEX IF NOT EXISTS idx_players_is_active ON players(is_active);
CREATE INDEX IF NOT EXISTS idx_players_name ON players(last_name, first_name);
CREATE INDEX IF NOT EXISTS idx_players_name_normalized ON players(last_name_normalized, first_name_normalized);

-- =============================================================================
-- Games and Boxscore Stats
-- =============================================================================

-- Game metadata from NHL API boxscores
CREATE TABLE IF NOT EXISTS games (
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

    -- Home team (NHL API team ID, no FK since teams are per-season)
    home_team_id BIGINT NOT NULL,
    home_team_score INT NOT NULL DEFAULT 0,
    home_team_sog INT NOT NULL DEFAULT 0,

    -- Away team (NHL API team ID, no FK since teams are per-season)
    away_team_id BIGINT NOT NULL,
    away_team_score INT NOT NULL DEFAULT 0,
    away_team_sog INT NOT NULL DEFAULT 0,

    -- Metadata
    limited_scoring BOOLEAN NOT NULL DEFAULT FALSE,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_games_game_date ON games(game_date);
CREATE INDEX IF NOT EXISTS idx_games_season ON games(season);
CREATE INDEX IF NOT EXISTS idx_games_game_type ON games(game_type);
CREATE INDEX IF NOT EXISTS idx_games_home_team ON games(home_team_id);
CREATE INDEX IF NOT EXISTS idx_games_away_team ON games(away_team_id);
CREATE INDEX IF NOT EXISTS idx_games_game_state ON games(game_state);
CREATE INDEX IF NOT EXISTS idx_games_season_game_type ON games(season, game_type);

-- Per-game statistics for forwards and defensemen
CREATE TABLE IF NOT EXISTS game_skater_stats (
    -- Composite primary key
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    player_id BIGINT NOT NULL REFERENCES players(id),

    -- Team context (NHL API team ID, no FK since teams are per-season)
    team_id BIGINT NOT NULL,
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

CREATE INDEX IF NOT EXISTS idx_game_skater_stats_player ON game_skater_stats(player_id);
CREATE INDEX IF NOT EXISTS idx_game_skater_stats_team ON game_skater_stats(team_id);
CREATE INDEX IF NOT EXISTS idx_game_skater_stats_position ON game_skater_stats(position);

-- Per-game statistics for goalies
CREATE TABLE IF NOT EXISTS game_goalie_stats (
    -- Composite primary key
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    player_id BIGINT NOT NULL REFERENCES players(id),

    -- Team context (NHL API team ID, no FK since teams are per-season)
    team_id BIGINT NOT NULL,
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

CREATE INDEX IF NOT EXISTS idx_game_goalie_stats_player ON game_goalie_stats(player_id);
CREATE INDEX IF NOT EXISTS idx_game_goalie_stats_team ON game_goalie_stats(team_id);
CREATE INDEX IF NOT EXISTS idx_game_goalie_stats_decision ON game_goalie_stats(decision) WHERE decision IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_game_goalie_stats_starter ON game_goalie_stats(starter) WHERE starter = TRUE;
