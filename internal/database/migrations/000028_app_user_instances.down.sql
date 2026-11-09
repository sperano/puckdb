-- Rolling back makes every user visible on every instance again. An identity
-- with users on two instances cannot be merged into one user (whose
-- conversations would it keep?), so the rollback refuses until an operator
-- removes or reassigns one of them.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM app_users
        GROUP BY auth_provider, auth_subject
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'app_users: an identity has users on several instances; remove or merge them before rolling back';
    END IF;
END
$$;

DROP VIEW app_user_usage;
CREATE VIEW app_user_usage AS
WITH sessions AS (
    SELECT user_id,
           count(*) AS puckdb_session_count,
           max(started_at) AS last_session_started_at
    FROM app_sessions
    GROUP BY user_id
), turns AS (
    SELECT user_id,
           count(*) AS turn_count,
           count(*) FILTER (WHERE status = 'succeeded') AS succeeded_turn_count,
           sum(extract(epoch FROM completed_at - started_at)) AS turn_seconds
    FROM maurice_turns
    GROUP BY user_id
), calls AS (
    SELECT c.user_id,
           count(*) AS llm_call_count,
           sum(l.input_tokens) AS input_tokens,
           sum(l.output_tokens) AS output_tokens,
           sum(l.cache_creation_input_tokens) AS cache_creation_input_tokens,
           sum(l.cache_read_input_tokens) AS cache_read_input_tokens,
           sum(extract(epoch FROM l.completed_at - l.started_at)) AS llm_call_seconds,
           sum(extract(epoch FROM l.completed_at - l.started_at))
               FILTER (WHERE l.call_kind = 'title_generation') AS title_seconds
    FROM maurice_llm_calls l
    JOIN maurice_conversations c ON c.id = l.conversation_id
    GROUP BY c.user_id
)
SELECT u.id AS user_id,
       u.username,
       COALESCE(s.puckdb_session_count, 0) AS puckdb_session_count,
       s.last_session_started_at,
       COALESCE(t.turn_count, 0) AS turn_count,
       COALESCE(t.succeeded_turn_count, 0) AS succeeded_turn_count,
       COALESCE(c.llm_call_count, 0) AS llm_call_count,
       COALESCE(c.input_tokens, 0) AS input_tokens,
       COALESCE(c.output_tokens, 0) AS output_tokens,
       COALESCE(c.cache_creation_input_tokens, 0) AS cache_creation_input_tokens,
       COALESCE(c.cache_read_input_tokens, 0) AS cache_read_input_tokens,
       COALESCE(c.llm_call_seconds, 0) AS llm_call_seconds,
       COALESCE(t.turn_seconds, 0) + COALESCE(c.title_seconds, 0) AS active_seconds
FROM app_users u
LEFT JOIN sessions s ON s.user_id = u.id
LEFT JOIN turns t ON t.user_id = u.id
LEFT JOIN calls c ON c.user_id = u.id;

ALTER TABLE app_users
    DROP CONSTRAINT app_users_instance_auth_provider_auth_subject_key,
    ADD CONSTRAINT app_users_auth_provider_auth_subject_key UNIQUE (auth_provider, auth_subject);
ALTER TABLE app_users DROP COLUMN instance;
