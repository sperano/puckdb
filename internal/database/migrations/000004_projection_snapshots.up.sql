CREATE TABLE projection_snapshots (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    target_season integer NOT NULL,
    as_of timestamp with time zone NOT NULL,
    source_max_game_date date,
    model_version text NOT NULL,
    config_hash text NOT NULL,
    source_data_hash text NOT NULL,
    lookback_seasons integer NOT NULL,
    season_decay double precision NOT NULL,
    skater_prior_toi_seconds double precision NOT NULL,
    goalie_prior_shots double precision NOT NULL,
    goalie_shutout_min_toi integer NOT NULL,
    max_games double precision NOT NULL,
    interval_z double precision NOT NULL,
    minimum_uncertainty double precision NOT NULL,
    maximum_uncertainty double precision NOT NULL,
    minimum_history_games integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    UNIQUE (target_season, as_of, model_version, config_hash, source_data_hash)
);

CREATE TABLE projection_players (
    snapshot_id uuid NOT NULL REFERENCES projection_snapshots(id) ON DELETE CASCADE,
    player_key text NOT NULL,
    player_id bigint,
    team_id bigint,
    player_kind text NOT NULL CHECK (player_kind IN ('skater', 'goalie')),
    position text NOT NULL,
    source text NOT NULL CHECK (source IN ('internal', 'imported', 'manual')),
    provider text NOT NULL DEFAULT '',
    provider_version text NOT NULL DEFAULT '',
    source_as_of timestamp with time zone NOT NULL,
    incorporates_news_through timestamp with time zone,
    history_seasons integer NOT NULL,
    history_games integer NOT NULL,
    sample_exposure double precision NOT NULL,
    uncertainty double precision NOT NULL,
    insufficient_history boolean NOT NULL,
    missing_stats text[] NOT NULL DEFAULT '{}',
    PRIMARY KEY (snapshot_id, player_key)
);

CREATE INDEX projection_players_player_id_idx
    ON projection_players (player_id, snapshot_id);

CREATE TABLE projection_values (
    snapshot_id uuid NOT NULL,
    player_key text NOT NULL,
    stat text NOT NULL,
    mean double precision NOT NULL,
    low double precision NOT NULL,
    high double precision NOT NULL,
    PRIMARY KEY (snapshot_id, player_key, stat),
    FOREIGN KEY (snapshot_id, player_key)
        REFERENCES projection_players(snapshot_id, player_key) ON DELETE CASCADE,
    CHECK (low <= mean AND mean <= high)
);

CREATE TABLE projection_evaluations (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    model_version text NOT NULL,
    config_hash text NOT NULL,
    source_data_hash text NOT NULL,
    target_season integer NOT NULL,
    as_of timestamp with time zone NOT NULL,
    observed_at timestamp with time zone NOT NULL,
    player_kind text NOT NULL CHECK (player_kind IN ('skater', 'goalie')),
    comparison_model text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CHECK (observed_at >= as_of),
    UNIQUE (
        model_version, config_hash, source_data_hash, target_season,
        as_of, observed_at, player_kind, comparison_model
    )
);

CREATE TABLE projection_evaluation_metrics (
    evaluation_id uuid NOT NULL REFERENCES projection_evaluations(id) ON DELETE CASCADE,
    stat text NOT NULL,
    sample_size integer NOT NULL,
    model_mae double precision NOT NULL,
    model_rmse double precision NOT NULL,
    comparison_mae double precision NOT NULL,
    comparison_rmse double precision NOT NULL,
    PRIMARY KEY (evaluation_id, stat),
    CHECK (stat <> ''),
    CHECK (sample_size > 0),
    CHECK (
        model_mae >= 0 AND model_mae < 'Infinity'::double precision
        AND model_rmse >= 0 AND model_rmse < 'Infinity'::double precision
        AND comparison_mae >= 0 AND comparison_mae < 'Infinity'::double precision
        AND comparison_rmse >= 0 AND comparison_rmse < 'Infinity'::double precision
    )
);
