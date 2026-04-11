-- PuckDB Core Schema
-- NHL reference data + player/game statistics

-- =============================================================================
-- Enum Types
-- =============================================================================

CREATE TYPE game_type AS ENUM (
    'preseason', 'regular_season', 'playoffs', 'all_star',
    'world_cup', 'world_cup_2004', 'olympics', 'young_stars',
    'pwhl_showcase', 'womens_all_star', 'four_nations'
);
CREATE TYPE game_state AS ENUM ('FUT', 'PRE', 'LIVE', 'FINAL', 'OFF', 'PPD', 'SUSP', 'CRIT');
CREATE TYPE game_schedule_state AS ENUM ('OK', 'DONT_PLAY', 'PPD', 'SUSP', 'TBD', 'COMPLETED', 'CNCL');
CREATE TYPE period_type AS ENUM ('REG', 'OT', 'SO');
CREATE TYPE play_event_type AS ENUM (
    'faceoff', 'hit', 'giveaway', 'goal', 'shot-on-goal', 'missed-shot',
    'blocked-shot', 'penalty', 'stoppage', 'period-start', 'period-end',
    'shootout-complete', 'game-end', 'takeaway', 'delayed-penalty', 'failed-shot-attempt'
);
CREATE TYPE zone_code AS ENUM ('O', 'D', 'N');
CREATE TYPE ice_side AS ENUM ('left', 'right');
CREATE TYPE goalie_decision AS ENUM ('W', 'L', 'T', 'OTL');
CREATE TYPE player_position AS ENUM ('C', 'LW', 'RW', 'F', 'D', 'G');
CREATE TYPE hand_side AS ENUM ('L', 'R');
CREATE TYPE official_role AS ENUM ('referee', 'linesman');
CREATE TYPE chat_role AS ENUM ('system', 'user', 'assistant', 'tool');
CREATE TYPE shootout_result AS ENUM ('goal', 'save');
CREATE TYPE shift_type AS ENUM ('505', '517');
CREATE TYPE shift_detail AS ENUM ('0', '801', '802', '803', '804', '805', '806', '807', '808', '809', '810', '811');

-- =============================================================================
-- Reference Data
-- =============================================================================

CREATE TABLE franchises (
    id BIGINT PRIMARY KEY,
    full_name TEXT NOT NULL,
    team_common_name TEXT NOT NULL,
    team_place_name TEXT NOT NULL
);

CREATE TABLE seasons (
    id INT PRIMARY KEY,                    -- e.g., 20232024
    standings_start DATE NOT NULL,
    standings_end DATE NOT NULL
);

CREATE TABLE season_teams (
    season INT NOT NULL REFERENCES seasons(id),
    team_id BIGINT NOT NULL,
    franchise_id BIGINT REFERENCES franchises(id),
    full_name TEXT NOT NULL,
    abbrev TEXT NOT NULL,
    logo_url TEXT,
    division_name TEXT NOT NULL,
    division_abbrev TEXT NOT NULL,
    conference_name TEXT,
    conference_abbrev TEXT,
    PRIMARY KEY (season, team_id)
);

CREATE INDEX idx_season_teams_franchise ON season_teams(franchise_id);
CREATE INDEX idx_season_teams_abbrev ON season_teams(abbrev);
CREATE INDEX idx_season_teams_division ON season_teams(division_name);

-- =============================================================================
-- Players
-- =============================================================================

CREATE TABLE players (
    id BIGINT PRIMARY KEY,
    yahoo_id BIGINT UNIQUE,
    first_name TEXT NOT NULL,
    last_name TEXT NOT NULL,
    first_name_normalized TEXT NOT NULL DEFAULT '',
    last_name_normalized TEXT NOT NULL DEFAULT '',
    team_id BIGINT,
    position player_position,
    shoots_catches hand_side,
    height_inches INT,
    weight_pounds INT,
    birth_date DATE,
    birth_city TEXT,
    birth_state_province TEXT,
    birth_country TEXT,
    sweater_number INT,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    headshot_url TEXT NOT NULL DEFAULT '',
    hero_image_url TEXT,
    yahoo_image_small TEXT NOT NULL DEFAULT '',
    yahoo_image_medium TEXT NOT NULL DEFAULT '',
    yahoo_image_large TEXT NOT NULL DEFAULT '',
    yahoo_home_url TEXT NOT NULL DEFAULT '',
    player_slug TEXT,
    draft_year INT,
    draft_team_abbrev TEXT,
    draft_round INT,
    draft_pick_in_round INT,
    draft_overall_pick INT
);

CREATE INDEX idx_players_team ON players(team_id);
CREATE INDEX idx_players_position ON players(position);
CREATE INDEX idx_players_is_active ON players(is_active);
CREATE INDEX idx_players_name ON players(last_name, first_name);
CREATE INDEX idx_players_name_normalized ON players(last_name_normalized, first_name_normalized);

-- =============================================================================
-- Games and Boxscore Stats
-- =============================================================================

