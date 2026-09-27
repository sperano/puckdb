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
    p.birth_date,
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
JOIN players p ON p.id = s.player_id
LEFT JOIN faceoff_totals fo ON fo.game_id = s.game_id AND fo.player_id = s.player_id
WHERE g.game_type = 'regular_season'
  AND g.game_state IN ('FINAL', 'OFF')
  AND g.season < $1
  AND g.season >= $2
  AND g.game_date <= $3
GROUP BY s.player_id, p.birth_date, g.season
ORDER BY s.player_id, g.season;

-- name: ListProjectionSkaterLinemateContext :many
-- Split shift charts into atomic half-open intervals, retain equal-strength
-- 3v3 through 5v5 segments, and return exact integer teammate overlap with
-- even-strength production from the same shift-covered games. Go accumulates
-- rates in this stable order so floating-point sums cannot perturb hashes.
WITH eligible_games AS (
    SELECT id, season
    FROM games
    WHERE game_type = 'regular_season'
      AND game_state IN ('FINAL', 'OFF')
      AND season >= sqlc.arg(min_season)
      AND season <= sqlc.arg(max_season)
      AND game_date <= sqlc.arg(game_date)
),
parsed_shifts AS (
    SELECT
        s.game_id,
        g.season,
        s.player_id,
        s.team_id,
        s.period,
        stats.player_id IS NOT NULL AS is_skater,
        CASE WHEN s.start_time ~ '^[0-9]{1,2}:[0-5][0-9]$' THEN
            split_part(s.start_time, ':', 1)::integer * 60
                + split_part(s.start_time, ':', 2)::integer
        END AS start_second,
        CASE WHEN s.end_time ~ '^[0-9]{1,2}:[0-5][0-9]$' THEN
            split_part(s.end_time, ':', 1)::integer * 60
                + split_part(s.end_time, ':', 2)::integer
        END AS end_second
    FROM shifts s
    JOIN eligible_games g ON g.id = s.game_id
    LEFT JOIN game_skater_stats stats
      ON stats.game_id = s.game_id
     AND stats.player_id = s.player_id
     AND stats.team_id = s.team_id
    LEFT JOIN game_goalie_stats goalies
      ON goalies.game_id = s.game_id
     AND goalies.player_id = s.player_id
     AND goalies.team_id = s.team_id
    WHERE s.type_code = '517'
      AND (stats.player_id IS NOT NULL OR goalies.player_id IS NOT NULL)
),
valid_shifts AS (
    SELECT *
    FROM parsed_shifts
    WHERE start_second IS NOT NULL
      AND end_second IS NOT NULL
      AND start_second < end_second
),
boundaries AS (
    SELECT game_id, season, period, start_second AS second FROM valid_shifts
    UNION
    SELECT game_id, season, period, end_second AS second FROM valid_shifts
),
segments AS (
    SELECT
        game_id,
        season,
        period,
        second AS start_second,
        lead(second) OVER (
            PARTITION BY game_id, period
            ORDER BY second
        ) AS end_second
    FROM boundaries
),
active_skaters AS (
    SELECT DISTINCT
        segment.game_id,
        segment.season,
        segment.period,
        segment.start_second,
        segment.end_second,
        shift_row.team_id,
        shift_row.player_id,
        shift_row.is_skater
    FROM segments segment
    JOIN valid_shifts shift_row
      ON shift_row.game_id = segment.game_id
     AND shift_row.period = segment.period
     AND shift_row.start_second < segment.end_second
     AND shift_row.end_second > segment.start_second
    WHERE segment.end_second > segment.start_second
),
team_strength AS (
    SELECT
        game_id,
        season,
        period,
        start_second,
        end_second,
        team_id,
        count(*) FILTER (WHERE is_skater) AS skater_count,
        count(*) FILTER (WHERE NOT is_skater) AS goalie_count
    FROM active_skaters
    GROUP BY game_id, season, period, start_second, end_second, team_id
),
even_segments AS (
    SELECT game_id, season, period, start_second, end_second
    FROM team_strength
    GROUP BY game_id, season, period, start_second, end_second
    HAVING count(*) = 2
       AND min(skater_count) = max(skater_count)
       AND min(skater_count) BETWEEN 3 AND 5
       AND min(goalie_count) = 1
       AND max(goalie_count) = 1
),
even_skaters AS (
    SELECT active.*
    FROM active_skaters active
    JOIN even_segments segment
      USING (game_id, season, period, start_second, end_second)
    WHERE active.is_skater
),
pair_overlap AS (
    SELECT
        player.season,
        player.player_id,
        teammate.player_id AS teammate_id,
        sum(player.end_second - player.start_second)::bigint AS shared_toi_seconds
    FROM even_skaters player
    JOIN even_skaters teammate
      ON teammate.game_id = player.game_id
     AND teammate.period = player.period
     AND teammate.start_second = player.start_second
     AND teammate.end_second = player.end_second
     AND teammate.team_id = player.team_id
     AND teammate.player_id <> player.player_id
    GROUP BY player.season, player.player_id, teammate.player_id
),
even_player_toi AS (
    SELECT
        game_id,
        season,
        player_id,
        sum(end_second - start_second)::bigint AS even_strength_toi_seconds
    FROM even_skaters
    GROUP BY game_id, season, player_id
),
even_strength_points AS (
    SELECT
        game.id AS game_id,
        game.season,
        scorer.player_id,
        count(*)::bigint AS even_strength_points
    FROM play_events event
    JOIN eligible_games game ON game.id = event.game_id
    CROSS JOIN LATERAL (
        SELECT CASE WHEN event.time_in_period ~ '^[0-9]{1,2}:[0-5][0-9]$' THEN
            split_part(event.time_in_period, ':', 1)::integer * 60
                + split_part(event.time_in_period, ':', 2)::integer
        END AS event_second
    ) clock
    CROSS JOIN LATERAL unnest(ARRAY[
        event.scoring_player_id,
        event.assist1_player_id,
        event.assist2_player_id
    ]) AS scorer(player_id)
    JOIN even_skaters coverage
      ON coverage.game_id = event.game_id
     AND coverage.period = event.period
     AND coverage.player_id = scorer.player_id
     AND clock.event_second > coverage.start_second
     AND clock.event_second <= coverage.end_second
    WHERE event.type_desc_key = 'goal'
      AND scorer.player_id IS NOT NULL
      AND clock.event_second IS NOT NULL
      AND event.situation_code IS NOT NULL
      AND event.situation_code / 1000 % 10 = 1
      AND event.situation_code % 10 = 1
      AND event.situation_code / 100 % 10 = event.situation_code / 10 % 10
      AND event.situation_code / 100 % 10 BETWEEN 3 AND 5
    GROUP BY game.id, game.season, scorer.player_id
),
teammate_rates AS (
    SELECT
        toi.season,
        toi.player_id,
        coalesce(sum(points.even_strength_points), 0)::bigint AS even_strength_points,
        sum(toi.even_strength_toi_seconds)::bigint AS even_strength_toi_seconds
    FROM even_player_toi toi
    LEFT JOIN even_strength_points points
      ON points.game_id = toi.game_id
     AND points.player_id = toi.player_id
    GROUP BY toi.season, toi.player_id
)
SELECT
    pair.player_id,
    pair.season,
    pair.teammate_id,
    pair.shared_toi_seconds,
    teammate_toi.even_strength_points AS teammate_even_strength_points,
    teammate_toi.even_strength_toi_seconds AS teammate_even_strength_toi_seconds
