CREATE TABLE play_events (
    game_id               BIGINT  NOT NULL REFERENCES games(id),
    event_id              BIGINT  NOT NULL,
    period                INT     NOT NULL,
    period_type           TEXT    NOT NULL,
    time_in_period        TEXT    NOT NULL,
    time_remaining        TEXT    NOT NULL,
    situation_code        TEXT,
    home_team_defending_side TEXT,
    type_code             INT     NOT NULL,
    type_desc_key         TEXT    NOT NULL,
    sort_order            INT     NOT NULL,
    -- Details: coordinates
    x_coord               INT,
    y_coord               INT,
    zone_code             TEXT,
    event_owner_team_id   BIGINT,
    -- Details: shot
    shot_type             TEXT,
    shooting_player_id    BIGINT,
    goalie_in_net_id      BIGINT,
    -- Details: blocked shot
    blocking_player_id    BIGINT,
    -- Details: goal
    scoring_player_id     BIGINT,
    scoring_player_total  INT,
    assist1_player_id     BIGINT,
    assist1_player_total  INT,
    assist2_player_id     BIGINT,
    assist2_player_total  INT,
    away_score            INT,
    home_score            INT,
    highlight_clip_id     BIGINT,
    highlight_clip_url    TEXT,
    discrete_clip_id      BIGINT,
    -- Details: penalty
    penalty_type_code     TEXT,
    penalty_desc_key      TEXT,
    penalty_duration      INT,
    committed_by_player_id BIGINT,
    drawn_by_player_id    BIGINT,
    -- Details: hit
    hitting_player_id     BIGINT,
    hittee_player_id      BIGINT,
    -- Details: faceoff
    winning_player_id     BIGINT,
    losing_player_id      BIGINT,
    -- Details: general
    player_id             BIGINT,
    reason                TEXT,
    away_sog              INT,
    home_sog              INT,
    PRIMARY KEY (game_id, event_id)
);
CREATE INDEX idx_play_events_type ON play_events (type_desc_key);
CREATE INDEX idx_play_events_game_period ON play_events (game_id, period);
