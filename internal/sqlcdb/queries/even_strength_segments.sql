-- name: DeleteEvenStrengthSegmentsForGame :exec
DELETE FROM even_strength_segments
WHERE game_id = sqlc.arg(game_id);

-- name: InsertEvenStrengthSegmentsForGame :execrows
-- Split one game's shift chart into atomic half-open intervals and store the
-- skaters of every equal-strength 3v3 through 5v5 segment with exactly one
-- goalie per club. Shifts are identified as skater or goalie through the
-- game's box-score rows, so box scores must be imported first; shifts in
-- neither are dropped. Goalie shifts still add boundaries and count toward
-- goalie_count. Callers delete the game's rows first, in the same
-- transaction (see DeleteEvenStrengthSegmentsForGame).
WITH parsed_shifts AS (
    SELECT
        s.game_id,
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
    LEFT JOIN game_skater_stats stats
      ON stats.game_id = s.game_id
     AND stats.player_id = s.player_id
     AND stats.team_id = s.team_id
    LEFT JOIN game_goalie_stats goalies
      ON goalies.game_id = s.game_id
     AND goalies.player_id = s.player_id
     AND goalies.team_id = s.team_id
    WHERE s.game_id = sqlc.arg(game_id)
      AND s.type_code = '517'
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
    SELECT game_id, period, start_second AS second FROM valid_shifts
    UNION
    SELECT game_id, period, end_second AS second FROM valid_shifts
),
segments AS (
    SELECT
        game_id,
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
        period,
        start_second,
        end_second,
        team_id,
        count(*) FILTER (WHERE is_skater) AS skater_count,
        count(*) FILTER (WHERE NOT is_skater) AS goalie_count
    FROM active_skaters
    GROUP BY game_id, period, start_second, end_second, team_id
),
even_segments AS (
    SELECT game_id, period, start_second, end_second
    FROM team_strength
    GROUP BY game_id, period, start_second, end_second
    HAVING count(*) = 2
       AND min(skater_count) = max(skater_count)
       AND min(skater_count) BETWEEN 3 AND 5
       AND min(goalie_count) = 1
       AND max(goalie_count) = 1
)
INSERT INTO even_strength_segments (
    game_id, period, start_second, end_second, team_id, player_id
)
SELECT
    active.game_id,
    active.period,
    active.start_second,
    active.end_second,
    active.team_id,
    active.player_id
FROM active_skaters active
JOIN even_segments segment
  USING (game_id, period, start_second, end_second)
WHERE active.is_skater;
