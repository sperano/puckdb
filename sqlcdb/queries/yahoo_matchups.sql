-- =============================================================================
-- Yahoo Matchups Queries
-- =============================================================================

-- name: UpsertYahooMatchupBatch :batchexec
INSERT INTO yahoo_matchups (
    league_id, week, team1_id, team2_id,
    team1_points, team2_points, status,
    is_playoffs, is_consolation
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (league_id, week, team1_id, team2_id) DO UPDATE SET
    team1_points = EXCLUDED.team1_points,
    team2_points = EXCLUDED.team2_points,
    status = EXCLUDED.status,
    is_playoffs = EXCLUDED.is_playoffs,
    is_consolation = EXCLUDED.is_consolation,
    updated_at = NOW()
WHERE (yahoo_matchups.team1_points, yahoo_matchups.team2_points,
       yahoo_matchups.status, yahoo_matchups.is_playoffs,
       yahoo_matchups.is_consolation)
      IS DISTINCT FROM
      (EXCLUDED.team1_points, EXCLUDED.team2_points,
       EXCLUDED.status, EXCLUDED.is_playoffs,
       EXCLUDED.is_consolation);

-- name: GetYahooMatchupsByLeague :many
SELECT * FROM yahoo_matchups
WHERE league_id = $1
ORDER BY week, team1_id;

-- name: GetYahooMatchupsByWeek :many
SELECT * FROM yahoo_matchups
WHERE league_id = $1 AND week = $2
ORDER BY team1_id;

-- name: GetYahooMatchupsByTeam :many
SELECT * FROM yahoo_matchups
WHERE league_id = $1 AND (team1_id = $2 OR team2_id = $2)
ORDER BY week;

-- name: CountYahooMatchups :one
SELECT COUNT(*) FROM yahoo_matchups;

-- name: CountYahooMatchupsByLeague :one
SELECT COUNT(*) FROM yahoo_matchups WHERE league_id = $1;
