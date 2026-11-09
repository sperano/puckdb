-- Users per instance.
--
-- Every API instance (the main puckdb API and each Maurice instance) shares
-- this database, so an identity was one user, with the same conversations and
-- sessions, on all of them. A user now belongs to one instance (the API's
-- --instance / PUCKDB_INSTANCE): the same person on two instances is two users,
-- and logging in to one grants nothing on another. Conversations, turns and
-- sessions stay scoped through their user.
--
-- Existing users belong to the main instance, 'puckdb'.
--
-- Rollout: run it and 000029 together with moving every API instance to the
-- build that ships them. An older API's login upsert (ON CONFLICT
-- (auth_provider, auth_subject), no instance) fails once 000028 has run, and
-- the newer one's (which also writes 000029's email column) fails until both
-- have, so user resolution (Maurice, sessions) fails on any instance whose
-- build does not match the schema in between.

ALTER TABLE app_users ADD COLUMN instance text NOT NULL DEFAULT 'puckdb' CHECK (instance <> '');
ALTER TABLE app_users ALTER COLUMN instance DROP DEFAULT;

ALTER TABLE app_users
    DROP CONSTRAINT app_users_auth_provider_auth_subject_key,
    ADD CONSTRAINT app_users_instance_auth_provider_auth_subject_key
        UNIQUE (instance, auth_provider, auth_subject);

-- Same view as 000022, plus the user's instance.
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
       u.instance,
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
