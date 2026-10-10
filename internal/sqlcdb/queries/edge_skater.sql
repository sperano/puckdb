-- =============================================================================
-- Edge Skater Queries
-- =============================================================================

-- name: UpsertEdgeSkaterStats :exec
INSERT INTO edge_skater_stats (
    player_id, season, game_type,
    top_speed_imperial, top_speed_metric, top_speed_percentile,
    top_speed_league_avg_imperial, top_speed_league_avg_metric,
    bursts_over_20, bursts_over_20_percentile, bursts_over_20_league_avg,
    total_distance_imperial, total_distance_metric, total_distance_percentile,
    max_game_distance_imperial, max_game_distance_metric, max_game_distance_percentile,
    top_shot_speed_imperial, top_shot_speed_metric, top_shot_speed_percentile,
    top_shot_speed_league_avg_imperial, top_shot_speed_league_avg_metric,
    oz_pctg, oz_percentile, oz_league_avg,
    nz_pctg, nz_percentile, nz_league_avg,
    dz_pctg, dz_percentile, dz_league_avg,
    oz_ev_pctg, oz_ev_percentile
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
        $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30,
        $31, $32, $33)
ON CONFLICT (player_id, season, game_type) DO UPDATE SET
    top_speed_imperial = EXCLUDED.top_speed_imperial,
    top_speed_metric = EXCLUDED.top_speed_metric,
    top_speed_percentile = EXCLUDED.top_speed_percentile,
    top_speed_league_avg_imperial = EXCLUDED.top_speed_league_avg_imperial,
    top_speed_league_avg_metric = EXCLUDED.top_speed_league_avg_metric,
    bursts_over_20 = EXCLUDED.bursts_over_20,
    bursts_over_20_percentile = EXCLUDED.bursts_over_20_percentile,
    bursts_over_20_league_avg = EXCLUDED.bursts_over_20_league_avg,
    total_distance_imperial = EXCLUDED.total_distance_imperial,
    total_distance_metric = EXCLUDED.total_distance_metric,
    total_distance_percentile = EXCLUDED.total_distance_percentile,
    max_game_distance_imperial = EXCLUDED.max_game_distance_imperial,
    max_game_distance_metric = EXCLUDED.max_game_distance_metric,
    max_game_distance_percentile = EXCLUDED.max_game_distance_percentile,
    top_shot_speed_imperial = EXCLUDED.top_shot_speed_imperial,
    top_shot_speed_metric = EXCLUDED.top_shot_speed_metric,
    top_shot_speed_percentile = EXCLUDED.top_shot_speed_percentile,
    top_shot_speed_league_avg_imperial = EXCLUDED.top_shot_speed_league_avg_imperial,
    top_shot_speed_league_avg_metric = EXCLUDED.top_shot_speed_league_avg_metric,
    oz_pctg = EXCLUDED.oz_pctg,
    oz_percentile = EXCLUDED.oz_percentile,
    oz_league_avg = EXCLUDED.oz_league_avg,
    nz_pctg = EXCLUDED.nz_pctg,
    nz_percentile = EXCLUDED.nz_percentile,
    nz_league_avg = EXCLUDED.nz_league_avg,
    dz_pctg = EXCLUDED.dz_pctg,
    dz_percentile = EXCLUDED.dz_percentile,
    dz_league_avg = EXCLUDED.dz_league_avg,
    oz_ev_pctg = EXCLUDED.oz_ev_pctg,
    oz_ev_percentile = EXCLUDED.oz_ev_percentile,
    updated_at = NOW()
WHERE (edge_skater_stats.top_speed_imperial, edge_skater_stats.top_speed_metric,
       edge_skater_stats.top_speed_percentile,
       edge_skater_stats.bursts_over_20, edge_skater_stats.bursts_over_20_percentile,
       edge_skater_stats.total_distance_imperial, edge_skater_stats.total_distance_metric,
       edge_skater_stats.top_shot_speed_imperial, edge_skater_stats.top_shot_speed_metric,
       edge_skater_stats.oz_pctg, edge_skater_stats.nz_pctg, edge_skater_stats.dz_pctg,
       edge_skater_stats.oz_ev_pctg)
      IS DISTINCT FROM
      (EXCLUDED.top_speed_imperial, EXCLUDED.top_speed_metric,
       EXCLUDED.top_speed_percentile,
       EXCLUDED.bursts_over_20, EXCLUDED.bursts_over_20_percentile,
       EXCLUDED.total_distance_imperial, EXCLUDED.total_distance_metric,
       EXCLUDED.top_shot_speed_imperial, EXCLUDED.top_shot_speed_metric,
       EXCLUDED.oz_pctg, EXCLUDED.nz_pctg, EXCLUDED.dz_pctg,
       EXCLUDED.oz_ev_pctg);

