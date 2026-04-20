-- Edge tracking stats (NHL Edge puck and player tracking, 2021-2022 onward)

-- Skater Edge summary stats (from skater-detail endpoint)
CREATE TABLE edge_skater_stats (
    player_id    BIGINT NOT NULL REFERENCES players(id),
    season       INT NOT NULL REFERENCES seasons(id),
    game_type    game_type NOT NULL,

    -- Skating speed
    top_speed_imperial       REAL,
    top_speed_metric         REAL,
    top_speed_percentile     REAL,
    top_speed_league_avg_imperial REAL,
    top_speed_league_avg_metric REAL,
    bursts_over_20           INT,
    bursts_over_20_percentile REAL,
    bursts_over_20_league_avg REAL,

    -- Distance
    total_distance_imperial  REAL,
    total_distance_metric    REAL,
    total_distance_percentile REAL,
    max_game_distance_imperial REAL,
    max_game_distance_metric REAL,
    max_game_distance_percentile REAL,

    -- Shot speed
    top_shot_speed_imperial  REAL,
    top_shot_speed_metric    REAL,
    top_shot_speed_percentile REAL,
    top_shot_speed_league_avg_imperial REAL,
    top_shot_speed_league_avg_metric REAL,

    -- Zone time (all strengths)
    oz_pctg                  REAL,
    oz_percentile            REAL,
    oz_league_avg            REAL,
    nz_pctg                  REAL,
    nz_percentile            REAL,
    nz_league_avg            REAL,
    dz_pctg                  REAL,
    dz_percentile            REAL,
    dz_league_avg            REAL,

    -- Zone time (even strength)
    oz_ev_pctg               REAL,
    oz_ev_percentile         REAL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (player_id, season, game_type)
);

CREATE INDEX idx_edge_skater_stats_season ON edge_skater_stats(season, game_type);

-- Skater shot location breakdown (17 areas per player/season)
CREATE TABLE edge_skater_shot_locations (
    player_id  BIGINT NOT NULL REFERENCES players(id),
    season     INT NOT NULL REFERENCES seasons(id),
    game_type  game_type NOT NULL,
    area       TEXT NOT NULL,

    sog              INT,
    goals            INT,
    shooting_pctg    REAL,
    sog_percentile   REAL,
    goals_percentile REAL,
    shooting_pctg_percentile REAL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (player_id, season, game_type, area)
);

-- Skater SOG summary by location code (all/high/long/mid)
CREATE TABLE edge_skater_sog_summary (
    player_id       BIGINT NOT NULL REFERENCES players(id),
    season          INT NOT NULL REFERENCES seasons(id),
    game_type       game_type NOT NULL,
    location_code   TEXT NOT NULL CHECK (location_code IN ('all', 'high', 'long', 'mid')),

    shots                    INT,
    shots_percentile         REAL,
    shots_league_avg         REAL,
    goals                    INT,
    goals_percentile         REAL,
    goals_league_avg         REAL,
    shooting_pctg            REAL,
    shooting_pctg_percentile REAL,
    shooting_pctg_league_avg REAL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (player_id, season, game_type, location_code)
);

-- Goalie Edge summary stats (from goalie-detail endpoint)
CREATE TABLE edge_goalie_stats (
    player_id    BIGINT NOT NULL REFERENCES players(id),
    season       INT NOT NULL REFERENCES seasons(id),
    game_type    game_type NOT NULL,

    gaa_value                REAL,
    gaa_percentile           REAL,
    gaa_league_avg           REAL,
    games_above_900_value    REAL,
    games_above_900_percentile REAL,
    games_above_900_league_avg REAL,
    goal_diff_per_60_value   REAL,
    goal_diff_per_60_percentile REAL,
    goal_diff_per_60_league_avg REAL,
    goal_support_avg_value   REAL,
    goal_support_avg_percentile REAL,
    goal_support_avg_league_avg REAL,
    point_pctg_value         REAL,
    point_pctg_percentile    REAL,
    point_pctg_league_avg    REAL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (player_id, season, game_type)
);

CREATE INDEX idx_edge_goalie_stats_season ON edge_goalie_stats(season, game_type);

