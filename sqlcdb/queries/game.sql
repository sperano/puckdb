-- =============================================================================
-- NHL Games Queries
-- =============================================================================

-- name: GetNHLGame :one
-- Get a single game by ID with team details
SELECT g.*,
    ht.full_name as home_team_name, ht.abbrev as home_team_abbrev,
    at.full_name as away_team_name, at.abbrev as away_team_abbrev
FROM nhl_games g
JOIN nhl_season_teams ht ON ht.team_id = g.home_team_id AND ht.season_id = g.season
JOIN nhl_season_teams at ON at.team_id = g.away_team_id AND at.season_id = g.season
WHERE g.id = $1;

-- name: GetNHLGamesByDate :many
-- Get all games on a specific date
SELECT g.*,
    ht.full_name as home_team_name, ht.abbrev as home_team_abbrev,
    at.full_name as away_team_name, at.abbrev as away_team_abbrev
FROM nhl_games g
JOIN nhl_season_teams ht ON ht.team_id = g.home_team_id AND ht.season_id = g.season
JOIN nhl_season_teams at ON at.team_id = g.away_team_id AND at.season_id = g.season
WHERE g.game_date = $1
ORDER BY g.start_time_utc;

-- name: GetNHLGamesByDateRange :many
-- Get all games within a date range
SELECT g.*,
    ht.full_name as home_team_name, ht.abbrev as home_team_abbrev,
    at.full_name as away_team_name, at.abbrev as away_team_abbrev
FROM nhl_games g
JOIN nhl_season_teams ht ON ht.team_id = g.home_team_id AND ht.season_id = g.season
JOIN nhl_season_teams at ON at.team_id = g.away_team_id AND at.season_id = g.season
WHERE g.game_date >= $1 AND g.game_date <= $2
ORDER BY g.game_date, g.start_time_utc;

-- name: GetNHLGamesBySeason :many
-- Get all games for a season
SELECT g.*,
    ht.full_name as home_team_name, ht.abbrev as home_team_abbrev,
    at.full_name as away_team_name, at.abbrev as away_team_abbrev
FROM nhl_games g
JOIN nhl_season_teams ht ON ht.team_id = g.home_team_id AND ht.season_id = g.season
JOIN nhl_season_teams at ON at.team_id = g.away_team_id AND at.season_id = g.season
WHERE g.season = $1
ORDER BY g.game_date, g.start_time_utc;

-- name: GetNHLGamesByTeam :many
-- Get all games for a team (home or away)
SELECT g.*,
    ht.full_name as home_team_name, ht.abbrev as home_team_abbrev,
    at.full_name as away_team_name, at.abbrev as away_team_abbrev
FROM nhl_games g
JOIN nhl_season_teams ht ON ht.team_id = g.home_team_id AND ht.season_id = g.season
JOIN nhl_season_teams at ON at.team_id = g.away_team_id AND at.season_id = g.season
WHERE g.home_team_id = $1 OR g.away_team_id = $1
ORDER BY g.game_date, g.start_time_utc;

-- name: GetNHLGamesByTeamAndSeason :many
-- Get all games for a team in a specific season
SELECT g.*,
    ht.full_name as home_team_name, ht.abbrev as home_team_abbrev,
    at.full_name as away_team_name, at.abbrev as away_team_abbrev
FROM nhl_games g
JOIN nhl_season_teams ht ON ht.team_id = g.home_team_id AND ht.season_id = g.season
JOIN nhl_season_teams at ON at.team_id = g.away_team_id AND at.season_id = g.season
WHERE (g.home_team_id = $1 OR g.away_team_id = $1)
  AND g.season = $2
ORDER BY g.game_date, g.start_time_utc;

-- name: ListNHLGames :many
-- Flexible game listing with optional filters
SELECT g.*,
    ht.full_name as home_team_name, ht.abbrev as home_team_abbrev,
    at.full_name as away_team_name, at.abbrev as away_team_abbrev
