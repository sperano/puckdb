-- players table: merged from GORM Player and nhl-api-go PlayerLanding
-- id = NHL API player ID (primary)
-- yahoo_id = Yahoo Fantasy player ID (nullable, for players not in Yahoo)

CREATE TABLE IF NOT EXISTS players (
    -- Primary key: NHL API player ID
    id BIGINT PRIMARY KEY,

    -- Yahoo Fantasy ID (nullable - not all NHL players are in Yahoo Fantasy)
    yahoo_id BIGINT UNIQUE,

    -- Basic info
    first_name TEXT NOT NULL,
    last_name TEXT NOT NULL,

    -- Team reference
    nhl_team_id BIGINT REFERENCES nhl_teams(id),

    -- Position and handedness
    position TEXT NOT NULL DEFAULT '',  -- C, LW, RW, D, G
    shoots_catches TEXT NOT NULL DEFAULT '',  -- L, R

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
    player_slug TEXT,  -- NHL API slug for URLs

    -- Draft details (denormalized)
    draft_year INT,
    draft_team_abbrev TEXT,
    draft_round INT,
    draft_pick_in_round INT,
    draft_overall_pick INT
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_players_nhl_team ON players(nhl_team_id);
CREATE INDEX IF NOT EXISTS idx_players_yahoo_id ON players(yahoo_id);
CREATE INDEX IF NOT EXISTS idx_players_position ON players(position);
CREATE INDEX IF NOT EXISTS idx_players_is_active ON players(is_active);
CREATE INDEX IF NOT EXISTS idx_players_name ON players(last_name, first_name);
