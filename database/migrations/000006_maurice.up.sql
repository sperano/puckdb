-- Maurice AI assistant

CREATE TABLE maurice_conversations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE maurice_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES maurice_conversations(id) ON DELETE CASCADE,
    role chat_role NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    tool_calls JSONB,
    tool_call_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_maurice_messages_conversation ON maurice_messages(conversation_id, created_at);
CREATE INDEX idx_maurice_conversations_updated ON maurice_conversations(updated_at DESC);
