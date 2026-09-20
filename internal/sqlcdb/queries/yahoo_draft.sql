-- =============================================================================
-- Yahoo Draft Results Queries
-- =============================================================================

-- name: UpsertYahooDraftResultBatch :batchexec
INSERT INTO yahoo_draft_results (
    league_id, round, pick, team_id, player_id, cost
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (league_id, round, pick) DO UPDATE SET
    team_id = EXCLUDED.team_id,
    player_id = EXCLUDED.player_id,
    cost = EXCLUDED.cost
WHERE (yahoo_draft_results.team_id, yahoo_draft_results.player_id,
       yahoo_draft_results.cost)
      IS DISTINCT FROM
      (EXCLUDED.team_id, EXCLUDED.player_id,
       EXCLUDED.cost);

-- name: GetYahooDraftResultsByLeague :many
SELECT * FROM yahoo_draft_results
WHERE league_id = $1
ORDER BY round, pick;

-- name: GetYahooDraftResultsByTeam :many
SELECT * FROM yahoo_draft_results
WHERE league_id = $1 AND team_id = $2
ORDER BY round, pick;

-- name: CountYahooDraftResults :one
SELECT COUNT(*) FROM yahoo_draft_results;

-- name: CountYahooDraftResultsByLeague :one
SELECT COUNT(*) FROM yahoo_draft_results WHERE league_id = $1;
