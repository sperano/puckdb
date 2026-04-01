-- =============================================================================
-- Club Stats Queries (team-level per-player season aggregates)
-- =============================================================================

-- name: UpsertClubSkaterStatsBatch :batchexec
INSERT INTO club_skater_stats (
    season, game_type, team_id, player_id,
    games_played, goals, assists, points, plus_minus, penalty_minutes,
    power_play_goals, shorthanded_goals, game_winning_goals, overtime_goals,
    shots, shooting_pctg, avg_toi_per_game, avg_shifts_per_game, faceoff_win_pctg
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
ON CONFLICT (season, game_type, team_id, player_id) DO UPDATE SET
    games_played = EXCLUDED.games_played,
    goals = EXCLUDED.goals,
    assists = EXCLUDED.assists,
    points = EXCLUDED.points,
    plus_minus = EXCLUDED.plus_minus,
    penalty_minutes = EXCLUDED.penalty_minutes,
    power_play_goals = EXCLUDED.power_play_goals,
    shorthanded_goals = EXCLUDED.shorthanded_goals,
    game_winning_goals = EXCLUDED.game_winning_goals,
    overtime_goals = EXCLUDED.overtime_goals,
    shots = EXCLUDED.shots,
    shooting_pctg = EXCLUDED.shooting_pctg,
    avg_toi_per_game = EXCLUDED.avg_toi_per_game,
    avg_shifts_per_game = EXCLUDED.avg_shifts_per_game,
    faceoff_win_pctg = EXCLUDED.faceoff_win_pctg,
    updated_at = NOW()
WHERE (club_skater_stats.games_played, club_skater_stats.goals,
       club_skater_stats.assists, club_skater_stats.points,
       club_skater_stats.plus_minus, club_skater_stats.penalty_minutes,
       club_skater_stats.power_play_goals, club_skater_stats.shorthanded_goals,
       club_skater_stats.game_winning_goals, club_skater_stats.overtime_goals,
       club_skater_stats.shots, club_skater_stats.shooting_pctg,
       club_skater_stats.avg_toi_per_game, club_skater_stats.avg_shifts_per_game,
       club_skater_stats.faceoff_win_pctg)
      IS DISTINCT FROM
      (EXCLUDED.games_played, EXCLUDED.goals,
       EXCLUDED.assists, EXCLUDED.points,
       EXCLUDED.plus_minus, EXCLUDED.penalty_minutes,
       EXCLUDED.power_play_goals, EXCLUDED.shorthanded_goals,
       EXCLUDED.game_winning_goals, EXCLUDED.overtime_goals,
       EXCLUDED.shots, EXCLUDED.shooting_pctg,
       EXCLUDED.avg_toi_per_game, EXCLUDED.avg_shifts_per_game,
       EXCLUDED.faceoff_win_pctg);

-- name: UpsertClubGoalieStatsBatch :batchexec
INSERT INTO club_goalie_stats (
    season, game_type, team_id, player_id,
    games_played, games_started, wins, losses, overtime_losses,
    goals_against_average, save_percentage, shots_against, saves, goals_against,
    shutouts, goals, assists, points, penalty_minutes, toi_seconds
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
ON CONFLICT (season, game_type, team_id, player_id) DO UPDATE SET
    games_played = EXCLUDED.games_played,
    games_started = EXCLUDED.games_started,
    wins = EXCLUDED.wins,
    losses = EXCLUDED.losses,
    overtime_losses = EXCLUDED.overtime_losses,
    goals_against_average = EXCLUDED.goals_against_average,
    save_percentage = EXCLUDED.save_percentage,
    shots_against = EXCLUDED.shots_against,
    saves = EXCLUDED.saves,
    goals_against = EXCLUDED.goals_against,
    shutouts = EXCLUDED.shutouts,
    goals = EXCLUDED.goals,
    assists = EXCLUDED.assists,
    points = EXCLUDED.points,
    penalty_minutes = EXCLUDED.penalty_minutes,
    toi_seconds = EXCLUDED.toi_seconds,
    updated_at = NOW()
WHERE (club_goalie_stats.games_played, club_goalie_stats.games_started,
       club_goalie_stats.wins, club_goalie_stats.losses,
       club_goalie_stats.overtime_losses, club_goalie_stats.goals_against_average,
       club_goalie_stats.save_percentage, club_goalie_stats.shots_against,
       club_goalie_stats.saves, club_goalie_stats.goals_against,
       club_goalie_stats.shutouts, club_goalie_stats.goals,
       club_goalie_stats.assists, club_goalie_stats.points,
       club_goalie_stats.penalty_minutes, club_goalie_stats.toi_seconds)
      IS DISTINCT FROM
      (EXCLUDED.games_played, EXCLUDED.games_started,
       EXCLUDED.wins, EXCLUDED.losses,
       EXCLUDED.overtime_losses, EXCLUDED.goals_against_average,
       EXCLUDED.save_percentage, EXCLUDED.shots_against,
       EXCLUDED.saves, EXCLUDED.goals_against,
       EXCLUDED.shutouts, EXCLUDED.goals,
       EXCLUDED.assists, EXCLUDED.points,
       EXCLUDED.penalty_minutes, EXCLUDED.toi_seconds);

-- name: GetClubSkaterStatsBySeason :many
SELECT s.*, p.first_name, p.last_name
FROM club_skater_stats s
JOIN players p ON s.player_id = p.id
WHERE s.season = $1 AND s.game_type = $2
ORDER BY s.points DESC, s.goals DESC;

-- name: GetClubSkaterStatsByTeam :many
SELECT s.*, p.first_name, p.last_name
FROM club_skater_stats s
JOIN players p ON s.player_id = p.id
WHERE s.season = $1 AND s.game_type = $2 AND s.team_id = $3
ORDER BY s.points DESC, s.goals DESC;

-- name: GetClubGoalieStatsBySeason :many
SELECT s.*, p.first_name, p.last_name
FROM club_goalie_stats s
JOIN players p ON s.player_id = p.id
WHERE s.season = $1 AND s.game_type = $2
ORDER BY s.wins DESC, s.games_played DESC;

-- name: GetClubGoalieStatsByTeam :many
SELECT s.*, p.first_name, p.last_name
FROM club_goalie_stats s
JOIN players p ON s.player_id = p.id
WHERE s.season = $1 AND s.game_type = $2 AND s.team_id = $3
ORDER BY s.wins DESC, s.games_played DESC;

-- name: CountClubSkaterStats :one
SELECT COUNT(*) FROM club_skater_stats;

-- name: CountClubGoalieStats :one
SELECT COUNT(*) FROM club_goalie_stats;
