CREATE TABLE shifts (
    id              BIGINT  PRIMARY KEY,
    game_id         BIGINT  NOT NULL REFERENCES games(id),
    player_id       BIGINT  NOT NULL,
    team_id         BIGINT  NOT NULL,
    period          INT     NOT NULL,
    start_time      TEXT    NOT NULL,
    end_time        TEXT    NOT NULL,
    duration        TEXT    NOT NULL,
    shift_number    INT     NOT NULL,
    type_code       INT     NOT NULL,
    detail_code     INT     NOT NULL,
    event_number    BIGINT  NOT NULL,
    event_description TEXT
);
CREATE INDEX idx_shifts_game ON shifts (game_id);
CREATE INDEX idx_shifts_player ON shifts (player_id);
CREATE INDEX idx_shifts_game_player ON shifts (game_id, player_id);
