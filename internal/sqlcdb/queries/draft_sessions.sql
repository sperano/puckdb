-- Live draft sessions, their events and poll observations (migrations
-- 000019, 000021), the shortlist, and the per-league sync advisory lock.
-- A store runs each mutation's queries in one transaction; the row lock
-- (LockDraftSession*) is what serializes concurrent writers.

-- name: EnsureDraftSession :exec
INSERT INTO draft_sessions (league_key, season, league_id, game_key)
VALUES (@league_key, @season, @league_id, @game_key)
ON CONFLICT (league_key) DO NOTHING;

-- name: GetDraftSessionIdentity :one
SELECT season, league_id, game_key FROM draft_sessions WHERE league_key = @league_key;

-- name: LockDraftSessionBoard :one
SELECT board, state_version FROM draft_sessions WHERE league_key = @league_key FOR UPDATE;

-- name: LockDraftSessionVersion :one
SELECT state_version FROM draft_sessions WHERE league_key = @league_key FOR UPDATE;

-- name: GetDraftSession :one
SELECT season, league_id, game_key, board, state_version, draft_status,
       recommendations_safe, complete, upstream_pick_count, skipped_pick_count,
       last_poll_at, last_success_at, last_authoritative_at, last_error,
       sync_version, updated_at
FROM draft_sessions WHERE league_key = @league_key;

-- name: UpdateDraftSessionPoll :exec
UPDATE draft_sessions SET state_version = @state_version, draft_status = @draft_status,
    board = @board, board_hash = @board_hash,
    recommendations_safe = @recommendations_safe, complete = @complete,
    upstream_pick_count = @upstream_pick_count, skipped_pick_count = @skipped_pick_count,
    last_poll_at = @polled_at, last_success_at = @polled_at,
    last_authoritative_at = CASE WHEN @authoritative::boolean THEN @polled_at ELSE last_authoritative_at END,
    last_error = '', sync_version = sync_version + 1, updated_at = @polled_at
WHERE league_key = @league_key;

-- name: UpdateDraftSessionFailure :exec
UPDATE draft_sessions SET last_poll_at = @polled_at, last_error = @last_error,
    recommendations_safe = false, sync_version = sync_version + 1, updated_at = @polled_at
WHERE league_key = @league_key;

-- name: UpdateDraftSessionBoard :exec
UPDATE draft_sessions SET state_version = @state_version, board = @board, board_hash = @board_hash,
    recommendations_safe = (@safe_to_recommend::boolean AND last_error = ''),
    sync_version = sync_version + 1, updated_at = @updated_at
WHERE league_key = @league_key;

-- name: BumpDraftSessionSyncVersion :exec
UPDATE draft_sessions SET sync_version = sync_version + 1, updated_at = @updated_at
WHERE league_key = @league_key;

-- name: InsertDraftSessionEvent :exec
INSERT INTO draft_session_events (league_key, state_version, kind, details, created_at)
VALUES (@league_key, @state_version, @kind, @details, @created_at);

-- name: ListDraftSessionEvents :many
SELECT state_version, kind, details, created_at
FROM draft_session_events
WHERE league_key = @league_key AND state_version > @after_version
ORDER BY state_version ASC
LIMIT @max_events;

-- name: InsertDraftSessionObservation :exec
INSERT INTO draft_session_observations (
    league_key, polled_at, duration_ms, success, authoritative, changed, draft_status,
    declared_count, parsed_count, skipped_count, snapshot_hash, error_class, error
) VALUES (@league_key, @polled_at, @duration_ms, @success, @authoritative, @changed, @draft_status,
    @declared_count, @parsed_count, @skipped_count, @snapshot_hash, @error_class, @error);

-- name: ListDraftSessionObservations :many
SELECT polled_at, duration_ms, success, authoritative, changed, draft_status,
       declared_count, parsed_count, skipped_count, snapshot_hash, error_class, error
FROM draft_session_observations WHERE league_key = @league_key
ORDER BY polled_at DESC, id DESC
LIMIT @max_observations;

-- The sync lock is a session advisory lock: it must run on one dedicated
-- connection, outside any transaction. The key expression is the one
-- released binaries use, so both exclude each other during a rolling
-- restart (TestSessionSyncLockExcludesLegacyLockStatement).

-- name: TryDraftSessionSyncLock :one
SELECT pg_try_advisory_lock(hashtextextended('draft-session-sync:' || @league_key::text, 0));

-- name: ReleaseDraftSessionSyncLock :one
SELECT pg_advisory_unlock(hashtextextended('draft-session-sync:' || @league_key::text, 0));

-- name: ListDraftShortlist :many
SELECT player_key FROM draft_shortlist WHERE league_key = @league_key
ORDER BY created_at, player_key;

-- name: AddDraftShortlistPlayer :execrows
INSERT INTO draft_shortlist (league_key, player_key, created_at)
VALUES (@league_key, @player_key, @created_at)
ON CONFLICT (league_key, player_key) DO NOTHING;

-- name: RemoveDraftShortlistPlayer :execrows
DELETE FROM draft_shortlist WHERE league_key = @league_key AND player_key = @player_key;
