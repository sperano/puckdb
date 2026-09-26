-- name: ListProjectionSkaterHistory :many
WITH faceoff_totals AS (
    -- One row per (game, player): counts wins and losses from play_events
    -- so the LEFT JOIN below cannot multiply game_skater_stats rows.
    SELECT
        game_id,
        player_id,
        SUM(faceoffs_won)::bigint AS faceoffs_won,
        SUM(faceoffs_lost)::bigint AS faceoffs_lost
    FROM (
        SELECT game_id, winning_player_id AS player_id, 1 AS faceoffs_won, 0 AS faceoffs_lost
        FROM play_events
        WHERE type_desc_key = 'faceoff'
        UNION ALL
        SELECT game_id, losing_player_id AS player_id, 0 AS faceoffs_won, 1 AS faceoffs_lost
        FROM play_events
        WHERE type_desc_key = 'faceoff'
    ) per_faceoff
    GROUP BY game_id, player_id
)
SELECT
    s.player_id,
    g.season,
    (ARRAY_AGG(s.team_id ORDER BY g.game_date DESC, s.game_id DESC))[1]::bigint AS team_id,
    (ARRAY_AGG(s.position ORDER BY g.game_date DESC, s.game_id DESC))[1]::text AS position,
    COUNT(DISTINCT s.game_id)::int AS games_played,
    SUM(s.toi_seconds)::bigint AS toi_seconds,
    SUM(s.goals)::bigint AS goals,
    SUM(s.assists)::bigint AS assists,
    SUM(s.plus_minus)::bigint AS plus_minus,
    SUM(s.penalty_minutes)::bigint AS penalty_minutes,
    SUM(s.power_play_points)::bigint AS power_play_points,
    SUM(s.shots_on_goal)::bigint AS shots_on_goal,
    SUM(s.hits)::bigint AS hits,
    SUM(s.blocked_shots)::bigint AS blocked_shots,
    COALESCE(SUM(fo.faceoffs_won), 0)::bigint AS faceoffs_won,
    COALESCE(SUM(fo.faceoffs_lost), 0)::bigint AS faceoffs_lost
FROM game_skater_stats s
JOIN games g ON g.id = s.game_id
LEFT JOIN faceoff_totals fo ON fo.game_id = s.game_id AND fo.player_id = s.player_id
WHERE g.game_type = 'regular_season'
  AND g.game_state IN ('FINAL', 'OFF')
  AND g.season < $1
  AND g.season >= $2
  AND g.game_date <= $3
GROUP BY s.player_id, g.season
ORDER BY s.player_id, g.season;

-- name: ListProjectionSkaterEvaluationData :many
WITH faceoff_totals AS (
    -- One row per (game, player): counts wins and losses from play_events
    -- so the LEFT JOIN below cannot multiply game_skater_stats rows.
    SELECT
        game_id,
        player_id,
        SUM(faceoffs_won)::bigint AS faceoffs_won,
        SUM(faceoffs_lost)::bigint AS faceoffs_lost
    FROM (
        SELECT game_id, winning_player_id AS player_id, 1 AS faceoffs_won, 0 AS faceoffs_lost
        FROM play_events
        WHERE type_desc_key = 'faceoff'
        UNION ALL
        SELECT game_id, losing_player_id AS player_id, 0 AS faceoffs_won, 1 AS faceoffs_lost
        FROM play_events
        WHERE type_desc_key = 'faceoff'
    ) per_faceoff
    GROUP BY game_id, player_id
)
SELECT
    s.player_id,
    g.season,
    (ARRAY_AGG(s.team_id ORDER BY g.game_date DESC, s.game_id DESC))[1]::bigint AS team_id,
    (ARRAY_AGG(s.position ORDER BY g.game_date DESC, s.game_id DESC))[1]::text AS position,
    COUNT(DISTINCT s.game_id)::int AS games_played,
    SUM(s.toi_seconds)::bigint AS toi_seconds,
    SUM(s.goals)::bigint AS goals,
    SUM(s.assists)::bigint AS assists,
    SUM(s.plus_minus)::bigint AS plus_minus,
    SUM(s.penalty_minutes)::bigint AS penalty_minutes,
    SUM(s.power_play_points)::bigint AS power_play_points,
    SUM(s.shots_on_goal)::bigint AS shots_on_goal,
    SUM(s.hits)::bigint AS hits,
    SUM(s.blocked_shots)::bigint AS blocked_shots,
    COALESCE(SUM(fo.faceoffs_won), 0)::bigint AS faceoffs_won,
    COALESCE(SUM(fo.faceoffs_lost), 0)::bigint AS faceoffs_lost
FROM game_skater_stats s
JOIN games g ON g.id = s.game_id
LEFT JOIN faceoff_totals fo ON fo.game_id = s.game_id AND fo.player_id = s.player_id
WHERE g.game_type = 'regular_season'
  AND g.game_state IN ('FINAL', 'OFF')
  AND g.season <= $1
  AND g.season >= $2
GROUP BY s.player_id, g.season
ORDER BY s.player_id, g.season;

