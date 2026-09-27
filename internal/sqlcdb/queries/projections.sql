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
-- Return exact integer teammate overlap with even-strength production from
-- the same shift-covered games. even_strength_segments holds each game's
-- equal-strength 3v3 through 5v5 atomic segments, built at shift chart
-- import (see InsertEvenStrengthSegmentsForGame); this query only filters
-- eligible games and aggregates. Teammates share a segment when they share
-- its (game, period, start second) and club. Go accumulates rates in this
-- stable order so floating-point sums cannot perturb hashes.
WITH eligible_games AS NOT MATERIALIZED (
    SELECT id, season
    FROM games
    WHERE game_type = 'regular_season'
      AND game_state IN ('FINAL', 'OFF')
      AND season >= sqlc.arg(min_season)
      AND season <= sqlc.arg(max_season)
      AND game_date <= sqlc.arg(game_date)
),
pair_overlap AS (
    SELECT
        game.season,
        player.player_id,
        teammate.player_id AS teammate_id,
        sum(player.end_second - player.start_second)::bigint AS shared_toi_seconds
    FROM eligible_games game
    JOIN even_strength_segments player ON player.game_id = game.id
    JOIN even_strength_segments teammate
      ON teammate.game_id = player.game_id
     AND teammate.period = player.period
     AND teammate.start_second = player.start_second
     AND teammate.team_id = player.team_id
     AND teammate.player_id <> player.player_id
    GROUP BY game.season, player.player_id, teammate.player_id
),
even_player_toi AS (
    SELECT
        segment.game_id,
        game.season,
        segment.player_id,
        sum(segment.end_second - segment.start_second)::bigint AS even_strength_toi_seconds
    FROM eligible_games game
    JOIN even_strength_segments segment ON segment.game_id = game.id
    GROUP BY segment.game_id, game.season, segment.player_id
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
    JOIN even_strength_segments coverage
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

-- name: ListProjectionTeamSeasons :many
-- Each NHL club's regular-season environment per season, for the
-- team-environment adjustment (internal/projection/teamenv.go), over the
-- same games and window as ListProjectionSkaterHistory (season_2 <= season
-- < season, completed games through game_date). goals_for drops the
-- shootout winner's extra goal; shots_for is the club's shots on goal.
-- Power-play opportunities are the minor and bench-minor penalties charged
-- to the opponent (coincidental minors over-count slightly), counted only
-- in games that have play-by-play (power_play_games) so a game whose
-- events were never imported does not read as zero opportunities.
WITH club_games AS (
    SELECT g.id AS game_id, g.season, g.home_team_id AS team_id, g.away_team_id AS opponent_id,
           g.home_team_score - CASE
               WHEN g.period_type = 'SO' AND g.home_team_score > g.away_team_score THEN 1 ELSE 0
           END AS goals_for,
           g.home_team_sog AS shots_for
    FROM games g
    WHERE g.game_type = 'regular_season'
      AND g.game_state IN ('FINAL', 'OFF')
      AND g.season < $1
      AND g.season >= $2
      AND g.game_date <= $3
    UNION ALL
    SELECT g.id, g.season, g.away_team_id, g.home_team_id,
           g.away_team_score - CASE
               WHEN g.period_type = 'SO' AND g.away_team_score > g.home_team_score THEN 1 ELSE 0
           END,
           g.away_team_sog
    FROM games g
    WHERE g.game_type = 'regular_season'
      AND g.game_state IN ('FINAL', 'OFF')
      AND g.season < $1
      AND g.season >= $2
      AND g.game_date <= $3
),
club_power_play AS (
    SELECT cg.game_id, cg.team_id,
           EXISTS (SELECT 1 FROM play_events pe WHERE pe.game_id = cg.game_id) AS has_play_by_play,
           (SELECT COUNT(*) FROM play_events pe
            WHERE pe.game_id = cg.game_id
              AND pe.event_owner_team_id = cg.opponent_id
              AND pe.type_desc_key = 'penalty'
              AND pe.penalty_type_code IN ('MIN', 'BEN')) AS opportunities
    FROM club_games cg
)
SELECT
    cg.team_id,
    cg.season,
    COALESCE(MAX(st.abbrev), '')::text AS abbrev,
    COUNT(*)::int AS games_played,
    SUM(cg.goals_for)::bigint AS goals_for,
    SUM(cg.shots_for)::bigint AS shots_for,
    COUNT(*) FILTER (WHERE pp.has_play_by_play)::int AS power_play_games,
    COALESCE(SUM(pp.opportunities) FILTER (WHERE pp.has_play_by_play), 0)::bigint AS power_play_opportunities
FROM club_games cg
JOIN club_power_play pp ON pp.game_id = cg.game_id AND pp.team_id = cg.team_id
LEFT JOIN season_teams st ON st.season = cg.season AND st.team_id = cg.team_id
GROUP BY cg.team_id, cg.season
ORDER BY cg.team_id, cg.season;

-- name: ListProjectionSkaterClubGames :many
-- Per-club games of the skater seasons that were split between clubs (a
-- trade), over the same games and window as ListProjectionSkaterHistory,
-- whose single row per season names only the last club. The
-- team-environment adjustment weighs a player's history by the clubs the
-- games were actually played for; a season with one club needs no row here.
WITH club_games AS (
    SELECT
        s.player_id,
        g.season,
        s.team_id,
        COUNT(DISTINCT s.game_id)::int AS games_played,
        COUNT(*) OVER (PARTITION BY s.player_id, g.season) AS clubs
    FROM game_skater_stats s
    JOIN games g ON g.id = s.game_id
    WHERE g.game_type = 'regular_season'
      AND g.game_state IN ('FINAL', 'OFF')
      AND g.season < $1
      AND g.season >= $2
      AND g.game_date <= $3
    GROUP BY s.player_id, g.season, s.team_id
)
SELECT player_id, season, team_id, games_played
FROM club_games
WHERE clubs > 1
ORDER BY player_id, season, team_id;

-- name: ListProjectionTargetTeams :many
-- Each player's NHL club for the target season, from its imported rosters:
-- the most recently updated roster row wins when a player appears on more
-- than one club (the same rule as the stand-in draft pool). Empty until the
-- season's rosters are imported, which leaves the team-environment
-- adjustment off.
SELECT DISTINCT ON (r.player_id) r.player_id, r.team_id
FROM season_rosters r
JOIN season_teams st ON st.season = r.season AND st.team_id = r.team_id
WHERE r.season = $1 AND st.team_kind = 'nhl'
ORDER BY r.player_id, r.updated_at DESC, r.team_id;

-- name: ListProjectionEvaluationTargetTeams :many
-- Each skater's first club of a held-out season: the club a preseason
-- roster would have shown, for EvaluateTeamChanges.
SELECT DISTINCT ON (s.player_id) s.player_id, s.team_id
FROM game_skater_stats s
JOIN games g ON g.id = s.game_id
WHERE g.game_type = 'regular_season'
  AND g.game_state IN ('FINAL', 'OFF')
  AND g.season = $1
ORDER BY s.player_id, g.game_date, s.game_id;

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
    maximum_uncertainty, minimum_history_games, linemate_regression_strength, aging_curve,
    team_environment_prior_games, team_environment_max_change
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9,
    $10, $11, $12, $13,
    $14, $15, $16, $17, $18,
    $19, $20
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
    linemate_shared_toi_seconds, linemate_adjustment_factor, team_environment
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11,
    $12, $13, $14, $15, $16,
    $17, $18, $19, $20, $21,
    $22
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
