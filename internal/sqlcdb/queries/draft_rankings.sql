-- Draft ranking snapshots, their players and refresh attempts (migration
-- 000010). Snapshots are immutable: readers serve the latest by as_of.

-- name: CreateDraftRankingSnapshot :one
INSERT INTO draft_ranking_snapshots (
    season, league_id, league_key, identity, rules_hash, projection_snapshot_id,
    adjustment_run_id, as_of, meta
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id;

-- name: CreateDraftRankingPlayer :exec
INSERT INTO draft_ranking_players (snapshot_id, player_key, baseline_rank, player)
VALUES ($1, $2, $3, $4);

-- name: GetDraftRankingSnapshot :one
SELECT * FROM draft_ranking_snapshots WHERE id = $1;

-- name: GetLatestDraftRankingSnapshot :one
SELECT * FROM draft_ranking_snapshots
WHERE season = $1 AND league_id = $2
ORDER BY as_of DESC, created_at DESC, id DESC
LIMIT 1;

-- name: ListDraftRankingPlayers :many
SELECT player_key, baseline_rank, player FROM draft_ranking_players
WHERE snapshot_id = $1
ORDER BY baseline_rank, player_key;

-- name: PruneDraftRankingSnapshots :execrows
-- Deletes all but the newest keep snapshots of a league.
DELETE FROM draft_ranking_snapshots s
WHERE s.season = @season AND s.league_id = @league_id
  AND s.id NOT IN (
    SELECT k.id FROM draft_ranking_snapshots k
    WHERE k.season = @season AND k.league_id = @league_id
    ORDER BY k.as_of DESC, k.created_at DESC, k.id DESC
    LIMIT @keep::integer
  );

-- name: LockDraftRankingLeague :exec
-- Serializes snapshot writes of one league within a transaction.
SELECT pg_advisory_xact_lock(hashtext('draft-ranking:' || @season::integer || ':' || @league_id::integer));

-- name: StartDraftRankingRefresh :one
INSERT INTO draft_ranking_refreshes (run_id, season, league_id, status, started_at)
VALUES ($1, $2, $3, 'running', $4)
ON CONFLICT (run_id, season, league_id) DO UPDATE
SET status = 'running', state = '', error = '', snapshot_id = NULL,
    started_at = EXCLUDED.started_at, finished_at = NULL
RETURNING id;

-- name: FinishDraftRankingRefresh :exec
UPDATE draft_ranking_refreshes
SET status = $2, state = $3, error = $4, snapshot_id = $5, league_key = $6, finished_at = $7
WHERE id = $1;

-- name: CancelDraftRankingRefreshes :execrows
-- Marks a run's unfinished attempts canceled.
UPDATE draft_ranking_refreshes
SET status = 'canceled', state = 'canceled', finished_at = $2
WHERE run_id = $1 AND status = 'running';

-- name: GetLatestDraftRankingRefresh :one
SELECT * FROM draft_ranking_refreshes
WHERE season = $1 AND league_id = $2
ORDER BY started_at DESC, id DESC
LIMIT 1;

-- name: ListLatestYahooLeagueRuleSnapshots :many
-- The latest rules of every league of a season.
SELECT DISTINCT ON (league_id) * FROM yahoo_league_rule_snapshots
WHERE season = $1
ORDER BY league_id, last_seen_at DESC, id DESC;

-- name: FindYahooLeagueRuleSnapshotByKey :one
-- Resolves a league key to its season and numeric league ID.
SELECT season, league_id FROM yahoo_league_rule_snapshots
WHERE league_key = $1
ORDER BY last_seen_at DESC, id DESC
LIMIT 1;

-- name: CountNewsAdjustmentOverrideChanges :one
-- Counts overrides of a league (or of every league) created, reset or
-- expired after since and by now: a snapshot built at since does not
-- reflect them.
SELECT COUNT(*) FROM news_adjustment_overrides
WHERE (league_key = @league_key OR league_key = '')
  AND ((created_at > @since AND created_at <= @now)
    OR (reset_at > @since AND reset_at <= @now)
    OR (expires_at > @since AND expires_at <= @now));