FROM nhl_games g
JOIN nhl_season_teams ht ON ht.team_id = g.home_team_id AND ht.season_id = g.season
JOIN nhl_season_teams at ON at.team_id = g.away_team_id AND at.season_id = g.season
WHERE
    (sqlc.narg('season')::int IS NULL OR g.season = sqlc.narg('season'))
    AND (sqlc.narg('game_type')::smallint IS NULL OR g.game_type = sqlc.narg('game_type'))
    AND (sqlc.narg('game_state')::text IS NULL OR g.game_state = sqlc.narg('game_state'))
    AND (sqlc.narg('team_id')::bigint IS NULL OR g.home_team_id = sqlc.narg('team_id') OR g.away_team_id = sqlc.narg('team_id'))
    AND (sqlc.narg('start_date')::date IS NULL OR g.game_date >= sqlc.narg('start_date'))
    AND (sqlc.narg('end_date')::date IS NULL OR g.game_date <= sqlc.narg('end_date'))
ORDER BY g.game_date, g.start_time_utc
LIMIT COALESCE(sqlc.narg('limit')::int, 100);

-- name: CountNHLGames :one
SELECT COUNT(*) FROM nhl_games;

-- name: CountNHLGamesBySeason :one
SELECT COUNT(*) FROM nhl_games WHERE season = $1;

-- name: CountNHLGamesByTeamAndSeason :one
SELECT COUNT(*) FROM nhl_games
WHERE (home_team_id = $1 OR away_team_id = $1) AND season = $2;

-- name: UpsertNHLGame :exec
INSERT INTO nhl_games (
    id, season, game_type, game_date,
    venue, venue_location, start_time_utc, eastern_utc_offset, venue_utc_offset,
    game_state, game_schedule_state,
    period_number, period_type, max_regulation_periods,
    clock_time_remaining, clock_seconds_remaining, clock_running, clock_in_intermission,
    home_team_id, home_team_score, home_team_sog,
    away_team_id, away_team_score, away_team_sog,
    limited_scoring, updated_at
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7, $8, $9,
    $10, $11,
    $12, $13, $14,
    $15, $16, $17, $18,
    $19, $20, $21,
    $22, $23, $24,
    $25, NOW()
)
ON CONFLICT (id) DO UPDATE SET
    season = EXCLUDED.season,
    game_type = EXCLUDED.game_type,
    game_date = EXCLUDED.game_date,
    venue = EXCLUDED.venue,
    venue_location = EXCLUDED.venue_location,
    start_time_utc = EXCLUDED.start_time_utc,
    eastern_utc_offset = EXCLUDED.eastern_utc_offset,
    venue_utc_offset = EXCLUDED.venue_utc_offset,
    game_state = EXCLUDED.game_state,
    game_schedule_state = EXCLUDED.game_schedule_state,
    period_number = EXCLUDED.period_number,
    period_type = EXCLUDED.period_type,
    max_regulation_periods = EXCLUDED.max_regulation_periods,
    clock_time_remaining = EXCLUDED.clock_time_remaining,
    clock_seconds_remaining = EXCLUDED.clock_seconds_remaining,
    clock_running = EXCLUDED.clock_running,
    clock_in_intermission = EXCLUDED.clock_in_intermission,
    home_team_id = EXCLUDED.home_team_id,
    home_team_score = EXCLUDED.home_team_score,
    home_team_sog = EXCLUDED.home_team_sog,
    away_team_id = EXCLUDED.away_team_id,
    away_team_score = EXCLUDED.away_team_score,
    away_team_sog = EXCLUDED.away_team_sog,
    limited_scoring = EXCLUDED.limited_scoring,
    updated_at = NOW();

-- name: DeleteNHLGame :exec
DELETE FROM nhl_games WHERE id = $1;

-- name: DeleteNHLGamesBySeason :exec
DELETE FROM nhl_games WHERE season = $1;
