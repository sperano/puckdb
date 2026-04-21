-- Fix edge_team_shot_differential to match actual API structure
-- API returns aggregate stats, not per-strength breakdown

DROP TABLE IF EXISTS edge_team_shot_differential;

CREATE TABLE edge_team_shot_differential (
    team_id                         BIGINT NOT NULL,
    season                          INT NOT NULL REFERENCES seasons(id),
    game_type                       game_type NOT NULL,

    FOREIGN KEY (season, team_id) REFERENCES season_teams(season, team_id),

    shot_attempt_differential       REAL,
    shot_attempt_differential_rank  INT,
    sog_differential                REAL,
    sog_differential_rank           INT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (team_id, season, game_type)
);
