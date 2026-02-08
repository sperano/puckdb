-- =============================================================================
-- NHL Game Goalie Stats Queries
-- =============================================================================

-- name: GetGameGoalieStats :one
-- Get a single goalie's stats for a specific game
SELECT s.*,
    p.first_name, p.last_name,
    t.full_name as team_name, t.abbrev as team_abbrev
FROM nhl_game_goalie_stats s
JOIN players p ON s.player_id = p.id
JOIN nhl_games g ON s.game_id = g.id
JOIN nhl_season_teams t ON t.team_id = s.team_id AND t.season_id = g.season
WHERE s.game_id = $1 AND s.player_id = $2;

-- name: GetGameGoalieStatsByGame :many
-- Get all goalie stats for a game
SELECT s.*,
    p.first_name, p.last_name,
    t.full_name as team_name, t.abbrev as team_abbrev
FROM nhl_game_goalie_stats s
JOIN players p ON s.player_id = p.id
JOIN nhl_games g ON s.game_id = g.id
JOIN nhl_season_teams t ON t.team_id = s.team_id AND t.season_id = g.season
WHERE s.game_id = $1
ORDER BY s.is_home DESC, s.starter DESC NULLS LAST;

-- name: GetGameGoalieStatsByGameAndTeam :many
-- Get all goalie stats for a specific team in a game
SELECT s.*,
    p.first_name, p.last_name,
    t.full_name as team_name, t.abbrev as team_abbrev
FROM nhl_game_goalie_stats s
JOIN players p ON s.player_id = p.id
JOIN nhl_games g ON s.game_id = g.id
JOIN nhl_season_teams t ON t.team_id = s.team_id AND t.season_id = g.season
WHERE s.game_id = $1 AND s.team_id = $2
ORDER BY s.starter DESC NULLS LAST;

-- name: GetGoalieStatsByPlayer :many
-- Get all game stats for a specific goalie (game log)
SELECT s.*,
    g.game_date, g.season, g.game_type,
    t.full_name as team_name, t.abbrev as team_abbrev
FROM nhl_game_goalie_stats s
JOIN nhl_games g ON s.game_id = g.id
JOIN nhl_season_teams t ON t.team_id = s.team_id AND t.season_id = g.season
WHERE s.player_id = $1
ORDER BY g.game_date DESC;

-- name: GetGoalieStatsByPlayerAndSeason :many
-- Get all game stats for a goalie in a specific season
SELECT s.*,
    g.game_date, g.game_type,
    t.full_name as team_name, t.abbrev as team_abbrev
FROM nhl_game_goalie_stats s
JOIN nhl_games g ON s.game_id = g.id
JOIN nhl_season_teams t ON t.team_id = s.team_id AND t.season_id = g.season
WHERE s.player_id = $1 AND g.season = $2
ORDER BY g.game_date;

-- name: GetGoalieStatsByPlayerAndDateRange :many
-- Get all game stats for a goalie within a date range
SELECT s.*,
    g.game_date, g.season, g.game_type,
    t.full_name as team_name, t.abbrev as team_abbrev
FROM nhl_game_goalie_stats s
JOIN nhl_games g ON s.game_id = g.id
JOIN nhl_season_teams t ON t.team_id = s.team_id AND t.season_id = g.season
WHERE s.player_id = $1 AND g.game_date >= $2 AND g.game_date <= $3
ORDER BY g.game_date;

