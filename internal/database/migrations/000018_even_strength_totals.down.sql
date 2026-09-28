DROP TABLE even_strength_skater_games;
DROP TABLE even_strength_pair_toi;

CREATE TABLE even_strength_segments (
    game_id bigint NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    period integer NOT NULL,
    start_second integer NOT NULL,
    end_second integer NOT NULL,
    team_id bigint NOT NULL,
    player_id bigint NOT NULL,
    PRIMARY KEY (game_id, period, player_id, start_second),
    CHECK (start_second < end_second)
);
