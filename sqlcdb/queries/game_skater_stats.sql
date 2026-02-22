-- =============================================================================
-- Game Skater Stats Queries
-- =============================================================================

-- name: GetGameSkaterStats :one
-- Get a single player's stats for a specific game
SELECT s.*,
    p.first_name, p.last_name, p.position as player_position,
    t.full_name as team_name, t.abbrev as team_abbrev
FROM game_skater_stats s
JOIN players p ON s.player_id = p.id
JOIN games g ON s.game_id = g.id
JOIN season_teams t ON t.team_id = s.team_id AND t.season_id = g.season
WHERE s.game_id = $1 AND s.player_id = $2;

-- name: GetGameSkaterStatsByGame :many
-- Get all skater stats for a game
SELECT s.*,
    p.first_name, p.last_name, p.position as player_position,
    t.full_name as team_name, t.abbrev as team_abbrev
FROM game_skater_stats s
JOIN players p ON s.player_id = p.id
JOIN games g ON s.game_id = g.id
JOIN season_teams t ON t.team_id = s.team_id AND t.season_id = g.season
WHERE s.game_id = $1
ORDER BY s.is_home DESC, s.position, p.last_name;

-- name: GetGameSkaterStatsByGameAndTeam :many
-- Get all skater stats for a specific team in a game
SELECT s.*,
    p.first_name, p.last_name, p.position as player_position,
    t.full_name as team_name, t.abbrev as team_abbrev
FROM game_skater_stats s
JOIN players p ON s.player_id = p.id
JOIN games g ON s.game_id = g.id
JOIN season_teams t ON t.team_id = s.team_id AND t.season_id = g.season
WHERE s.game_id = $1 AND s.team_id = $2
ORDER BY s.position, p.last_name;

-- name: GetSkaterStatsByPlayer :many
-- Get all game stats for a specific player (game log)
SELECT s.*,
    g.game_date, g.season, g.game_type,
    t.full_name as team_name, t.abbrev as team_abbrev
FROM game_skater_stats s
JOIN games g ON s.game_id = g.id
JOIN season_teams t ON t.team_id = s.team_id AND t.season_id = g.season
WHERE s.player_id = $1
ORDER BY g.game_date DESC;

-- name: GetSkaterStatsByPlayerAndSeason :many
-- Get all game stats for a player in a specific season
SELECT s.*,
    g.game_date, g.game_type,
    t.full_name as team_name, t.abbrev as team_abbrev
FROM game_skater_stats s
JOIN games g ON s.game_id = g.id
JOIN season_teams t ON t.team_id = s.team_id AND t.season_id = g.season
WHERE s.player_id = $1 AND g.season = $2
ORDER BY g.game_date;

-- name: GetSkaterStatsByPlayerAndDateRange :many
-- Get all game stats for a player within a date range
SELECT s.*,
    g.game_date, g.season, g.game_type,
    t.full_name as team_name, t.abbrev as team_abbrev
FROM game_skater_stats s
JOIN games g ON s.game_id = g.id
JOIN season_teams t ON t.team_id = s.team_id AND t.season_id = g.season
WHERE s.player_id = $1 AND g.game_date >= $2 AND g.game_date <= $3
ORDER BY g.game_date;

-- name: GetSkaterSeasonTotals :one
-- Aggregate season stats for a skater
SELECT
    s.player_id,
    COUNT(*)::int as games_played,
    SUM(s.goals)::int as total_goals,
    SUM(s.assists)::int as total_assists,
    SUM(s.points)::int as total_points,
    SUM(s.plus_minus)::int as total_plus_minus,
    SUM(s.shots_on_goal)::int as total_shots,
    SUM(s.hits)::int as total_hits,
    SUM(s.blocked_shots)::int as total_blocked_shots,
    SUM(s.penalty_minutes)::int as total_pim,
    SUM(s.toi_seconds)::int as total_toi_seconds,
    SUM(s.giveaways)::int as total_giveaways,
    SUM(s.takeaways)::int as total_takeaways,
    SUM(s.power_play_goals)::int as total_pp_goals
FROM game_skater_stats s
JOIN games g ON s.game_id = g.id
WHERE s.player_id = $1 AND g.season = $2
GROUP BY s.player_id;

