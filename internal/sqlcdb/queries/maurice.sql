-- Every conversation query is scoped by its owner: knowing a conversation
-- UUID is not authorization. A conversation with deleted_at set is gone for
-- its owner, but its rows are kept (prompts and usage are kept forever).

-- name: CreateConversation :one
INSERT INTO maurice_conversations (user_id)
VALUES ($1)
RETURNING id, title, created_at, updated_at;

-- name: GetConversation :one
SELECT id, title, created_at, updated_at
FROM maurice_conversations
WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL;

-- name: LockConversation :one
-- Serializes turn starts on one conversation for the rest of the transaction.
SELECT id
FROM maurice_conversations
WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
FOR UPDATE;

-- name: LockOwnedConversation :one
-- Ownership check that also matches a deleted conversation, for usage that
-- must be recorded even after the owner deleted it (title generation).
SELECT id
FROM maurice_conversations
WHERE id = $1 AND user_id = $2
FOR UPDATE;

-- name: UpdateConversationTitle :exec
UPDATE maurice_conversations
SET title = $3, updated_at = NOW()
WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL;

-- name: ListConversations :many
SELECT id, title, created_at, updated_at
FROM maurice_conversations
WHERE user_id = $1 AND deleted_at IS NULL
ORDER BY updated_at DESC
LIMIT $2;

-- name: DeleteConversation :execrows
-- Hides the conversation; nothing is removed.
UPDATE maurice_conversations
SET deleted_at = clock_timestamp()
WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL;

-- name: TouchConversation :exec
-- Bumps the conversation's activity timestamp. Runs in the same transaction
-- as the message inserts so ListConversations (ORDER BY updated_at DESC)
-- only surfaces a conversation once its turn committed. clock_timestamp()
-- rather than NOW(): NOW() is the transaction START time, which would put
-- updated_at before the messages stamped during the transaction and order
-- overlapping turns by begin time instead of by when they actually landed.
UPDATE maurice_conversations
SET updated_at = clock_timestamp()
WHERE id = $1;