-- Goalie shot location summary (all/high/long/mid)
CREATE TABLE edge_goalie_shot_location_summary (
    player_id       BIGINT NOT NULL REFERENCES players(id),
    season          INT NOT NULL REFERENCES seasons(id),
    game_type       game_type NOT NULL,
    location_code   TEXT NOT NULL CHECK (location_code IN ('all', 'high', 'long', 'mid')),

    goals_against            INT,
    goals_against_percentile REAL,
    goals_against_league_avg REAL,
    saves                    INT,
    saves_percentile         REAL,
    saves_league_avg         REAL,
    save_pctg                REAL,
    save_pctg_percentile     REAL,
    save_pctg_league_avg     REAL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (player_id, season, game_type, location_code)
);

-- Goalie shot location detail (17 areas)
CREATE TABLE edge_goalie_shot_locations (
    player_id  BIGINT NOT NULL REFERENCES players(id),
    season     INT NOT NULL REFERENCES seasons(id),
    game_type  game_type NOT NULL,
    area       TEXT NOT NULL,

    saves              INT,
    saves_percentile   REAL,
    save_pctg          REAL,
    save_pctg_percentile REAL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (player_id, season, game_type, area)
);

-- Team Edge summary stats (from team-detail endpoint)
CREATE TABLE edge_team_stats (
    team_id      BIGINT NOT NULL,
    season       INT NOT NULL REFERENCES seasons(id),
    game_type    game_type NOT NULL,

    FOREIGN KEY (season, team_id) REFERENCES season_teams(season, team_id),

    -- Shot speed
    shot_attempts_over_90    INT,
    shot_attempts_over_90_rank INT,
    top_shot_speed_imperial  REAL,
    top_shot_speed_metric    REAL,
    top_shot_speed_rank      INT,

    -- Skating speed
    bursts_over_22           INT,
    bursts_over_22_rank      INT,
    bursts_over_20           INT,
    bursts_over_20_rank      INT,
    speed_max_imperial       REAL,
    speed_max_metric         REAL,
    speed_max_rank           INT,

    -- Distance
    total_distance           INT,
    total_distance_rank      INT,

    -- Zone time
    oz_pctg                  REAL,
    oz_rank                  INT,
    oz_league_avg            REAL,
    oz_ev_pctg               REAL,
    oz_ev_rank               INT,
    nz_pctg                  REAL,
    nz_rank                  INT,
    nz_league_avg            REAL,
    dz_pctg                  REAL,
    dz_rank                  INT,
    dz_league_avg            REAL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (team_id, season, game_type)
);

CREATE INDEX idx_edge_team_stats_season ON edge_team_stats(season, game_type);

-- Team SOG summary (all/high/long/mid)
CREATE TABLE edge_team_sog_summary (
    team_id         BIGINT NOT NULL,
    season          INT NOT NULL REFERENCES seasons(id),
    game_type       game_type NOT NULL,
    location_code   TEXT NOT NULL CHECK (location_code IN ('all', 'high', 'long', 'mid')),

    FOREIGN KEY (season, team_id) REFERENCES season_teams(season, team_id),

    shots               INT,
    shots_rank          INT,
    shots_league_avg    REAL,
    goals               INT,
    goals_rank          INT,
    goals_league_avg    REAL,
    shooting_pctg       REAL,
    shooting_pctg_rank  INT,
    shooting_pctg_league_avg REAL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (team_id, season, game_type, location_code)
);

-- Team shot location detail (17 areas)
CREATE TABLE edge_team_shot_locations (
    team_id    BIGINT NOT NULL,
    season     INT NOT NULL REFERENCES seasons(id),
    game_type  game_type NOT NULL,
    area       TEXT NOT NULL,

    FOREIGN KEY (season, team_id) REFERENCES season_teams(season, team_id),

    shots      INT,
    shots_rank INT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (team_id, season, game_type, area)
);

-- Team zone time by strength code (from team-zone-time-details endpoint)
-- DISTINCT from aggregate zone time in edge_team_stats: breaks down by all/es/pp/pk
CREATE TABLE edge_team_zone_time_by_strength (
    team_id         BIGINT NOT NULL,
    season          INT NOT NULL REFERENCES seasons(id),
    game_type       game_type NOT NULL,
    strength_code   TEXT NOT NULL CHECK (strength_code IN ('all', 'es', 'pp', 'pk')),

    FOREIGN KEY (season, team_id) REFERENCES season_teams(season, team_id),

    oz_pctg         REAL,
    oz_rank         INT,
    nz_pctg         REAL,
    nz_rank         INT,
    dz_pctg         REAL,
    dz_rank         INT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (team_id, season, game_type, strength_code)
);

-- Team shot differential by strength code (from team-zone-time-details endpoint)
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
