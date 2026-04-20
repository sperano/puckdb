-- =============================================================================
-- Edge Team Queries
-- =============================================================================

-- name: UpsertEdgeTeamStats :exec
INSERT INTO edge_team_stats (
    team_id, season, game_type,
    shot_attempts_over_90, shot_attempts_over_90_rank,
    top_shot_speed_imperial, top_shot_speed_metric, top_shot_speed_rank,
    bursts_over_22, bursts_over_22_rank,
    bursts_over_20, bursts_over_20_rank,
    speed_max_imperial, speed_max_metric, speed_max_rank,
    total_distance, total_distance_rank,
    oz_pctg, oz_rank, oz_league_avg,
    oz_ev_pctg, oz_ev_rank,
    nz_pctg, nz_rank, nz_league_avg,
    dz_pctg, dz_rank, dz_league_avg
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
        $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28)
ON CONFLICT (team_id, season, game_type) DO UPDATE SET
    shot_attempts_over_90 = EXCLUDED.shot_attempts_over_90,
    shot_attempts_over_90_rank = EXCLUDED.shot_attempts_over_90_rank,
    top_shot_speed_imperial = EXCLUDED.top_shot_speed_imperial,
    top_shot_speed_metric = EXCLUDED.top_shot_speed_metric,
    top_shot_speed_rank = EXCLUDED.top_shot_speed_rank,
    bursts_over_22 = EXCLUDED.bursts_over_22,
    bursts_over_22_rank = EXCLUDED.bursts_over_22_rank,
    bursts_over_20 = EXCLUDED.bursts_over_20,
    bursts_over_20_rank = EXCLUDED.bursts_over_20_rank,
    speed_max_imperial = EXCLUDED.speed_max_imperial,
    speed_max_metric = EXCLUDED.speed_max_metric,
    speed_max_rank = EXCLUDED.speed_max_rank,
    total_distance = EXCLUDED.total_distance,
    total_distance_rank = EXCLUDED.total_distance_rank,
    oz_pctg = EXCLUDED.oz_pctg,
    oz_rank = EXCLUDED.oz_rank,
    oz_league_avg = EXCLUDED.oz_league_avg,
    oz_ev_pctg = EXCLUDED.oz_ev_pctg,
    oz_ev_rank = EXCLUDED.oz_ev_rank,
    nz_pctg = EXCLUDED.nz_pctg,
    nz_rank = EXCLUDED.nz_rank,
    nz_league_avg = EXCLUDED.nz_league_avg,
    dz_pctg = EXCLUDED.dz_pctg,
    dz_rank = EXCLUDED.dz_rank,
    dz_league_avg = EXCLUDED.dz_league_avg,
    updated_at = NOW()
WHERE (edge_team_stats.shot_attempts_over_90, edge_team_stats.shot_attempts_over_90_rank,
       edge_team_stats.top_shot_speed_imperial, edge_team_stats.top_shot_speed_rank,
       edge_team_stats.bursts_over_22, edge_team_stats.bursts_over_22_rank,
       edge_team_stats.bursts_over_20, edge_team_stats.bursts_over_20_rank,
       edge_team_stats.speed_max_imperial, edge_team_stats.speed_max_rank,
       edge_team_stats.total_distance, edge_team_stats.total_distance_rank,
       edge_team_stats.oz_pctg, edge_team_stats.oz_rank,
       edge_team_stats.nz_pctg, edge_team_stats.nz_rank,
       edge_team_stats.dz_pctg, edge_team_stats.dz_rank)
      IS DISTINCT FROM
      (EXCLUDED.shot_attempts_over_90, EXCLUDED.shot_attempts_over_90_rank,
       EXCLUDED.top_shot_speed_imperial, EXCLUDED.top_shot_speed_rank,
       EXCLUDED.bursts_over_22, EXCLUDED.bursts_over_22_rank,
       EXCLUDED.bursts_over_20, EXCLUDED.bursts_over_20_rank,
       EXCLUDED.speed_max_imperial, EXCLUDED.speed_max_rank,
       EXCLUDED.total_distance, EXCLUDED.total_distance_rank,
       EXCLUDED.oz_pctg, EXCLUDED.oz_rank,
       EXCLUDED.nz_pctg, EXCLUDED.nz_rank,
       EXCLUDED.dz_pctg, EXCLUDED.dz_rank);

-- name: DeleteEdgeTeamSogSummary :exec
DELETE FROM edge_team_sog_summary
WHERE team_id = $1 AND season = $2 AND game_type = $3;

-- name: InsertEdgeTeamSogSummary :exec
INSERT INTO edge_team_sog_summary (
    team_id, season, game_type, location_code,
    shots, shots_rank, shots_league_avg,
    goals, goals_rank, goals_league_avg,
    shooting_pctg, shooting_pctg_rank, shooting_pctg_league_avg
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: DeleteEdgeTeamShotLocations :exec
DELETE FROM edge_team_shot_locations
WHERE team_id = $1 AND season = $2 AND game_type = $3;

-- name: InsertEdgeTeamShotLocation :exec
INSERT INTO edge_team_shot_locations (
    team_id, season, game_type, area,
    shots, shots_rank
)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: DeleteEdgeTeamZoneTimeByStrength :exec
DELETE FROM edge_team_zone_time_by_strength
WHERE team_id = $1 AND season = $2 AND game_type = $3;

-- name: InsertEdgeTeamZoneTimeByStrength :exec
INSERT INTO edge_team_zone_time_by_strength (
    team_id, season, game_type, strength_code,
    oz_pctg, oz_rank, nz_pctg, nz_rank, dz_pctg, dz_rank
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: DeleteEdgeTeamShotDifferential :exec
DELETE FROM edge_team_shot_differential
WHERE team_id = $1 AND season = $2 AND game_type = $3;

-- name: InsertEdgeTeamShotDifferential :exec
INSERT INTO edge_team_shot_differential (
    team_id, season, game_type, strength_code,
    for_per_game, for_per_game_rank,
    against_per_game, against_per_game_rank,
    differential_per_game, differential_per_game_rank
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: GetEdgeTeamStats :one
SELECT * FROM edge_team_stats
WHERE team_id = $1 AND season = $2 AND game_type = $3;

-- name: GetEdgeTeamStatsBySeason :many
SELECT s.*, st.abbrev, st.full_name
FROM edge_team_stats s
JOIN season_teams st ON s.season = st.season AND s.team_id = st.team_id
WHERE s.season = $1 AND s.game_type = $2
ORDER BY s.oz_pctg DESC NULLS LAST;

-- name: GetEdgeTeamSogSummary :many
SELECT * FROM edge_team_sog_summary
WHERE team_id = $1 AND season = $2 AND game_type = $3
ORDER BY location_code;

-- name: GetEdgeTeamShotLocations :many
SELECT * FROM edge_team_shot_locations
WHERE team_id = $1 AND season = $2 AND game_type = $3
ORDER BY area;

-- name: GetEdgeTeamZoneTimeByStrength :many
SELECT * FROM edge_team_zone_time_by_strength
WHERE team_id = $1 AND season = $2 AND game_type = $3
ORDER BY strength_code;

-- name: GetEdgeTeamShotDifferential :many
SELECT * FROM edge_team_shot_differential
WHERE team_id = $1 AND season = $2 AND game_type = $3
ORDER BY strength_code;

-- name: CountEdgeTeamStats :one
SELECT COUNT(*) FROM edge_team_stats;
