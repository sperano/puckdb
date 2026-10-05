-- Maurice users, sessions and usage.
--
-- Identity is the trusted Authentik subject (X-authentik-uid); username and
-- display name are mutable snapshots. A PuckDB session stores only a keyed
-- hash of its opaque cookie. Turns group one prompt with every LLM call and
-- tool call that produced its answer; messages of failed or cancelled turns
-- are kept (they are the exact provider prompts) but are never part of the
-- replayable transcript. Nothing here expires: prompts and tool payloads are
-- kept forever.

CREATE TABLE app_users (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    auth_provider text NOT NULL CHECK (auth_provider <> ''),
    auth_subject text NOT NULL CHECK (auth_subject <> ''),
    username text NOT NULL,
    display_name text,
    first_seen_at timestamp with time zone DEFAULT now() NOT NULL,
    profile_updated_at timestamp with time zone DEFAULT now() NOT NULL,
    disabled_at timestamp with time zone,
    UNIQUE (auth_provider, auth_subject)
);

CREATE INDEX idx_app_users_username ON app_users (username);

CREATE TABLE app_sessions (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES app_users(id),
    session_key_hash bytea NOT NULL UNIQUE,
    started_at timestamp with time zone DEFAULT now() NOT NULL,
    last_activity_at timestamp with time zone DEFAULT now() NOT NULL,
    ended_at timestamp with time zone,
    CHECK (last_activity_at >= started_at),
    CHECK (ended_at IS NULL OR ended_at >= started_at)
);

CREATE INDEX idx_app_sessions_user_started ON app_sessions (user_id, started_at DESC);
CREATE INDEX idx_app_sessions_user_activity ON app_sessions (user_id, last_activity_at DESC)
    WHERE ended_at IS NULL;

-- Conversations created before ownership existed have no owner to give them;
-- they are deleted (their messages cascade).
DELETE FROM maurice_conversations;

ALTER TABLE maurice_conversations
    ADD COLUMN user_id uuid NOT NULL REFERENCES app_users(id);

DROP INDEX idx_maurice_conversations_updated;
CREATE INDEX idx_maurice_conversations_user_updated
    ON maurice_conversations (user_id, updated_at DESC);

-- Error classes are bounded labels, never provider error text (which may
-- carry content or credentials).
CREATE TABLE maurice_turns (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    conversation_id uuid NOT NULL REFERENCES maurice_conversations(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES app_users(id),
    turn_number integer NOT NULL CHECK (turn_number >= 0),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 200),
    request_hash bytea NOT NULL,
    status text DEFAULT 'running' NOT NULL
        CHECK (status IN ('running', 'succeeded', 'failed', 'cancelled')),
    started_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    completed_at timestamp with time zone,
    error_class text CHECK (error_class IN
        ('timeout', 'cancelled', 'provider_error', 'tool_error', 'internal_error', 'abandoned')),
    UNIQUE (conversation_id, turn_number),
    -- Keyed per user, not per conversation, so a retried first prompt of a
    -- new conversation finds its turn instead of opening a second conversation.
    UNIQUE (user_id, idempotency_key),
    UNIQUE (id, conversation_id),
    CHECK ((status = 'running') = (completed_at IS NULL)),
    CHECK (completed_at IS NULL OR completed_at >= started_at),
    CHECK ((status IN ('running', 'succeeded')) = (error_class IS NULL))
);

CREATE UNIQUE INDEX maurice_turns_one_running
    ON maurice_turns (conversation_id) WHERE status = 'running';

ALTER TABLE maurice_messages
    ADD COLUMN turn_id uuid NOT NULL,
    ADD COLUMN message_number integer NOT NULL CHECK (message_number >= 0),
    ADD CONSTRAINT maurice_messages_turn_fkey FOREIGN KEY (turn_id, conversation_id)
        REFERENCES maurice_turns(id, conversation_id) ON DELETE CASCADE,
    ADD CONSTRAINT maurice_messages_turn_number_key UNIQUE (turn_id, message_number);

