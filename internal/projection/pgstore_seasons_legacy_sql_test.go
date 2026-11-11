package projection

// The four queries ListProjectionSkaterSeasons and ListProjectionGoalieSeasons
// replaced, exactly as sqlc generated them, kept only so
// TestSeasonQueriesMatchLegacyQueries can prove the merged queries return
// identical rows in identical order for both windows. The history queries
// take (target season, floor season, as-of cutoff), the evaluation queries
// (target season, floor season); the goalie queries add the minimum shutout
// TOI as their last argument.

const legacySkaterHistorySQL = `
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
ORDER BY s.player_id, g.season`

const legacySkaterEvaluationSQL = `
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
ORDER BY s.player_id, g.season`

const legacyGoalieHistorySQL = `
WITH projection_constants AS (
    SELECT $4::integer AS minimum_shutout_toi_seconds
)
SELECT
    s.player_id,
    p.birth_date,
    g.season,
    (ARRAY_AGG(s.team_id ORDER BY g.game_date DESC, s.game_id DESC))[1]::bigint AS team_id,
    COUNT(DISTINCT s.game_id)::int AS games_played,
    -- games_played counts every boxscore row, including games dressed as
    -- the backup with no time on ice; games_appeared counts the games played
    -- (nhl-baseline-v6 reads it, earlier versions keep games_played).
    COUNT(DISTINCT s.game_id) FILTER (WHERE s.toi_seconds > 0)::int AS games_appeared,
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
ORDER BY s.player_id, g.season`

const legacyGoalieEvaluationSQL = `
WITH projection_constants AS (
    SELECT $3::integer AS minimum_shutout_toi_seconds
)
SELECT
    s.player_id,
    p.birth_date,
    g.season,
    (ARRAY_AGG(s.team_id ORDER BY g.game_date DESC, s.game_id DESC))[1]::bigint AS team_id,
    COUNT(DISTINCT s.game_id)::int AS games_played,
    -- games_played counts every boxscore row, including games dressed as
    -- the backup with no time on ice; games_appeared counts the games played
    -- (nhl-baseline-v6 reads it, earlier versions keep games_played).
    COUNT(DISTINCT s.game_id) FILTER (WHERE s.toi_seconds > 0)::int AS games_appeared,
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
ORDER BY s.player_id, g.season`