-- name: DeleteEdgeSkaterShotLocations :exec
DELETE FROM edge_skater_shot_locations
WHERE player_id = $1 AND season = $2 AND game_type = $3;

-- name: UpsertEdgeSkaterShotLocation :exec
INSERT INTO edge_skater_shot_locations (
    player_id, season, game_type, area,
    sog, goals, shooting_pctg, sog_percentile, goals_percentile, shooting_pctg_percentile
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (player_id, season, game_type, area) DO UPDATE SET
    sog = EXCLUDED.sog,
    goals = EXCLUDED.goals,
    shooting_pctg = EXCLUDED.shooting_pctg,
    sog_percentile = EXCLUDED.sog_percentile,
    goals_percentile = EXCLUDED.goals_percentile,
    shooting_pctg_percentile = EXCLUDED.shooting_pctg_percentile;

-- name: DeleteEdgeSkaterSogSummary :exec
DELETE FROM edge_skater_sog_summary
WHERE player_id = $1 AND season = $2 AND game_type = $3;

-- name: UpsertEdgeSkaterSogSummary :exec
INSERT INTO edge_skater_sog_summary (
    player_id, season, game_type, location_code,
    shots, shots_percentile, shots_league_avg,
    goals, goals_percentile, goals_league_avg,
    shooting_pctg, shooting_pctg_percentile, shooting_pctg_league_avg
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (player_id, season, game_type, location_code) DO UPDATE SET
    shots = EXCLUDED.shots,
    shots_percentile = EXCLUDED.shots_percentile,
    shots_league_avg = EXCLUDED.shots_league_avg,
    goals = EXCLUDED.goals,
    goals_percentile = EXCLUDED.goals_percentile,
    goals_league_avg = EXCLUDED.goals_league_avg,
    shooting_pctg = EXCLUDED.shooting_pctg,
    shooting_pctg_percentile = EXCLUDED.shooting_pctg_percentile,
    shooting_pctg_league_avg = EXCLUDED.shooting_pctg_league_avg;

-- name: GetEdgeSkaterStats :one
SELECT * FROM edge_skater_stats
WHERE player_id = $1 AND season = $2 AND game_type = $3;

-- name: GetEdgeSkaterStatsBySeason :many
SELECT s.*, p.first_name, p.last_name
FROM edge_skater_stats s
JOIN players p ON s.player_id = p.id
WHERE s.season = $1 AND s.game_type = $2
ORDER BY s.top_speed_percentile DESC NULLS LAST;

-- name: GetEdgeSkaterLeaders :many
-- Skaters of one season and game type ordered by one Edge metric (sort_by,
-- whitelisted by the MCP get_edge_leaders tool; an unknown key matches no
-- row). Skaters without a value for the metric are left out. Games played
-- and clubs come from club_skater_stats (summed over a traded player's
-- clubs); a skater without club stats gets 0 games and no teams.
SELECT s.*, p.first_name, p.last_name, p.position,
       COALESCE(c.games_played, 0)::int AS games_played,
       COALESCE(c.teams, '')::text AS teams
FROM edge_skater_stats s
JOIN players p ON p.id = s.player_id
LEFT JOIN (
  SELECT cs.player_id, SUM(cs.games_played)::int AS games_played,
         string_agg(st.abbrev, '/' ORDER BY st.abbrev)::text AS teams
  FROM club_skater_stats cs
  LEFT JOIN season_teams st ON st.season = cs.season AND st.team_id = cs.team_id
  WHERE cs.season = sqlc.arg(season) AND cs.game_type = sqlc.arg(game_type)
  GROUP BY cs.player_id
) c ON c.player_id = s.player_id
CROSS JOIN LATERAL (
  SELECT CASE sqlc.arg(sort_by)::text
    WHEN 'top_speed' THEN s.top_speed_imperial::float8
    WHEN 'bursts_over_20' THEN s.bursts_over_20::float8
    WHEN 'total_distance' THEN s.total_distance_imperial::float8
    WHEN 'max_game_distance' THEN s.max_game_distance_imperial::float8
    WHEN 'top_shot_speed' THEN s.top_shot_speed_imperial::float8
    WHEN 'oz_pctg' THEN s.oz_pctg::float8
    WHEN 'oz_ev_pctg' THEN s.oz_ev_pctg::float8
    WHEN 'nz_pctg' THEN s.nz_pctg::float8
    WHEN 'dz_pctg' THEN s.dz_pctg::float8
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

-- name: GetEdgeSkaterShotLocations :many
SELECT * FROM edge_skater_shot_locations
WHERE player_id = $1 AND season = $2 AND game_type = $3
ORDER BY area;

-- name: GetEdgeSkaterSogSummary :many
SELECT * FROM edge_skater_sog_summary
WHERE player_id = $1 AND season = $2 AND game_type = $3
ORDER BY location_code;

-- name: CountEdgeSkaterStats :one
SELECT COUNT(*) FROM edge_skater_stats;
