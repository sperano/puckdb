-- Yahoo Fantasy Leagues Schema
-- Stores league, team, roster position, and stat category data from Yahoo Fantasy API

-- =============================================================================
-- Leagues
-- =============================================================================

CREATE TABLE IF NOT EXISTS yahoo_leagues (
    -- Primary key: Yahoo league ID
    id INT PRIMARY KEY,

    -- League key (format: "game_key.l.league_id", e.g., "419.l.12345")
    league_key TEXT NOT NULL UNIQUE,

    -- Basic info
    name TEXT NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    logo_url TEXT NOT NULL DEFAULT '',

    -- Season and game context
    season INT NOT NULL,
    game_code TEXT NOT NULL DEFAULT 'nhl',

    -- League configuration
    num_teams INT NOT NULL DEFAULT 0,
    scoring_type TEXT NOT NULL DEFAULT '',      -- head, roto, point, headone, headpoint
    league_type TEXT NOT NULL DEFAULT '',       -- private, public
    draft_status TEXT NOT NULL DEFAULT '',      -- predraft, drafted, postdraft

    -- League flags
    is_pro_league BOOLEAN NOT NULL DEFAULT FALSE,
    is_cash_league BOOLEAN NOT NULL DEFAULT FALSE,

    -- Season dates
    start_date DATE,
    end_date DATE,

    -- Draft settings
    draft_type TEXT NOT NULL DEFAULT '',        -- live, offline, autopick
    is_auction_draft BOOLEAN NOT NULL DEFAULT FALSE,
    draft_time TIMESTAMPTZ,
    draft_pick_time INT,                        -- seconds per pick

    -- Waiver settings
    waiver_type TEXT NOT NULL DEFAULT '',       -- R (rolling), FR (first-year), FAB
    waiver_rule TEXT NOT NULL DEFAULT '',       -- gametime, continuous
    waiver_time INT,                            -- days

    -- Trade settings
    trade_end_date DATE,
    trade_ratify_type TEXT NOT NULL DEFAULT '', -- commish, vote
    trade_reject_time INT,                      -- days

    -- Other settings
    max_teams INT,
    player_pool TEXT NOT NULL DEFAULT '',       -- all, nhl
    post_draft_players TEXT NOT NULL DEFAULT '',-- W (waivers), FA (free agents)
    cant_cut_list TEXT NOT NULL DEFAULT '',     -- yahoo, none
    uses_playoff BOOLEAN NOT NULL DEFAULT TRUE,
    persistent_url TEXT NOT NULL DEFAULT '',

    -- Timestamps
    league_update_timestamp BIGINT,             -- Yahoo's update timestamp
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_yahoo_leagues_season ON yahoo_leagues(season);
CREATE INDEX IF NOT EXISTS idx_yahoo_leagues_league_key ON yahoo_leagues(league_key);

-- =============================================================================
-- Roster Positions (per league)
-- =============================================================================

CREATE TABLE IF NOT EXISTS yahoo_league_roster_positions (
    -- Composite primary key
    league_id INT NOT NULL REFERENCES yahoo_leagues(id) ON DELETE CASCADE,
    position TEXT NOT NULL,

    -- Position configuration
    position_type TEXT NOT NULL DEFAULT '',     -- P (player), B (bench), IL (injured)
    count INT NOT NULL DEFAULT 1,
    is_starting_position BOOLEAN NOT NULL DEFAULT TRUE,

    PRIMARY KEY (league_id, position)
);

-- =============================================================================
-- Stat Categories (per league)
-- =============================================================================

CREATE TABLE IF NOT EXISTS yahoo_league_stat_categories (
    -- Composite primary key
    league_id INT NOT NULL REFERENCES yahoo_leagues(id) ON DELETE CASCADE,
    stat_id INT NOT NULL,

    -- Stat configuration
    name TEXT NOT NULL,
    abbr TEXT NOT NULL DEFAULT '',
    stat_group TEXT NOT NULL DEFAULT '',        -- offense, goaltending
    enabled BOOLEAN NOT NULL DEFAULT TRUE,

    -- Scoring value (for point-based leagues)
    value REAL,

    PRIMARY KEY (league_id, stat_id)
);

CREATE INDEX IF NOT EXISTS idx_yahoo_league_stat_categories_enabled
    ON yahoo_league_stat_categories(league_id) WHERE enabled = TRUE;

-- =============================================================================
-- Teams (within leagues)
-- =============================================================================

CREATE TABLE IF NOT EXISTS yahoo_teams (
    -- Composite primary key: team within a league
    league_id INT NOT NULL REFERENCES yahoo_leagues(id) ON DELETE CASCADE,
    id INT NOT NULL,

    -- Team key (format: "game_key.l.league_id.t.team_id")
    team_key TEXT NOT NULL UNIQUE,

    -- Basic info
    name TEXT NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    logo_url TEXT NOT NULL DEFAULT '',

    -- Draft and waiver info
    draft_position INT,
    waiver_priority INT,
    number_of_moves INT NOT NULL DEFAULT 0,
    number_of_trades INT NOT NULL DEFAULT 0,

    -- Ownership
    is_owned_by_current_login BOOLEAN NOT NULL DEFAULT FALSE,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (league_id, id)
);

CREATE INDEX IF NOT EXISTS idx_yahoo_teams_team_key ON yahoo_teams(team_key);
CREATE INDEX IF NOT EXISTS idx_yahoo_teams_league ON yahoo_teams(league_id);

-- =============================================================================
-- Team Managers
-- =============================================================================

CREATE TABLE IF NOT EXISTS yahoo_team_managers (
    -- Composite primary key: manager within a team
    league_id INT NOT NULL,
    team_id INT NOT NULL,
    id INT NOT NULL,

    -- Manager info
    nickname TEXT NOT NULL DEFAULT '',
    guid TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    image_url TEXT NOT NULL DEFAULT '',

    -- Felo rating (Yahoo's manager rating system)
    felo_score INT NOT NULL DEFAULT 0,
    felo_tier TEXT NOT NULL DEFAULT '',

    -- Flags
    is_current_login BOOLEAN NOT NULL DEFAULT FALSE,
    is_commissioner BOOLEAN NOT NULL DEFAULT FALSE,

    PRIMARY KEY (league_id, team_id, id),
    FOREIGN KEY (league_id, team_id) REFERENCES yahoo_teams(league_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_yahoo_team_managers_guid ON yahoo_team_managers(guid);
