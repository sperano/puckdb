-- Conversations deleted by the up migration cannot be restored, and rolling
-- back loses users, sessions, turns and usage. Transcripts survive, unowned as
-- before; messages of turns that did not succeed are deleted first because the
-- previous schema would replay them as history, and soft-deleted
-- conversations are removed because it had no way to hide them.
DROP VIEW app_user_usage;
DROP TABLE maurice_tool_calls;
DROP TABLE maurice_llm_call_messages;
DROP TABLE maurice_llm_calls;

DELETE FROM maurice_messages m
USING maurice_turns t
WHERE t.id = m.turn_id AND t.status <> 'succeeded';

-- The previous schema deleted conversations outright.
DELETE FROM maurice_conversations WHERE deleted_at IS NOT NULL;

ALTER TABLE maurice_messages
    DROP CONSTRAINT maurice_messages_turn_number_key,
    DROP CONSTRAINT maurice_messages_turn_fkey,
    DROP COLUMN message_number,
    DROP COLUMN turn_id;

DROP TABLE maurice_turns;

DROP INDEX idx_maurice_conversations_user_updated;
ALTER TABLE maurice_conversations DROP COLUMN deleted_at, DROP COLUMN user_id;
CREATE INDEX idx_maurice_conversations_updated ON maurice_conversations USING btree (updated_at DESC);

DROP TABLE app_sessions;
DROP TABLE app_users;
