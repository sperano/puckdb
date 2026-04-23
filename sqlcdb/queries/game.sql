-- =============================================================================
-- Games Queries
-- =============================================================================

-- name: GetGame :one
-- Get a single game by ID with team details
SELECT g.*,
    ht.full_name as home_team_name, ht.abbrev as home_team_abbrev,
    at.full_name as away_team_name, at.abbrev as away_team_abbrev
FROM games g
JOIN season_teams ht ON ht.team_id = g.home_team_id AND ht.season = g.season
JOIN season_teams at ON at.team_id = g.away_team_id AND at.season = g.season
WHERE g.id = $1;

-- name: GetGamesByDate :many
-- Get all games on a specific date
SELECT g.*,
    ht.full_name as home_team_name, ht.abbrev as home_team_abbrev,
    at.full_name as away_team_name, at.abbrev as away_team_abbrev
FROM games g
JOIN season_teams ht ON ht.team_id = g.home_team_id AND ht.season = g.season
JOIN season_teams at ON at.team_id = g.away_team_id AND at.season = g.season
WHERE g.game_date = $1
ORDER BY g.start_time_utc;

-- name: GetGamesByDateRange :many
-- Get all games within a date range
SELECT g.*,
    ht.full_name as home_team_name, ht.abbrev as home_team_abbrev,
    at.full_name as away_team_name, at.abbrev as away_team_abbrev
FROM games g
JOIN season_teams ht ON ht.team_id = g.home_team_id AND ht.season = g.season
JOIN season_teams at ON at.team_id = g.away_team_id AND at.season = g.season
WHERE g.game_date >= $1 AND g.game_date <= $2
ORDER BY g.game_date, g.start_time_utc;

-- name: GetGamesBySeason :many
-- Get all games for a season
SELECT g.*,
    ht.full_name as home_team_name, ht.abbrev as home_team_abbrev,
    at.full_name as away_team_name, at.abbrev as away_team_abbrev
FROM games g
JOIN season_teams ht ON ht.team_id = g.home_team_id AND ht.season = g.season
JOIN season_teams at ON at.team_id = g.away_team_id AND at.season = g.season
WHERE g.season = $1
ORDER BY g.game_date, g.start_time_utc;

-- name: GetGamesByTeam :many
-- Get all games for a team (home or away)
SELECT g.*,
    ht.full_name as home_team_name, ht.abbrev as home_team_abbrev,
    at.full_name as away_team_name, at.abbrev as away_team_abbrev
FROM games g
JOIN season_teams ht ON ht.team_id = g.home_team_id AND ht.season = g.season
JOIN season_teams at ON at.team_id = g.away_team_id AND at.season = g.season
WHERE g.home_team_id = $1 OR g.away_team_id = $1
ORDER BY g.game_date, g.start_time_utc;

-- name: GetGamesByTeamAndSeason :many
-- Get all games for a team in a specific season
SELECT g.*,
    ht.full_name as home_team_name, ht.abbrev as home_team_abbrev,
    at.full_name as away_team_name, at.abbrev as away_team_abbrev
FROM games g
JOIN season_teams ht ON ht.team_id = g.home_team_id AND ht.season = g.season
JOIN season_teams at ON at.team_id = g.away_team_id AND at.season = g.season
WHERE (g.home_team_id = $1 OR g.away_team_id = $1)
  AND g.season = $2
ORDER BY g.game_date, g.start_time_utc;

-- name: ListGames :many
-- Flexible game listing with optional filters
SELECT g.*,
    ht.full_name as home_team_name, ht.abbrev as home_team_abbrev,
    at.full_name as away_team_name, at.abbrev as away_team_abbrev
FROM games g
JOIN season_teams ht ON ht.team_id = g.home_team_id AND ht.season = g.season
JOIN season_teams at ON at.team_id = g.away_team_id AND at.season = g.season
WHERE
    (sqlc.narg('season')::int IS NULL OR g.season = sqlc.narg('season'))
    AND (sqlc.narg('game_type')::game_type IS NULL OR g.game_type = sqlc.narg('game_type'))
    AND (sqlc.narg('game_state')::game_state IS NULL OR g.game_state = sqlc.narg('game_state'))
    AND (sqlc.narg('team_id')::bigint IS NULL OR g.home_team_id = sqlc.narg('team_id') OR g.away_team_id = sqlc.narg('team_id'))
    AND (sqlc.narg('start_date')::date IS NULL OR g.game_date >= sqlc.narg('start_date'))
    AND (sqlc.narg('end_date')::date IS NULL OR g.game_date <= sqlc.narg('end_date'))
ORDER BY g.game_date, g.start_time_utc
LIMIT COALESCE(sqlc.narg('limit')::int, 100);

-- name: CountGames :one
SELECT COUNT(*) FROM games;