CREATE TABLE maurice_llm_calls (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    turn_id uuid,
    conversation_id uuid NOT NULL REFERENCES maurice_conversations(id) ON DELETE CASCADE,
    call_kind text NOT NULL CHECK (call_kind IN ('chat_round', 'forced_final', 'title_generation')),
    round_number integer CHECK (round_number >= 0),
    -- Not unique: some providers (Ollama) draw request IDs from a tiny range.
    provider_request_id text,
    provider text NOT NULL,
    model text NOT NULL,
    status text NOT NULL CHECK (status IN ('succeeded', 'failed')),
    finish_reason text,
    started_at timestamp with time zone NOT NULL,
    completed_at timestamp with time zone NOT NULL,
    -- NULL means the provider did not report the count, which differs from 0.
    input_tokens bigint CHECK (input_tokens >= 0),
    output_tokens bigint CHECK (output_tokens >= 0),
    cache_creation_input_tokens bigint CHECK (cache_creation_input_tokens >= 0),
    cache_read_input_tokens bigint CHECK (cache_read_input_tokens >= 0),
    error_class text CHECK (error_class IN
        ('timeout', 'cancelled', 'provider_error', 'tool_error', 'internal_error', 'abandoned')),
    system_prompt text,
    -- Trailing user instruction sent after the linked messages (title generation).
    instruction text,
    tool_definitions jsonb,
    FOREIGN KEY (turn_id, conversation_id)
        REFERENCES maurice_turns(id, conversation_id) ON DELETE CASCADE,
    CHECK (completed_at >= started_at),
    CHECK ((call_kind = 'title_generation') = (turn_id IS NULL)),
    CHECK ((call_kind = 'title_generation') = (round_number IS NULL)),
    CHECK ((status = 'failed') = (error_class IS NOT NULL))
);

CREATE UNIQUE INDEX maurice_llm_calls_turn_round
    ON maurice_llm_calls (turn_id, round_number, call_kind) WHERE turn_id IS NOT NULL;
CREATE INDEX idx_maurice_llm_calls_conversation_started
    ON maurice_llm_calls (conversation_id, started_at);
CREATE INDEX idx_maurice_llm_calls_provider_request
    ON maurice_llm_calls (provider, provider_request_id) WHERE provider_request_id IS NOT NULL;

-- input_number is the message's position in the request, system prompt
-- excluded (it is stored on the call).
CREATE TABLE maurice_llm_call_messages (
    llm_call_id bigint NOT NULL REFERENCES maurice_llm_calls(id) ON DELETE CASCADE,
    input_number integer NOT NULL CHECK (input_number >= 0),
    message_id uuid NOT NULL REFERENCES maurice_messages(id) ON DELETE CASCADE,
    PRIMARY KEY (llm_call_id, input_number),
    UNIQUE (llm_call_id, message_id)
);

CREATE INDEX idx_maurice_llm_call_messages_message ON maurice_llm_call_messages (message_id);

CREATE TABLE maurice_tool_calls (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    turn_id uuid NOT NULL REFERENCES maurice_turns(id) ON DELETE CASCADE,
    llm_call_id bigint NOT NULL REFERENCES maurice_llm_calls(id) ON DELETE CASCADE,
    sequence_number integer NOT NULL CHECK (sequence_number >= 0),
    provider_tool_call_id text,
    tool_name text NOT NULL,
    arguments jsonb,
    arguments_raw text NOT NULL,
    result text,
    status text NOT NULL CHECK (status IN ('succeeded', 'tool_error', 'parse_error', 'cancelled')),
    started_at timestamp with time zone,
    completed_at timestamp with time zone,
    UNIQUE (llm_call_id, sequence_number),
    -- cancelled = requested by the model but never run.
    CHECK ((status = 'cancelled') = (started_at IS NULL)),
    CHECK ((status = 'cancelled') = (result IS NULL)),
    CHECK ((started_at IS NULL) = (completed_at IS NULL)),
    CHECK (completed_at IS NULL OR completed_at >= started_at)
);

CREATE INDEX idx_maurice_tool_calls_tool_started ON maurice_tool_calls (tool_name, started_at);
CREATE INDEX idx_maurice_tool_calls_turn ON maurice_tool_calls (turn_id);

-- Per-user usage. Each one-to-many source is aggregated on its own before the
-- join so rows never multiply. Active use is the time spent processing
-- prompts: terminal turn durations plus title generation calls (which have no
-- turn). Title generation tokens count toward the user.
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