-- name: CreateMessage :one
-- created_at is supplied explicitly rather than left to DEFAULT NOW(): NOW()
-- is the transaction start time, so every message of a turn inserted in one
-- transaction would share it. The caller stamps a strictly increasing value.
INSERT INTO maurice_messages (conversation_id, turn_id, message_number, role, content, created_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, conversation_id, role, content, created_at;

-- name: GetTranscript :many
-- The replayable transcript: messages of succeeded turns only. Messages of
-- failed or cancelled turns are kept as exact provider prompts but must never
-- be replayed as history.
SELECT m.id, m.conversation_id, m.role, m.content,
       result_call.provider_tool_call_id AS tool_call_id, m.created_at
FROM maurice_messages m
JOIN maurice_turns t ON t.id = m.turn_id
JOIN maurice_conversations c ON c.id = m.conversation_id
LEFT JOIN maurice_tool_calls result_call ON result_call.id = m.result_tool_call_id
WHERE m.conversation_id = $1 AND c.user_id = $2 AND c.deleted_at IS NULL AND t.status = 'succeeded'
ORDER BY t.turn_number, m.message_number;

-- name: GetTranscriptToolCalls :many
SELECT tool_call.message_id, tool_call.provider_tool_call_id, tool_call.tool_type,
       tool_call.tool_name, tool_call.arguments_raw
FROM maurice_tool_calls tool_call
JOIN maurice_messages message ON message.id = tool_call.message_id
JOIN maurice_turns turn_row ON turn_row.id = message.turn_id
JOIN maurice_conversations conversation ON conversation.id = message.conversation_id
WHERE message.conversation_id = $1
  AND conversation.user_id = $2
  AND conversation.deleted_at IS NULL
  AND turn_row.status = 'succeeded'
ORDER BY turn_row.turn_number, message.message_number, tool_call.sequence_number;

-- name: GetTurnByKey :one
SELECT t.id, t.conversation_id, t.turn_number, t.request_hash, t.status, t.error_class, t.warnings,
       (t.status = 'running' AND t.started_at < clock_timestamp() - make_interval(secs => sqlc.arg(stale_seconds)::double precision))::boolean AS stale,
       (c.deleted_at IS NOT NULL)::boolean AS conversation_deleted
FROM maurice_turns t
JOIN maurice_conversations c ON c.id = t.conversation_id
WHERE t.user_id = $1 AND t.idempotency_key = $2;

-- name: AbandonTurn :exec
UPDATE maurice_turns
SET status = 'failed', error_class = 'abandoned', completed_at = clock_timestamp()
WHERE id = $1 AND status = 'running';

-- name: AbandonStaleTurns :exec
-- A running turn older than the stale cutoff belongs to a request that died
-- without finishing it; failing it frees the conversation.
UPDATE maurice_turns
SET status = 'failed', error_class = 'abandoned', completed_at = clock_timestamp()
WHERE conversation_id = $1 AND status = 'running'
  AND started_at < clock_timestamp() - make_interval(secs => sqlc.arg(stale_seconds)::double precision);

-- name: InsertTurn :one
INSERT INTO maurice_turns (conversation_id, user_id, turn_number, idempotency_key, request_hash)
VALUES ($1, $2,
        (SELECT COALESCE(MAX(turn_number) + 1, 0) FROM maurice_turns WHERE conversation_id = $1),
        $3, $4)
RETURNING id, turn_number;

-- name: CompleteTurn :one
UPDATE maurice_turns
SET status = $2, error_class = $3, warnings = $4, completed_at = clock_timestamp()
WHERE id = $1 AND status = 'running'
RETURNING conversation_id;

-- name: GetTurnFinalMessage :one
SELECT id, content
FROM maurice_messages
WHERE turn_id = $1
ORDER BY message_number DESC
LIMIT 1;

-- name: GetTurnToolNames :many
SELECT tool_name
FROM maurice_tool_calls tool_call
JOIN maurice_messages message ON message.id = tool_call.message_id
WHERE tool_call.turn_id = $1
ORDER BY message.message_number, tool_call.sequence_number;

-- name: InsertLLMCall :one
INSERT INTO maurice_llm_calls (
    turn_id, conversation_id, call_kind, round_number, provider_request_id, provider, model,
    status, finish_reason, started_at, completed_at, input_tokens, output_tokens,
    cache_creation_input_tokens, cache_read_input_tokens, error_class, system_prompt,
    instruction, tool_definitions
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19
)
RETURNING id;

-- name: InsertLLMCallMessages :exec
INSERT INTO maurice_llm_call_messages (llm_call_id, input_number, message_id)
-- Both arrays have one entry per input; set-returning functions in the select
-- list advance in lockstep.
SELECT sqlc.arg(llm_call_id)::bigint,
       unnest(sqlc.arg(input_numbers)::integer[]),
       unnest(sqlc.arg(message_ids)::uuid[]);

-- name: InsertToolCall :one
INSERT INTO maurice_tool_calls (
    turn_id, llm_call_id, message_id, sequence_number, provider_tool_call_id,
    tool_type, tool_name, arguments, arguments_raw, result, status, started_at, completed_at
) VALUES (
    sqlc.arg(turn_id), sqlc.arg(llm_call_id), sqlc.arg(message_id),
    sqlc.arg(sequence_number), sqlc.arg(provider_tool_call_id), sqlc.arg(tool_type),
    sqlc.arg(tool_name),
    -- OFFSET 0 keeps PostgreSQL from evaluating the cast before the validity filter.
    (SELECT valid.raw::jsonb
     FROM (SELECT sqlc.arg(arguments_raw)::text AS raw
           WHERE pg_input_is_valid(sqlc.arg(arguments_raw)::text, 'jsonb')
           OFFSET 0) valid),
    sqlc.arg(arguments_raw), sqlc.arg(result), sqlc.arg(status),
    sqlc.arg(started_at), sqlc.arg(completed_at)
)
RETURNING id;

-- name: InsertTranscriptToolCall :one
INSERT INTO maurice_tool_calls (
    turn_id, message_id, sequence_number, provider_tool_call_id, tool_type,
    tool_name, arguments, arguments_raw
) VALUES (
    sqlc.arg(turn_id), sqlc.arg(message_id), sqlc.arg(sequence_number),
    sqlc.arg(provider_tool_call_id), sqlc.arg(tool_type), sqlc.arg(tool_name),
    -- OFFSET 0 keeps PostgreSQL from evaluating the cast before the validity filter.
    (SELECT valid.raw::jsonb
     FROM (SELECT sqlc.arg(arguments_raw)::text AS raw
           WHERE pg_input_is_valid(sqlc.arg(arguments_raw)::text, 'jsonb')
           OFFSET 0) valid),
    sqlc.arg(arguments_raw)
)
RETURNING id;

-- name: LinkMessageResultToolCall :exec
UPDATE maurice_messages
SET result_tool_call_id = $2
WHERE id = $1;
