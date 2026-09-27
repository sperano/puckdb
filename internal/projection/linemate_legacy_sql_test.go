package projection

// legacyLinemateContextSQL is ListProjectionSkaterLinemateContext as it was
// before even_strength_segments existed: it derived the segments from shifts
// at query time. It is kept only so the equivalence test can prove the
// precomputed table returns identical rows in identical order.
const legacyLinemateContextSQL = `
WITH eligible_games AS (
    SELECT id, season
    FROM games
    WHERE game_type = 'regular_season'
      AND game_state IN ('FINAL', 'OFF')
      AND season >= $1
      AND season <= $2
      AND game_date <= $3
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
    SELECT game_id, season, player_id, team_id, period, is_skater, start_second, end_second
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
    SELECT active.game_id, active.season, active.period, active.start_second, active.end_second, active.team_id, active.player_id, active.is_skater
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
ORDER BY pair.player_id, pair.season, pair.teammate_id
`
