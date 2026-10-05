-- name: UpsertAppUser :one
-- profile_updated_at moves only when the username or display name changed.
-- The upsert always takes the row lock (DO UPDATE, never DO NOTHING), which
-- serializes session resolution per user for the rest of the transaction.
INSERT INTO app_users (auth_provider, auth_subject, username, display_name)
VALUES ($1, $2, $3, $4)
ON CONFLICT (auth_provider, auth_subject) DO UPDATE
SET username = EXCLUDED.username,
    display_name = EXCLUDED.display_name,
    profile_updated_at = CASE
        WHEN (app_users.username, app_users.display_name)
             IS DISTINCT FROM (EXCLUDED.username, EXCLUDED.display_name)
        THEN now()
        ELSE app_users.profile_updated_at
    END
RETURNING id, disabled_at;

-- name: TouchSessionByHash :one
UPDATE app_sessions
SET last_activity_at = GREATEST(last_activity_at, now())
WHERE session_key_hash = $1 AND user_id = $2 AND ended_at IS NULL
  AND last_activity_at > now() - make_interval(secs => sqlc.arg(idle_seconds)::double precision)
RETURNING id;

-- name: TouchLatestActiveSession :one
UPDATE app_sessions
SET last_activity_at = GREATEST(last_activity_at, now())
WHERE id = (
    SELECT s.id FROM app_sessions s
    WHERE s.user_id = $1 AND s.ended_at IS NULL
      AND s.last_activity_at > now() - make_interval(secs => sqlc.arg(idle_seconds)::double precision)
    ORDER BY s.last_activity_at DESC
    LIMIT 1
)
RETURNING id;

-- name: EndIdleSessions :exec
-- An idle session ends when its idle window closed, not when it is noticed.
UPDATE app_sessions
SET ended_at = last_activity_at + make_interval(secs => sqlc.arg(idle_seconds)::double precision)
WHERE user_id = $1 AND ended_at IS NULL
  AND last_activity_at <= now() - make_interval(secs => sqlc.arg(idle_seconds)::double precision);

-- name: CreateAppSession :one
INSERT INTO app_sessions (user_id, session_key_hash)
VALUES ($1, $2)
RETURNING id;