-- name: GetGoalieSeasonTotals :one
-- Aggregate season stats for a goalie
SELECT
    s.player_id,
    COUNT(*)::int as games_played,
    COUNT(*) FILTER (WHERE s.starter = TRUE)::int as starts,
    COUNT(*) FILTER (WHERE s.decision = 'W')::int as wins,
    COUNT(*) FILTER (WHERE s.decision = 'L')::int as losses,
    COUNT(*) FILTER (WHERE s.decision = 'OTL')::int as otl,
    SUM(s.shots_against)::int as total_shots_against,
    SUM(s.saves)::int as total_saves,
    SUM(s.goals_against)::int as total_goals_against,
    SUM(s.toi_seconds)::int as total_toi_seconds,
    CASE
        WHEN SUM(s.shots_against) > 0
        THEN SUM(s.saves)::float / SUM(s.shots_against)::float
        ELSE NULL
    END as season_save_pctg,
    CASE
        WHEN SUM(s.toi_seconds) > 0
        THEN (SUM(s.goals_against)::float / SUM(s.toi_seconds)::float) * 3600
        ELSE NULL
    END as season_gaa
FROM nhl_game_goalie_stats s
JOIN nhl_games g ON s.game_id = g.id
WHERE s.player_id = $1 AND g.season = $2
GROUP BY s.player_id;

-- name: GetTeamGoalieSeasonTotals :many
-- Aggregate season stats for all goalies on a team
SELECT
    s.player_id,
    p.first_name, p.last_name,
    COUNT(*)::int as games_played,
    COUNT(*) FILTER (WHERE s.starter = TRUE)::int as starts,
    COUNT(*) FILTER (WHERE s.decision = 'W')::int as wins,
    COUNT(*) FILTER (WHERE s.decision = 'L')::int as losses,
    COUNT(*) FILTER (WHERE s.decision = 'OTL')::int as otl,
    SUM(s.shots_against)::int as total_shots_against,
    SUM(s.saves)::int as total_saves,
    SUM(s.goals_against)::int as total_goals_against,
    SUM(s.toi_seconds)::int as total_toi_seconds,
    CASE
        WHEN SUM(s.shots_against) > 0
        THEN SUM(s.saves)::float / SUM(s.shots_against)::float
        ELSE NULL
    END as season_save_pctg,
    CASE
        WHEN SUM(s.toi_seconds) > 0
        THEN (SUM(s.goals_against)::float / SUM(s.toi_seconds)::float) * 3600
        ELSE NULL
    END as season_gaa
FROM nhl_game_goalie_stats s
JOIN nhl_games g ON s.game_id = g.id
JOIN players p ON s.player_id = p.id
WHERE s.team_id = $1 AND g.season = $2
GROUP BY s.player_id, p.first_name, p.last_name
ORDER BY wins DESC, games_played DESC;

-- name: UpsertGameGoalieStats :exec
INSERT INTO nhl_game_goalie_stats (
    game_id, player_id, team_id, is_home, sweater_number,
    decision, starter,
    shots_against, saves, save_pctg,
    goals_against, even_strength_goals_against, power_play_goals_against, shorthanded_goals_against,
    toi_seconds, penalty_minutes,
    updated_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7,
    $8, $9, $10,
    $11, $12, $13, $14,
    $15, $16,
    NOW()
)
ON CONFLICT (game_id, player_id) DO UPDATE SET
    team_id = EXCLUDED.team_id,
    is_home = EXCLUDED.is_home,
    sweater_number = EXCLUDED.sweater_number,
    decision = EXCLUDED.decision,
    starter = EXCLUDED.starter,
    shots_against = EXCLUDED.shots_against,
    saves = EXCLUDED.saves,
    save_pctg = EXCLUDED.save_pctg,
    goals_against = EXCLUDED.goals_against,
    even_strength_goals_against = EXCLUDED.even_strength_goals_against,
    power_play_goals_against = EXCLUDED.power_play_goals_against,
    shorthanded_goals_against = EXCLUDED.shorthanded_goals_against,
    toi_seconds = EXCLUDED.toi_seconds,
    penalty_minutes = EXCLUDED.penalty_minutes,
    updated_at = NOW();

-- name: DeleteGameGoalieStats :exec
DELETE FROM nhl_game_goalie_stats WHERE game_id = $1 AND player_id = $2;

-- name: DeleteGameGoalieStatsByGame :exec
DELETE FROM nhl_game_goalie_stats WHERE game_id = $1;
