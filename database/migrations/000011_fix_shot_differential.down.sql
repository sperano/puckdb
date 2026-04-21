-- Revert to original (incorrect) schema
DROP TABLE IF EXISTS edge_team_shot_differential;

CREATE TABLE edge_team_shot_differential (
    team_id              BIGINT NOT NULL,
    season               INT NOT NULL REFERENCES seasons(id),
    game_type            game_type NOT NULL,
    strength_code        TEXT NOT NULL CHECK (strength_code IN ('all', 'es', 'pp', 'pk')),

    FOREIGN KEY (season, team_id) REFERENCES season_teams(season, team_id),

    for_per_game              REAL,
    for_per_game_rank         INT,
    against_per_game          REAL,
    against_per_game_rank     INT,
    differential_per_game     REAL,
    differential_per_game_rank INT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (team_id, season, game_type, strength_code)
);
