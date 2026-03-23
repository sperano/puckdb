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

-- name: CreateMessage :one
INSERT INTO maurice_messages (conversation_id, role, content, tool_calls, tool_call_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, conversation_id, role, content, tool_calls, tool_call_id, created_at;

-- name: GetMessagesByConversation :many
SELECT id, conversation_id, role, content, tool_calls, tool_call_id, created_at
FROM maurice_messages
WHERE conversation_id = $1
ORDER BY created_at ASC;
