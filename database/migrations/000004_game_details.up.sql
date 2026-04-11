-- Game details: story data, play-by-play, shifts, season series

-- Three Stars of the game
CREATE TABLE game_three_stars (
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    star SMALLINT NOT NULL CHECK (star BETWEEN 1 AND 3),
    player_id BIGINT NOT NULL REFERENCES players(id),
    PRIMARY KEY (game_id, star)
);

CREATE INDEX idx_game_three_stars_player ON game_three_stars(player_id);

-- Goal highlights
CREATE TABLE goal_highlights (
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    event_id BIGINT NOT NULL,
    player_id BIGINT NOT NULL REFERENCES players(id),
    period SMALLINT NOT NULL,
    time_in_period TEXT NOT NULL,
    goals_to_date SMALLINT,
    highlight_clip_id BIGINT,
    highlight_clip_url TEXT,
    discrete_clip_id BIGINT,
    PRIMARY KEY (game_id, event_id)
);

CREATE INDEX idx_goal_highlights_player ON goal_highlights(player_id);

-- Shootout attempts
CREATE TABLE shootout_attempts (
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    sequence SMALLINT NOT NULL,
    player_id BIGINT NOT NULL REFERENCES players(id),
    team_id BIGINT NOT NULL,
    shot_type TEXT NOT NULL,
    result shootout_result NOT NULL,
    game_winner BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (game_id, sequence)
);

CREATE INDEX idx_shootout_attempts_player ON shootout_attempts(player_id);

-- Play-by-play events
CREATE TABLE play_events (
    game_id BIGINT NOT NULL REFERENCES games(id),
    event_id BIGINT NOT NULL,
    period INT NOT NULL,
    period_type period_type NOT NULL,
    time_in_period TEXT NOT NULL,
    time_remaining TEXT NOT NULL,
    situation_code INT,
    home_team_defending_side ice_side,
    type_desc_key play_event_type NOT NULL,
    sort_order INT NOT NULL,
    x_coord INT,
    y_coord INT,
    zone_code zone_code,
    event_owner_team_id BIGINT,
    shot_type TEXT,
    shooting_player_id BIGINT,
    goalie_in_net_id BIGINT,
    blocking_player_id BIGINT,
    scoring_player_id BIGINT,
    scoring_player_total INT,
    assist1_player_id BIGINT,
    assist1_player_total INT,
    assist2_player_id BIGINT,
    assist2_player_total INT,
    away_score INT,
    home_score INT,
    highlight_clip_id BIGINT,
    highlight_clip_url TEXT,
    discrete_clip_id BIGINT,
    penalty_type_code TEXT,
    penalty_desc_key TEXT,
    penalty_duration INT,
    committed_by_player_id BIGINT,
    drawn_by_player_id BIGINT,
    hitting_player_id BIGINT,
    hittee_player_id BIGINT,
    winning_player_id BIGINT,
    losing_player_id BIGINT,
    player_id BIGINT,
    reason TEXT,
    away_sog INT,
    home_sog INT,
    PRIMARY KEY (game_id, event_id)
);

CREATE INDEX idx_play_events_type ON play_events(type_desc_key);
CREATE INDEX idx_play_events_game_period ON play_events(game_id, period);

-- Shift charts
CREATE TABLE shifts (
    id BIGINT PRIMARY KEY,
    game_id BIGINT NOT NULL REFERENCES games(id),
    player_id BIGINT NOT NULL,
    team_id BIGINT NOT NULL,
    period INT NOT NULL,
    start_time TEXT NOT NULL,
    end_time TEXT NOT NULL,
    duration TEXT NOT NULL,
    shift_number INT NOT NULL,
    type_code shift_type NOT NULL,
    detail_code shift_detail NOT NULL,
    event_number BIGINT NOT NULL,
    event_description TEXT
);

CREATE INDEX idx_shifts_player ON shifts(player_id);
CREATE INDEX idx_shifts_game_player ON shifts(game_id, player_id);

-- Game officials
CREATE TABLE game_officials (
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    role official_role NOT NULL,
    sequence SMALLINT NOT NULL,
    name TEXT NOT NULL,
    PRIMARY KEY (game_id, role, sequence)
);

-- Head coaches per team per game
CREATE TABLE game_coaches (
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    team_id BIGINT NOT NULL,
    head_coach TEXT NOT NULL,
    PRIMARY KEY (game_id, team_id)
);

-- Scratched players
CREATE TABLE game_scratches (
    game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    team_id BIGINT NOT NULL,
    player_id BIGINT NOT NULL REFERENCES players(id),
    PRIMARY KEY (game_id, player_id)
);

CREATE INDEX idx_game_scratches_player ON game_scratches(player_id);

COMMENT ON COLUMN play_events.situation_code IS
    '4-digit integer encoding on-ice strength: [away_goalie][away_skaters][home_skaters][home_goalie]. '
    'Example: 1551 = both goalies in, 5v5. 0541 = away empty net, 5v4 home power play.';

COMMENT ON COLUMN play_events.penalty_type_code IS
    'NHL penalty type code string (e.g. "PS-HOOKING"). See penalty_desc_key for human-readable description.';