-- name: ListProjectionGoalieHistory :many
WITH projection_constants AS (
    SELECT sqlc.arg(minimum_shutout_toi_seconds)::integer AS minimum_shutout_toi_seconds
)
SELECT
    s.player_id,
    g.season,
    (ARRAY_AGG(s.team_id ORDER BY g.game_date DESC, s.game_id DESC))[1]::bigint AS team_id,
    COUNT(DISTINCT s.game_id)::int AS games_played,
    COUNT(*) FILTER (WHERE s.starter = TRUE)::int AS games_started,
    SUM(s.toi_seconds)::bigint AS toi_seconds,
    COUNT(*) FILTER (WHERE s.decision = 'W')::int AS wins,
    COUNT(*) FILTER (
        WHERE s.starter = TRUE AND s.goals_against = 0
          AND s.toi_seconds >= projection_constants.minimum_shutout_toi_seconds
    )::int AS shutouts,
    SUM(s.shots_against)::bigint AS shots_against,
    SUM(s.saves)::bigint AS saves,
    SUM(s.goals_against)::bigint AS goals_against
FROM game_goalie_stats s
JOIN games g ON g.id = s.game_id
CROSS JOIN projection_constants
WHERE g.game_type = 'regular_season'
  AND g.game_state IN ('FINAL', 'OFF')
  AND g.season < $1
  AND g.season >= $2
  AND g.game_date <= $3
GROUP BY s.player_id, g.season
ORDER BY s.player_id, g.season;

-- name: ListProjectionGoalieEvaluationData :many
WITH projection_constants AS (
    SELECT sqlc.arg(minimum_shutout_toi_seconds)::integer AS minimum_shutout_toi_seconds
)
SELECT
    s.player_id,
    g.season,
    (ARRAY_AGG(s.team_id ORDER BY g.game_date DESC, s.game_id DESC))[1]::bigint AS team_id,
    COUNT(DISTINCT s.game_id)::int AS games_played,
    COUNT(*) FILTER (WHERE s.starter = TRUE)::int AS games_started,
    SUM(s.toi_seconds)::bigint AS toi_seconds,
    COUNT(*) FILTER (WHERE s.decision = 'W')::int AS wins,
    COUNT(*) FILTER (
        WHERE s.starter = TRUE AND s.goals_against = 0
          AND s.toi_seconds >= projection_constants.minimum_shutout_toi_seconds
    )::int AS shutouts,
    SUM(s.shots_against)::bigint AS shots_against,
    SUM(s.saves)::bigint AS saves,
    SUM(s.goals_against)::bigint AS goals_against
FROM game_goalie_stats s
JOIN games g ON g.id = s.game_id
CROSS JOIN projection_constants
WHERE g.game_type = 'regular_season'
  AND g.game_state IN ('FINAL', 'OFF')
  AND g.season <= $1
  AND g.season >= $2
GROUP BY s.player_id, g.season
ORDER BY s.player_id, g.season;

-- name: GetProjectionSourceMaxGameDate :one
SELECT MAX(game_date)::date
FROM games
WHERE game_type = 'regular_season'
  AND game_state IN ('FINAL', 'OFF')
  AND season < $1
  AND season >= $2
  AND game_date <= $3;

-- name: CreateProjectionSnapshot :one
INSERT INTO projection_snapshots (
    target_season, as_of, source_max_game_date, model_version, config_hash, source_data_hash,
    lookback_seasons, season_decay, skater_prior_toi_seconds,
    goalie_prior_shots, goalie_shutout_min_toi, max_games, interval_z, minimum_uncertainty,
    maximum_uncertainty, minimum_history_games
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9,
    $10, $11, $12, $13,
    $14, $15, $16
)
ON CONFLICT (target_season, as_of, model_version, config_hash, source_data_hash) DO UPDATE SET
    source_max_game_date = EXCLUDED.source_max_game_date
RETURNING *;

-- name: CreateProjectionPlayer :exec
INSERT INTO projection_players (
    snapshot_id, player_key, player_id, team_id, player_kind, position, source,
    provider, provider_version, source_as_of, incorporates_news_through,
    history_seasons, history_games, sample_exposure, uncertainty, insufficient_history,
    missing_stats
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11,
    $12, $13, $14, $15, $16,
    $17
);

-- name: CreateProjectionValue :exec
INSERT INTO projection_values (
    snapshot_id, player_key, stat, mean, low, high
) VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetProjectionSnapshot :one
SELECT * FROM projection_snapshots WHERE id = $1;

-- name: ListProjectionPlayers :many
SELECT * FROM projection_players
WHERE snapshot_id = $1
ORDER BY player_key;

-- name: ListProjectionValues :many
SELECT * FROM projection_values
WHERE snapshot_id = $1
ORDER BY player_key, stat;

-- name: DeleteProjectionPlayersBySnapshot :exec
DELETE FROM projection_players WHERE snapshot_id = $1;

-- name: CreateProjectionEvaluation :one
INSERT INTO projection_evaluations (
    model_version, config_hash, source_data_hash, target_season, as_of,
    observed_at, player_kind, comparison_model
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (
    model_version, config_hash, source_data_hash, target_season,
    as_of, observed_at, player_kind, comparison_model
) DO UPDATE SET observed_at = EXCLUDED.observed_at
RETURNING *;

-- name: CreateProjectionEvaluationMetric :exec
INSERT INTO projection_evaluation_metrics (
    evaluation_id, stat, sample_size, model_mae, model_rmse,
    comparison_mae, comparison_rmse
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (evaluation_id, stat) DO UPDATE SET
    sample_size = EXCLUDED.sample_size,
    model_mae = EXCLUDED.model_mae,
    model_rmse = EXCLUDED.model_rmse,
    comparison_mae = EXCLUDED.comparison_mae,
    comparison_rmse = EXCLUDED.comparison_rmse;

-- name: DeleteProjectionEvaluationMetrics :exec
DELETE FROM projection_evaluation_metrics WHERE evaluation_id = $1;
