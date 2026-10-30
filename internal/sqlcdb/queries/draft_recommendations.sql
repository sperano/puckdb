-- Replayable draft recommendation runs (migration 000020): the complete
-- input and result of each run, with indexed version columns for audits.

-- name: InsertDraftRecommendationRun :one
INSERT INTO draft_recommendation_runs (
    league_key, session_version, ranking_snapshot_id, ranking_identity, ranking_version,
    projection_snapshot_id, projection_version, rules_hash, scenario,
    strategy, input, result, generated_at, latency_milliseconds
) VALUES (@league_key, @session_version, @ranking_snapshot_id, @ranking_identity, @ranking_version,
    @projection_snapshot_id, @projection_version, @rules_hash, @scenario,
    @strategy, @input, @result, @generated_at, @latency_milliseconds)
RETURNING id;

-- name: GetDraftRecommendationRun :one
SELECT input, result FROM draft_recommendation_runs WHERE id = @id;

-- name: GetLatestDraftRecommendationRun :one
SELECT id, input, result FROM draft_recommendation_runs
WHERE league_key = @league_key AND session_version = @session_version
ORDER BY generated_at DESC, id DESC
LIMIT 1;