FROM pair_overlap pair
JOIN teammate_rates teammate_toi
  ON teammate_toi.season = pair.season
 AND teammate_toi.player_id = pair.teammate_id
ORDER BY pair.player_id, pair.season, pair.teammate_id;

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
    p.birth_date,
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
JOIN players p ON p.id = s.player_id
LEFT JOIN faceoff_totals fo ON fo.game_id = s.game_id AND fo.player_id = s.player_id
WHERE g.game_type = 'regular_season'
  AND g.game_state IN ('FINAL', 'OFF')
  AND g.season <= $1
  AND g.season >= $2
GROUP BY s.player_id, p.birth_date, g.season
ORDER BY s.player_id, g.season;

-- name: ListProjectionGoalieHistory :many
WITH projection_constants AS (
    SELECT sqlc.arg(minimum_shutout_toi_seconds)::integer AS minimum_shutout_toi_seconds
)
SELECT
    s.player_id,
    p.birth_date,
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
JOIN players p ON p.id = s.player_id
CROSS JOIN projection_constants
WHERE g.game_type = 'regular_season'
  AND g.game_state IN ('FINAL', 'OFF')
  AND g.season < $1
  AND g.season >= $2
  AND g.game_date <= $3
GROUP BY s.player_id, p.birth_date, g.season
ORDER BY s.player_id, g.season;

-- name: ListProjectionGoalieEvaluationData :many
WITH projection_constants AS (
    SELECT sqlc.arg(minimum_shutout_toi_seconds)::integer AS minimum_shutout_toi_seconds
)
SELECT
    s.player_id,
    p.birth_date,
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
JOIN players p ON p.id = s.player_id
CROSS JOIN projection_constants
WHERE g.game_type = 'regular_season'
  AND g.game_state IN ('FINAL', 'OFF')
  AND g.season <= $1
  AND g.season >= $2
GROUP BY s.player_id, p.birth_date, g.season
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
    maximum_uncertainty, minimum_history_games, linemate_regression_strength, aging_curve
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9,
    $10, $11, $12, $13,
    $14, $15, $16, $17, $18
)
ON CONFLICT (target_season, as_of, model_version, config_hash, source_data_hash) DO UPDATE SET
    source_max_game_date = EXCLUDED.source_max_game_date
RETURNING *;

-- name: CreateProjectionPlayer :exec
INSERT INTO projection_players (
    snapshot_id, player_key, player_id, team_id, player_kind, position, source,
    provider, provider_version, source_as_of, incorporates_news_through,
    history_seasons, history_games, sample_exposure, uncertainty, insufficient_history,
    missing_stats, linemate_observed_points_per_60, linemate_average_points_per_60,
    linemate_shared_toi_seconds, linemate_adjustment_factor
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11,
    $12, $13, $14, $15, $16,
    $17, $18, $19, $20, $21
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