CREATE TABLE games (
    id BIGINT PRIMARY KEY,
    season INT NOT NULL,
    game_type game_type NOT NULL,
    game_date DATE NOT NULL,
    venue TEXT NOT NULL DEFAULT '',
    venue_location TEXT NOT NULL DEFAULT '',
    start_time_utc TIMESTAMPTZ,
    eastern_utc_offset TEXT NOT NULL DEFAULT '',
    venue_utc_offset TEXT NOT NULL DEFAULT '',
    game_state game_state NOT NULL DEFAULT 'FUT',
    game_schedule_state game_schedule_state NOT NULL DEFAULT 'OK',
    period_number SMALLINT NOT NULL DEFAULT 0,
    period_type period_type NOT NULL DEFAULT 'REG',
    max_regulation_periods SMALLINT NOT NULL DEFAULT 3,
    clock_time_remaining TEXT NOT NULL DEFAULT '',
    clock_seconds_remaining INT NOT NULL DEFAULT 0,
    clock_running BOOLEAN NOT NULL DEFAULT FALSE,
    clock_in_intermission BOOLEAN NOT NULL DEFAULT FALSE,
    home_team_id BIGINT NOT NULL,
    home_team_score INT NOT NULL DEFAULT 0,
    home_team_sog INT NOT NULL DEFAULT 0,
    away_team_id BIGINT NOT NULL,
    away_team_score INT NOT NULL DEFAULT 0,
    away_team_sog INT NOT NULL DEFAULT 0,
    limited_scoring BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_games_game_date ON games(game_date);
CREATE INDEX idx_games_season ON games(season);
CREATE INDEX idx_games_game_type ON games(game_type);
CREATE INDEX idx_games_home_team ON games(home_team_id);
CREATE INDEX idx_games_away_team ON games(away_team_id);
CREATE INDEX idx_games_game_state ON games(game_state);
CREATE INDEX idx_games_season_game_type ON games(season, game_type);

CREATE TABLE game_skater_stats (
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    player_id BIGINT NOT NULL REFERENCES players(id),
    team_id BIGINT NOT NULL,
    is_home BOOLEAN NOT NULL,
    sweater_number SMALLINT NOT NULL,
    position player_position NOT NULL,
    goals SMALLINT NOT NULL DEFAULT 0,
    assists SMALLINT NOT NULL DEFAULT 0,
    points SMALLINT NOT NULL DEFAULT 0,
    plus_minus SMALLINT NOT NULL DEFAULT 0,
    shots_on_goal SMALLINT NOT NULL DEFAULT 0,
    toi_seconds INT NOT NULL DEFAULT 0,
    shifts SMALLINT NOT NULL DEFAULT 0,
    faceoff_winning_pctg REAL,
    hits SMALLINT NOT NULL DEFAULT 0,
    blocked_shots SMALLINT NOT NULL DEFAULT 0,
    penalty_minutes SMALLINT NOT NULL DEFAULT 0,
    giveaways SMALLINT NOT NULL DEFAULT 0,
    takeaways SMALLINT NOT NULL DEFAULT 0,
    power_play_goals SMALLINT NOT NULL DEFAULT 0,
    power_play_points SMALLINT NOT NULL DEFAULT 0,
    game_winning_goals SMALLINT NOT NULL DEFAULT 0,
    ot_goals SMALLINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (game_id, player_id)
);

CREATE INDEX idx_game_skater_stats_player ON game_skater_stats(player_id);
CREATE INDEX idx_game_skater_stats_team ON game_skater_stats(team_id);
CREATE INDEX idx_game_skater_stats_position ON game_skater_stats(position);
CREATE INDEX idx_game_skater_stats_gwg ON game_skater_stats(game_winning_goals) WHERE game_winning_goals > 0;

CREATE TABLE game_goalie_stats (
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    player_id BIGINT NOT NULL REFERENCES players(id),
    team_id BIGINT NOT NULL,
    is_home BOOLEAN NOT NULL,
    sweater_number SMALLINT NOT NULL,
    decision goalie_decision,
    starter BOOLEAN,
    shots_against INT NOT NULL DEFAULT 0,
    saves INT NOT NULL DEFAULT 0,
    save_pctg REAL,
    goals_against SMALLINT NOT NULL DEFAULT 0,
    even_strength_goals_against SMALLINT NOT NULL DEFAULT 0,
    power_play_goals_against SMALLINT NOT NULL DEFAULT 0,
    shorthanded_goals_against SMALLINT NOT NULL DEFAULT 0,
    even_strength_shots_against TEXT,
    power_play_shots_against TEXT,
    shorthanded_shots_against TEXT,
    toi_seconds INT NOT NULL DEFAULT 0,
    penalty_minutes SMALLINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (game_id, player_id)
);

CREATE INDEX idx_game_goalie_stats_player ON game_goalie_stats(player_id);
CREATE INDEX idx_game_goalie_stats_team ON game_goalie_stats(team_id);
CREATE INDEX idx_game_goalie_stats_decision ON game_goalie_stats(decision) WHERE decision IS NOT NULL;
CREATE INDEX idx_game_goalie_stats_starter ON game_goalie_stats(starter) WHERE starter = TRUE;