-- name: GetTeamSkaterSeasonTotals :many
-- Aggregate season stats for all skaters on a team
SELECT
    s.player_id,
    p.first_name, p.last_name, p.position,
    COUNT(*)::int as games_played,
    SUM(s.goals)::int as total_goals,
    SUM(s.assists)::int as total_assists,
    SUM(s.points)::int as total_points,
    SUM(s.plus_minus)::int as total_plus_minus,
    SUM(s.shots_on_goal)::int as total_shots,
    SUM(s.penalty_minutes)::int as total_pim
FROM game_skater_stats s
JOIN games g ON s.game_id = g.id
JOIN players p ON s.player_id = p.id
WHERE s.team_id = $1 AND g.season = $2
GROUP BY s.player_id, p.first_name, p.last_name, p.position
ORDER BY total_points DESC, total_goals DESC;

-- name: UpsertGameSkaterStats :exec
INSERT INTO game_skater_stats (
    game_id, player_id, team_id, is_home, sweater_number, position,
    goals, assists, points, plus_minus, shots_on_goal,
    toi_seconds, shifts, faceoff_winning_pctg,
    hits, blocked_shots, penalty_minutes,
    giveaways, takeaways, power_play_goals,
    updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11,
    $12, $13, $14,
    $15, $16, $17,
    $18, $19, $20,
    NOW()
)
ON CONFLICT (game_id, player_id) DO UPDATE SET
    team_id = EXCLUDED.team_id,
    is_home = EXCLUDED.is_home,
    sweater_number = EXCLUDED.sweater_number,
    position = EXCLUDED.position,
    goals = EXCLUDED.goals,
    assists = EXCLUDED.assists,
    points = EXCLUDED.points,
    plus_minus = EXCLUDED.plus_minus,
    shots_on_goal = EXCLUDED.shots_on_goal,
    toi_seconds = EXCLUDED.toi_seconds,
    shifts = EXCLUDED.shifts,
    faceoff_winning_pctg = EXCLUDED.faceoff_winning_pctg,
    hits = EXCLUDED.hits,
    blocked_shots = EXCLUDED.blocked_shots,
    penalty_minutes = EXCLUDED.penalty_minutes,
    giveaways = EXCLUDED.giveaways,
    takeaways = EXCLUDED.takeaways,
    power_play_goals = EXCLUDED.power_play_goals,
    updated_at = NOW();

-- name: UpsertGameSkaterStatsBatch :batchexec
INSERT INTO game_skater_stats (
    game_id, player_id, team_id, is_home, sweater_number, position,
    goals, assists, points, plus_minus, shots_on_goal,
    toi_seconds, shifts, faceoff_winning_pctg,
    hits, blocked_shots, penalty_minutes,
    giveaways, takeaways, power_play_goals,
    updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11,
    $12, $13, $14,
    $15, $16, $17,
    $18, $19, $20,
    NOW()
)
ON CONFLICT (game_id, player_id) DO UPDATE SET
    team_id = EXCLUDED.team_id,
    is_home = EXCLUDED.is_home,
    sweater_number = EXCLUDED.sweater_number,
    position = EXCLUDED.position,
    goals = EXCLUDED.goals,
    assists = EXCLUDED.assists,
    points = EXCLUDED.points,
    plus_minus = EXCLUDED.plus_minus,
    shots_on_goal = EXCLUDED.shots_on_goal,
    toi_seconds = EXCLUDED.toi_seconds,
    shifts = EXCLUDED.shifts,
    faceoff_winning_pctg = EXCLUDED.faceoff_winning_pctg,
    hits = EXCLUDED.hits,
    blocked_shots = EXCLUDED.blocked_shots,
    penalty_minutes = EXCLUDED.penalty_minutes,
    giveaways = EXCLUDED.giveaways,
    takeaways = EXCLUDED.takeaways,
    power_play_goals = EXCLUDED.power_play_goals,
    updated_at = NOW();

-- name: DeleteGameSkaterStats :exec
DELETE FROM game_skater_stats WHERE game_id = $1 AND player_id = $2;

-- name: DeleteGameSkaterStatsByGame :exec
DELETE FROM game_skater_stats WHERE game_id = $1;

-- name: UpdateSkaterGameLogStats :exec
-- Update player game log specific stats (PPP, GWG, OT goals) that aren't in boxscores
UPDATE game_skater_stats
SET power_play_points = $3,
    game_winning_goals = $4,
    ot_goals = $5,
    updated_at = NOW()
WHERE game_id = $1 AND player_id = $2;
