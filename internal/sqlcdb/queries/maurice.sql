-- name: CreateConversation :one
INSERT INTO maurice_conversations (title)
VALUES ($1)
RETURNING id, title, created_at, updated_at;

-- name: GetConversation :one
SELECT id, title, created_at, updated_at
FROM maurice_conversations
WHERE id = $1;

-- name: UpdateConversationTitle :exec
UPDATE maurice_conversations
SET title = $2, updated_at = NOW()
WHERE id = $1;

-- name: ListConversations :many
SELECT id, title, created_at, updated_at
FROM maurice_conversations
ORDER BY updated_at DESC
LIMIT $1;

-- name: DeleteConversation :exec
DELETE FROM maurice_conversations
WHERE id = $1;

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
-- transaction would share it and GetMessagesByConversation could not
-- reconstruct turn order. The caller stamps a strictly increasing value.
INSERT INTO maurice_messages (conversation_id, role, content, tool_calls, tool_call_id, created_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, conversation_id, role, content, tool_calls, tool_call_id, created_at;

-- name: GetMessagesByConversation :many
SELECT id, conversation_id, role, content, tool_calls, tool_call_id, created_at
FROM maurice_messages
WHERE conversation_id = $1
ORDER BY created_at ASC;