-- name: CountGamesBySeason :one
SELECT COUNT(*) FROM games WHERE season = $1;

-- name: CountGamesByTeamAndSeason :one
SELECT COUNT(*) FROM games
WHERE (home_team_id = $1 OR away_team_id = $1) AND season = $2;

-- name: GetGameTeamIDs :one
SELECT home_team_id, away_team_id FROM games WHERE id = $1;

-- name: UpsertGame :exec
INSERT INTO games (
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
    updated_at = NOW()
WHERE (games.season, games.game_type, games.game_date,
       games.venue, games.venue_location,
       games.start_time_utc, games.eastern_utc_offset, games.venue_utc_offset,
       games.game_state, games.game_schedule_state,
       games.period_number, games.period_type, games.max_regulation_periods,
       games.clock_time_remaining, games.clock_seconds_remaining,
       games.clock_running, games.clock_in_intermission,
       games.home_team_id, games.home_team_score, games.home_team_sog,
       games.away_team_id, games.away_team_score, games.away_team_sog,
       games.limited_scoring)
      IS DISTINCT FROM
      (EXCLUDED.season, EXCLUDED.game_type, EXCLUDED.game_date,
       EXCLUDED.venue, EXCLUDED.venue_location,
       EXCLUDED.start_time_utc, EXCLUDED.eastern_utc_offset, EXCLUDED.venue_utc_offset,
       EXCLUDED.game_state, EXCLUDED.game_schedule_state,
       EXCLUDED.period_number, EXCLUDED.period_type, EXCLUDED.max_regulation_periods,
       EXCLUDED.clock_time_remaining, EXCLUDED.clock_seconds_remaining,
       EXCLUDED.clock_running, EXCLUDED.clock_in_intermission,
       EXCLUDED.home_team_id, EXCLUDED.home_team_score, EXCLUDED.home_team_sog,
       EXCLUDED.away_team_id, EXCLUDED.away_team_score, EXCLUDED.away_team_sog,
       EXCLUDED.limited_scoring);

-- name: DeleteGame :exec
DELETE FROM games WHERE id = $1;

-- name: DeleteGamesBySeason :exec
DELETE FROM games WHERE season = $1;

-- =============================================================================
-- Playoff Queries
-- =============================================================================

-- name: GetPlayoffGames :many
-- Get playoff games for a season, optionally filtered by round (1-4)
-- Round is extracted from game ID: position 8 indicates round
-- Game ID format: YYYYTTRRSS where TT=03 for playoffs, RR=round (01-04), SS=series+game
-- 1=First Round, 2=Second Round, 3=Conference Finals, 4=Stanley Cup Finals
SELECT g.*,
    ht.full_name as home_team_name, ht.abbrev as home_team_abbrev,
    at.full_name as away_team_name, at.abbrev as away_team_abbrev,
    CAST(SUBSTRING(g.id::text, 8, 1) AS int) as playoff_round
FROM games g
JOIN season_teams ht ON ht.team_id = g.home_team_id AND ht.season = g.season
JOIN season_teams at ON at.team_id = g.away_team_id AND at.season = g.season
WHERE g.game_type = 'playoffs'
  AND g.season = $1
  AND (sqlc.narg('round')::int IS NULL OR CAST(SUBSTRING(g.id::text, 8, 1) AS int) = sqlc.narg('round'))
  AND (sqlc.narg('team_id')::bigint IS NULL OR g.home_team_id = sqlc.narg('team_id') OR g.away_team_id = sqlc.narg('team_id'))
ORDER BY g.game_date, g.start_time_utc;

-- name: GetPlayoffSeries :many
-- Get playoff series summaries for a season with win counts
-- Groups games by round and matchup, calculates series standings
WITH series_games AS (
    SELECT
        g.season,
        CAST(SUBSTRING(g.id::text, 8, 1) AS int) as round,
        CAST(SUBSTRING(g.id::text, 8, 2) AS text) as series_id,
        g.home_team_id,
        g.away_team_id,
        g.home_team_score,
        g.away_team_score,
        g.game_date,
        g.game_state,
        ht.full_name as home_team_name,
        ht.abbrev as home_team_abbrev,
        at.full_name as away_team_name,
        at.abbrev as away_team_abbrev,
        CASE WHEN g.home_team_score > g.away_team_score THEN g.home_team_id ELSE g.away_team_id END as winner_id
    FROM games g
    JOIN season_teams ht ON ht.team_id = g.home_team_id AND ht.season = g.season
    JOIN season_teams at ON at.team_id = g.away_team_id AND at.season = g.season
    WHERE g.game_type = 'playoffs'
      AND g.season = $1
      AND g.game_state IN ('OFF', 'FINAL')
),
series_summary AS (
    SELECT
        season,
        round,
        series_id,
        MIN(home_team_id)::bigint as team1_id,
        MAX(away_team_id)::bigint as team2_id,
        MIN(home_team_name)::text as team1_name,
        MAX(away_team_name)::text as team2_name,
        MIN(home_team_abbrev)::text as team1_abbrev,
        MAX(away_team_abbrev)::text as team2_abbrev,
        COUNT(*)::int as games_played,
        MAX(game_date)::date as last_game_date
    FROM series_games
    GROUP BY season, round, series_id
),
series_with_wins AS (
    SELECT
        ss.*,
        (SELECT COUNT(*)::int FROM series_games sg
         WHERE sg.season = ss.season AND sg.series_id = ss.series_id
         AND sg.winner_id = ss.team1_id) as team1_wins,
        (SELECT COUNT(*)::int FROM series_games sg
         WHERE sg.season = ss.season AND sg.series_id = ss.series_id
         AND sg.winner_id = ss.team2_id) as team2_wins
    FROM series_summary ss
)
SELECT
    season,
    round,
    series_id,
    team1_id,
    team1_name,
    team1_abbrev,
    team1_wins,
    team2_id,
    team2_name,
    team2_abbrev,
    team2_wins,
    games_played,
    last_game_date,
    CASE
        WHEN team1_wins = 4 THEN team1_name
        WHEN team2_wins = 4 THEN team2_name
        ELSE ''
    END::text as series_winner
FROM series_with_wins
WHERE (sqlc.narg('round')::int IS NULL OR round = sqlc.narg('round'))
ORDER BY round, series_id;

-- name: GetStanleyCupFinals :many
-- Get Stanley Cup Finals games for a season (the final round, which varies by era)
-- Uses subquery to find the max round for the season dynamically
SELECT g.*,
    ht.full_name as home_team_name, ht.abbrev as home_team_abbrev,
    at.full_name as away_team_name, at.abbrev as away_team_abbrev
FROM games g
JOIN season_teams ht ON ht.team_id = g.home_team_id AND ht.season = g.season
JOIN season_teams at ON at.team_id = g.away_team_id AND at.season = g.season
WHERE g.game_type = 'playoffs'
  AND g.season = $1
  AND CAST(SUBSTRING(g.id::text, 8, 1) AS int) = (
      SELECT MAX(CAST(SUBSTRING(id::text, 8, 1) AS int))
      FROM games
      WHERE game_type = 'playoffs' AND season = $1
  )
ORDER BY g.game_date, g.start_time_utc;

-- name: GetStanleyCupWinners :many
-- Get Stanley Cup champions for recent seasons
-- Finds the winner of the last Finals game for each season
-- Works for all eras (finals round varies: 1-4 depending on playoff format)
WITH max_rounds AS (
    SELECT season, MAX(CAST(SUBSTRING(id::text, 8, 1) AS int)) as final_round
    FROM games
    WHERE game_type = 'playoffs'
    GROUP BY season
),
finals_games AS (
    SELECT
        g.season,
        g.game_date,
        g.home_team_id,
        g.away_team_id,
        g.home_team_score,
        g.away_team_score,
        ht.full_name as home_team_name,
        ht.abbrev as home_team_abbrev,
        at.full_name as away_team_name,
        at.abbrev as away_team_abbrev,
        ROW_NUMBER() OVER (PARTITION BY g.season ORDER BY g.game_date DESC, g.id DESC) as rn
    FROM games g
    JOIN season_teams ht ON ht.team_id = g.home_team_id AND ht.season = g.season
    JOIN season_teams at ON at.team_id = g.away_team_id AND at.season = g.season
    JOIN max_rounds mr ON mr.season = g.season
    WHERE g.game_type = 'playoffs'
      AND CAST(SUBSTRING(g.id::text, 8, 1) AS int) = mr.final_round
      AND g.game_state IN ('OFF', 'FINAL')
)
SELECT
    season,
    game_date as clinching_date,
    (CASE WHEN home_team_score > away_team_score THEN home_team_id ELSE away_team_id END)::bigint as champion_id,
    (CASE WHEN home_team_score > away_team_score THEN home_team_name ELSE away_team_name END)::text as champion_name,
    (CASE WHEN home_team_score > away_team_score THEN home_team_abbrev ELSE away_team_abbrev END)::text as champion_abbrev,
    (CASE WHEN home_team_score > away_team_score THEN away_team_id ELSE home_team_id END)::bigint as runner_up_id,
    (CASE WHEN home_team_score > away_team_score THEN away_team_name ELSE home_team_name END)::text as runner_up_name,
    (CASE WHEN home_team_score > away_team_score THEN away_team_abbrev ELSE home_team_abbrev END)::text as runner_up_abbrev,
    home_team_score,
    away_team_score
FROM finals_games
WHERE rn = 1
ORDER BY season DESC
LIMIT COALESCE(sqlc.narg('limit')::int, 10);
