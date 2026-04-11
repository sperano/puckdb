-- Yahoo Fantasy Leagues Schema

CREATE TABLE yahoo_leagues (
    id INT PRIMARY KEY,
    league_key TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    logo_url TEXT NOT NULL DEFAULT '',
    season INT NOT NULL,
    game_code TEXT NOT NULL DEFAULT 'nhl',
    num_teams INT NOT NULL DEFAULT 0,
    scoring_type TEXT NOT NULL DEFAULT '',
    league_type TEXT NOT NULL DEFAULT '',
    draft_status TEXT NOT NULL DEFAULT '',
    is_pro_league BOOLEAN NOT NULL DEFAULT FALSE,
    is_cash_league BOOLEAN NOT NULL DEFAULT FALSE,
    start_date DATE,
    end_date DATE,
    draft_type TEXT NOT NULL DEFAULT '',
    is_auction_draft BOOLEAN NOT NULL DEFAULT FALSE,
    draft_time TIMESTAMPTZ,
    draft_pick_time INT,
    waiver_type TEXT NOT NULL DEFAULT '',
    waiver_rule TEXT NOT NULL DEFAULT '',
    waiver_time INT,
    trade_end_date DATE,
    trade_ratify_type TEXT NOT NULL DEFAULT '',
    trade_reject_time INT,
    max_teams INT,
    player_pool TEXT NOT NULL DEFAULT '',
    post_draft_players TEXT NOT NULL DEFAULT '',
    cant_cut_list TEXT NOT NULL DEFAULT '',
    uses_playoff BOOLEAN NOT NULL DEFAULT TRUE,
    persistent_url TEXT NOT NULL DEFAULT '',
    league_update_timestamp BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_yahoo_leagues_season ON yahoo_leagues(season);

CREATE TABLE yahoo_league_roster_positions (
    league_id INT NOT NULL REFERENCES yahoo_leagues(id) ON DELETE CASCADE,
    position TEXT NOT NULL,
    position_type TEXT NOT NULL DEFAULT '',
    count INT NOT NULL DEFAULT 1,
    is_starting_position BOOLEAN NOT NULL DEFAULT TRUE,
    PRIMARY KEY (league_id, position)
);

CREATE TABLE yahoo_league_stat_categories (
    league_id INT NOT NULL REFERENCES yahoo_leagues(id) ON DELETE CASCADE,
    stat_id INT NOT NULL,
    name TEXT NOT NULL,
    abbr TEXT NOT NULL DEFAULT '',
    stat_group TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    value REAL,
    PRIMARY KEY (league_id, stat_id)
);

CREATE INDEX idx_yahoo_league_stat_categories_enabled
    ON yahoo_league_stat_categories(league_id) WHERE enabled = TRUE;

CREATE TABLE yahoo_teams (
    league_id INT NOT NULL REFERENCES yahoo_leagues(id) ON DELETE CASCADE,
    id INT NOT NULL,
    team_key TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    logo_url TEXT NOT NULL DEFAULT '',
    draft_position INT,
    waiver_priority INT,
    number_of_moves INT NOT NULL DEFAULT 0,
    number_of_trades INT NOT NULL DEFAULT 0,
    is_owned_by_current_login BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (league_id, id)
);

CREATE TABLE yahoo_team_managers (
    league_id INT NOT NULL,
    team_id INT NOT NULL,
    id INT NOT NULL,
    nickname TEXT NOT NULL DEFAULT '',
    guid TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    image_url TEXT NOT NULL DEFAULT '',
    felo_score INT NOT NULL DEFAULT 0,
    felo_tier TEXT NOT NULL DEFAULT '',
    is_current_login BOOLEAN NOT NULL DEFAULT FALSE,
    is_commissioner BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (league_id, team_id, id),
    FOREIGN KEY (league_id, team_id) REFERENCES yahoo_teams(league_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_yahoo_team_managers_guid ON yahoo_team_managers(guid);
