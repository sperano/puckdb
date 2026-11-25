-- name: GetSkaterThresholdSeasons :many
-- Skaters with at least min_seasons seasons whose stat reached min_value.
-- A season's line is the sum of the player's club rows of that season and
-- game type, so a traded player's season counts once, with every club.
WITH season_values AS (
    SELECT s.player_id, s.season,
           SUM(CASE sqlc.arg(stat)::text
                   WHEN 'points' THEN s.points
                   WHEN 'goals' THEN s.goals
                   WHEN 'assists' THEN s.assists
                   WHEN 'plus_minus' THEN s.plus_minus
                   WHEN 'pim' THEN s.penalty_minutes
                   WHEN 'ppg' THEN s.power_play_goals
                   WHEN 'shg' THEN s.shorthanded_goals
                   WHEN 'gwg' THEN s.game_winning_goals
                   WHEN 'otg' THEN s.overtime_goals
                   WHEN 'sog' THEN s.shots
                   WHEN 'gp' THEN s.games_played
               END)::integer AS value
    FROM club_skater_stats s
    WHERE s.game_type = sqlc.arg(game_type)
      AND s.season BETWEEN sqlc.arg(from_season)::integer AND sqlc.arg(to_season)::integer
    GROUP BY s.player_id, s.season
)
SELECT v.player_id, p.first_name, p.last_name, p.position,
       COUNT(*)::integer AS qualifying_seasons,
       MAX(v.value)::integer AS best_value,
       string_agg(v.season::text || ':' || v.value::text, ' ' ORDER BY v.season)::text AS seasons
FROM season_values v
JOIN players p ON p.id = v.player_id
WHERE v.value >= sqlc.arg(min_value)::integer
GROUP BY v.player_id, p.first_name, p.last_name, p.position
HAVING COUNT(*) >= sqlc.arg(min_seasons)::integer
ORDER BY qualifying_seasons DESC, best_value DESC, p.last_name, p.first_name, v.player_id
LIMIT sqlc.arg(result_limit);

-- name: GetGoalieThresholdSeasons :many
-- GetSkaterThresholdSeasons for goalies, over club_goalie_stats.
WITH season_values AS (
    SELECT s.player_id, s.season,
           SUM(CASE sqlc.arg(stat)::text
                   WHEN 'wins' THEN s.wins
                   WHEN 'losses' THEN s.losses
                   WHEN 'otl' THEN s.overtime_losses
                   WHEN 'shutouts' THEN s.shutouts
                   WHEN 'saves' THEN s.saves
                   WHEN 'shots_against' THEN s.shots_against
                   WHEN 'gp' THEN s.games_played
                   WHEN 'gs' THEN s.games_started
               END)::integer AS value
    FROM club_goalie_stats s
    WHERE s.game_type = sqlc.arg(game_type)
      AND s.season BETWEEN sqlc.arg(from_season)::integer AND sqlc.arg(to_season)::integer
    GROUP BY s.player_id, s.season
)
SELECT v.player_id, p.first_name, p.last_name,
       COUNT(*)::integer AS qualifying_seasons,
       MAX(v.value)::integer AS best_value,
       string_agg(v.season::text || ':' || v.value::text, ' ' ORDER BY v.season)::text AS seasons
FROM season_values v
JOIN players p ON p.id = v.player_id
WHERE v.value >= sqlc.arg(min_value)::integer
GROUP BY v.player_id, p.first_name, p.last_name
HAVING COUNT(*) >= sqlc.arg(min_seasons)::integer
ORDER BY qualifying_seasons DESC, best_value DESC, p.last_name, p.first_name, v.player_id
LIMIT sqlc.arg(result_limit);
