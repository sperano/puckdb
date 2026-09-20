-- =============================================================================
-- Game Story Data Queries (Three Stars, Goal Highlights, Shootout Attempts)
-- =============================================================================

-- name: UpsertGameThreeStar :exec
INSERT INTO game_three_stars (game_id, star, player_id)
VALUES ($1, $2, $3)
ON CONFLICT (game_id, star) DO UPDATE SET
    player_id = EXCLUDED.player_id
WHERE game_three_stars.player_id IS DISTINCT FROM EXCLUDED.player_id;

-- name: UpsertGoalHighlight :exec
INSERT INTO goal_highlights (
    game_id, event_id, player_id, period, time_in_period,
    goals_to_date, highlight_clip_id, highlight_clip_url, discrete_clip_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (game_id, event_id) DO UPDATE SET
    player_id = EXCLUDED.player_id,
    period = EXCLUDED.period,
    time_in_period = EXCLUDED.time_in_period,
    goals_to_date = EXCLUDED.goals_to_date,
    highlight_clip_id = EXCLUDED.highlight_clip_id,
    highlight_clip_url = EXCLUDED.highlight_clip_url,
    discrete_clip_id = EXCLUDED.discrete_clip_id
WHERE (goal_highlights.player_id, goal_highlights.period,
       goal_highlights.time_in_period, goal_highlights.goals_to_date,
       goal_highlights.highlight_clip_id, goal_highlights.highlight_clip_url,
       goal_highlights.discrete_clip_id)
      IS DISTINCT FROM
      (EXCLUDED.player_id, EXCLUDED.period,
       EXCLUDED.time_in_period, EXCLUDED.goals_to_date,
       EXCLUDED.highlight_clip_id, EXCLUDED.highlight_clip_url,
       EXCLUDED.discrete_clip_id);

-- name: UpsertShootoutAttempt :exec
INSERT INTO shootout_attempts (
    game_id, sequence, player_id, team_id, shot_type, result, game_winner
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (game_id, sequence) DO UPDATE SET
    player_id = EXCLUDED.player_id,
    team_id = EXCLUDED.team_id,
    shot_type = EXCLUDED.shot_type,
    result = EXCLUDED.result,
    game_winner = EXCLUDED.game_winner
WHERE (shootout_attempts.player_id, shootout_attempts.team_id,
       shootout_attempts.shot_type, shootout_attempts.result,
       shootout_attempts.game_winner)
      IS DISTINCT FROM
      (EXCLUDED.player_id, EXCLUDED.team_id,
       EXCLUDED.shot_type, EXCLUDED.result,
       EXCLUDED.game_winner);

-- name: GetTeamIDByAbbrev :one
-- Look up team_id from abbreviation for a given season
SELECT team_id FROM season_teams
WHERE abbrev = $1 AND season = $2;

-- name: GetGameThreeStars :many
SELECT ts.*, p.first_name, p.last_name, p.position
FROM game_three_stars ts
JOIN players p ON ts.player_id = p.id
WHERE ts.game_id = $1
ORDER BY ts.star;

-- name: GetGoalHighlights :many
SELECT gh.*, p.first_name, p.last_name
FROM goal_highlights gh
JOIN players p ON gh.player_id = p.id
WHERE gh.game_id = $1
ORDER BY gh.period, gh.time_in_period;

-- name: GetShootoutAttempts :many
SELECT sa.*, p.first_name, p.last_name
FROM shootout_attempts sa
JOIN players p ON sa.player_id = p.id
WHERE sa.game_id = $1
ORDER BY sa.sequence;

-- name: GetPlayerThreeStarSelections :many
-- Get all three-star selections for a player
SELECT ts.star, g.game_date, g.season
FROM game_three_stars ts
JOIN games g ON ts.game_id = g.id
WHERE ts.player_id = $1
ORDER BY g.game_date DESC;

-- name: CountPlayerThreeStars :one
-- Count three-star selections by type for a player in a season
SELECT
    COUNT(*) FILTER (WHERE star = 1)::int as first_stars,
    COUNT(*) FILTER (WHERE star = 2)::int as second_stars,
    COUNT(*) FILTER (WHERE star = 3)::int as third_stars
FROM game_three_stars ts
JOIN games g ON ts.game_id = g.id
WHERE ts.player_id = $1 AND g.season = $2;
