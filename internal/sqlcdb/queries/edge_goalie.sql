-- =============================================================================
-- Edge Goalie Queries
-- =============================================================================

-- name: UpsertEdgeGoalieStats :exec
INSERT INTO edge_goalie_stats (
    player_id, season, game_type,
    gaa_value, gaa_percentile, gaa_league_avg,
    games_above_900_value, games_above_900_percentile, games_above_900_league_avg,
    goal_diff_per_60_value, goal_diff_per_60_percentile, goal_diff_per_60_league_avg,
    goal_support_avg_value, goal_support_avg_percentile, goal_support_avg_league_avg,
    point_pctg_value, point_pctg_percentile, point_pctg_league_avg
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
ON CONFLICT (player_id, season, game_type) DO UPDATE SET
    gaa_value = EXCLUDED.gaa_value,
    gaa_percentile = EXCLUDED.gaa_percentile,
    gaa_league_avg = EXCLUDED.gaa_league_avg,
    games_above_900_value = EXCLUDED.games_above_900_value,
    games_above_900_percentile = EXCLUDED.games_above_900_percentile,
    games_above_900_league_avg = EXCLUDED.games_above_900_league_avg,
    goal_diff_per_60_value = EXCLUDED.goal_diff_per_60_value,
    goal_diff_per_60_percentile = EXCLUDED.goal_diff_per_60_percentile,
    goal_diff_per_60_league_avg = EXCLUDED.goal_diff_per_60_league_avg,
    goal_support_avg_value = EXCLUDED.goal_support_avg_value,
    goal_support_avg_percentile = EXCLUDED.goal_support_avg_percentile,
    goal_support_avg_league_avg = EXCLUDED.goal_support_avg_league_avg,
    point_pctg_value = EXCLUDED.point_pctg_value,
    point_pctg_percentile = EXCLUDED.point_pctg_percentile,
    point_pctg_league_avg = EXCLUDED.point_pctg_league_avg,
    updated_at = NOW()
WHERE (edge_goalie_stats.gaa_value, edge_goalie_stats.gaa_percentile,
       edge_goalie_stats.games_above_900_value, edge_goalie_stats.games_above_900_percentile,
       edge_goalie_stats.goal_diff_per_60_value, edge_goalie_stats.goal_diff_per_60_percentile,
       edge_goalie_stats.goal_support_avg_value, edge_goalie_stats.goal_support_avg_percentile,
       edge_goalie_stats.point_pctg_value, edge_goalie_stats.point_pctg_percentile)
      IS DISTINCT FROM
      (EXCLUDED.gaa_value, EXCLUDED.gaa_percentile,
       EXCLUDED.games_above_900_value, EXCLUDED.games_above_900_percentile,
       EXCLUDED.goal_diff_per_60_value, EXCLUDED.goal_diff_per_60_percentile,
       EXCLUDED.goal_support_avg_value, EXCLUDED.goal_support_avg_percentile,
       EXCLUDED.point_pctg_value, EXCLUDED.point_pctg_percentile);

-- name: DeleteEdgeGoalieShotLocationSummary :exec
DELETE FROM edge_goalie_shot_location_summary
WHERE player_id = $1 AND season = $2 AND game_type = $3;

-- name: UpsertEdgeGoalieShotLocationSummary :exec
INSERT INTO edge_goalie_shot_location_summary (
    player_id, season, game_type, location_code,
    goals_against, goals_against_percentile, goals_against_league_avg,
    saves, saves_percentile, saves_league_avg,
    save_pctg, save_pctg_percentile, save_pctg_league_avg
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (player_id, season, game_type, location_code) DO UPDATE SET
    goals_against = EXCLUDED.goals_against,
    goals_against_percentile = EXCLUDED.goals_against_percentile,
    goals_against_league_avg = EXCLUDED.goals_against_league_avg,
    saves = EXCLUDED.saves,
    saves_percentile = EXCLUDED.saves_percentile,
    saves_league_avg = EXCLUDED.saves_league_avg,
    save_pctg = EXCLUDED.save_pctg,
    save_pctg_percentile = EXCLUDED.save_pctg_percentile,
    save_pctg_league_avg = EXCLUDED.save_pctg_league_avg;

-- name: DeleteEdgeGoalieShotLocations :exec
DELETE FROM edge_goalie_shot_locations
WHERE player_id = $1 AND season = $2 AND game_type = $3;

-- name: UpsertEdgeGoalieShotLocation :exec
INSERT INTO edge_goalie_shot_locations (
    player_id, season, game_type, area,
    saves, saves_percentile, save_pctg, save_pctg_percentile
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (player_id, season, game_type, area) DO UPDATE SET
    saves = EXCLUDED.saves,
    saves_percentile = EXCLUDED.saves_percentile,
    save_pctg = EXCLUDED.save_pctg,
    save_pctg_percentile = EXCLUDED.save_pctg_percentile;

-- name: GetEdgeGoalieStats :one
SELECT * FROM edge_goalie_stats
WHERE player_id = $1 AND season = $2 AND game_type = $3;

-- name: GetEdgeGoalieStatsBySeason :many
SELECT s.*, p.first_name, p.last_name
FROM edge_goalie_stats s
JOIN players p ON s.player_id = p.id
WHERE s.season = $1 AND s.game_type = $2
ORDER BY s.gaa_value ASC NULLS LAST;

-- name: GetEdgeGoalieLeaders :many
-- Goalies of one season and game type ordered by one Edge metric (sort_by,
-- whitelisted by the MCP get_edge_leaders tool; an unknown key matches no
-- row). Goalies without a value for the metric are left out. Games played
-- and clubs come from club_goalie_stats (summed over a traded goalie's
-- clubs); a goalie without club stats gets 0 games and no teams.
SELECT s.*, p.first_name, p.last_name, p.position,
       COALESCE(c.games_played, 0)::int AS games_played,
       COALESCE(c.teams, '')::text AS teams
FROM edge_goalie_stats s
JOIN players p ON p.id = s.player_id
LEFT JOIN (
  SELECT cg.player_id, SUM(cg.games_played)::int AS games_played,
         string_agg(st.abbrev, '/' ORDER BY st.abbrev)::text AS teams
  FROM club_goalie_stats cg
  LEFT JOIN season_teams st ON st.season = cg.season AND st.team_id = cg.team_id
  WHERE cg.season = sqlc.arg(season) AND cg.game_type = sqlc.arg(game_type)
  GROUP BY cg.player_id
) c ON c.player_id = s.player_id
CROSS JOIN LATERAL (
  SELECT CASE sqlc.arg(sort_by)::text
    WHEN 'gaa' THEN s.gaa_value::float8
    WHEN 'games_above_900' THEN s.games_above_900_value::float8
    WHEN 'goal_diff_per_60' THEN s.goal_diff_per_60_value::float8
    WHEN 'goal_support_avg' THEN s.goal_support_avg_value::float8
    WHEN 'point_pctg' THEN s.point_pctg_value::float8
  END AS sort_value
) m
WHERE s.season = sqlc.arg(season) AND s.game_type = sqlc.arg(game_type)
  AND m.sort_value IS NOT NULL
  AND COALESCE(c.games_played, 0) >= sqlc.arg(min_games)::int
ORDER BY
  CASE WHEN sqlc.arg(ascending)::bool THEN m.sort_value END ASC,
  m.sort_value DESC,
  p.last_name, p.first_name, s.player_id
LIMIT sqlc.arg(result_limit);

-- name: GetEdgeGoalieShotLocationSummary :many
SELECT * FROM edge_goalie_shot_location_summary
WHERE player_id = $1 AND season = $2 AND game_type = $3
ORDER BY location_code;

-- name: GetEdgeGoalieShotLocations :many
SELECT * FROM edge_goalie_shot_locations
WHERE player_id = $1 AND season = $2 AND game_type = $3
ORDER BY area;

-- name: CountEdgeGoalieStats :one
SELECT COUNT(*) FROM edge_goalie_stats;
